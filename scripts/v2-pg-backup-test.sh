#!/usr/bin/env bash
set -Eeuo pipefail

ROOT="$(git rev-parse --show-toplevel)"
cd "$ROOT"
command -v docker >/dev/null || { echo 'v2-pg-backup-test: Docker required' >&2; exit 1; }
docker info >/dev/null || { echo 'v2-pg-backup-test: Docker daemon unavailable' >&2; exit 1; }
command -v sha256sum >/dev/null || { echo 'v2-pg-backup-test: sha256sum required' >&2; exit 1; }

tmp="$(mktemp -d "${TMPDIR:-/tmp}/ginbar-v2-backup.XXXXXX")"
container="ginbar-v2-backup-$$"
cleanup() {
  local status=$?
  trap - EXIT
  docker rm -f "$container" >/dev/null 2>&1 || true
  rm -rf "$tmp"
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

image="${GINBAR_PG_BACKUP_IMAGE:-postgres:17.11-alpine}"
docker run --rm -d --name "$container" \
  -e POSTGRES_USER=ginbar_fixture -e POSTGRES_PASSWORD=disposable_only \
  -e POSTGRES_DB=postgres "$image" >/dev/null
for i in $(seq 1 60); do
  if docker exec "$container" pg_isready -U ginbar_fixture -d postgres >/dev/null 2>&1; then break; fi
  sleep 1
done
docker exec "$container" pg_isready -U ginbar_fixture -d postgres >/dev/null
pg() { docker exec -i "$container" psql -X -v ON_ERROR_STOP=1 -U ginbar_fixture "$@"; }
dump() { docker exec "$container" pg_dump -U ginbar_fixture "$@"; }

pg -d postgres -c 'CREATE DATABASE ginbar_source'
pg -d postgres -c 'CREATE DATABASE ginbar_restore'

# Run every checked-in migration, sorted by name, against the disposable source.
mapfile -t migrations < <(find src/backend/v2/internal/schema/migrations -maxdepth 1 -type f -name '*.sql' | LC_ALL=C sort)
(("${#migrations[@]}" > 0)) || { echo 'v2-pg-backup-test: no migrations' >&2; exit 1; }
for migration in "${migrations[@]}"; do
  pg -d ginbar_source < "$migration" > /dev/null
done

# Foreign-key-linked representative state and a nondefault identity value.
pg -d ginbar_source >/dev/null <<'SQL'
INSERT INTO users (id, username) OVERRIDING SYSTEM VALUE VALUES (42, 'backup_fixture');
INSERT INTO user_credentials (user_id, kind, secret_hash)
VALUES (42, 0, 'test-fixture-not-a-credential');
INSERT INTO posts (id, author_user_id, release_state, released_at)
OVERRIDING SYSTEM VALUE VALUES (100, 42, 1, now());
INSERT INTO tags (id, name, normalized_name, created_by_user_id)
OVERRIDING SYSTEM VALUE VALUES (55, 'Fixture', 'fixture', 42);
INSERT INTO post_tags (post_id, tag_id, added_by_user_id) VALUES (100, 55, 42);
INSERT INTO comments (id, post_id, user_id, body)
OVERRIDING SYSTEM VALUE VALUES (77, 100, 42, 'backup restore');
INSERT INTO post_votes (post_id, user_id, value) VALUES (100, 42, 1);
SELECT setval(pg_get_serial_sequence('users','id'), 42, true);
SELECT setval(pg_get_serial_sequence('posts','id'), 100, true);
SELECT setval(pg_get_serial_sequence('tags','id'), 55, true);
SELECT setval(pg_get_serial_sequence('comments','id'), 77, true);
SQL

# Generate deterministic per-table row-count and content fingerprints.
# JSONB canonicalizes keys; sorting by complete row text avoids heap-order dependence.
snapshot() {
  local db="$1" out="$2"
  pg -d "$db" -At <<'SQL' > "$tmp/queries.sql"
SELECT format(
  'SELECT %L || E''\\t'' || count(*)::text || E''\\t'' || md5(coalesce(string_agg(to_jsonb(t)::text, E''\\n'' ORDER BY to_jsonb(t)::text), '''')) FROM %I.%I t;',
  table_schema || '.' || table_name, table_schema, table_name
)
FROM information_schema.tables
WHERE table_schema = 'public' AND table_type = 'BASE TABLE'
ORDER BY table_schema, table_name;
SQL
  pg -d "$db" -At < "$tmp/queries.sql" > "$out"
  pg -d "$db" -At <<'SQL' >> "$out"
SELECT 'sequence:' || schemaname || '.' || sequencename || E'\t' ||
       coalesce(last_value::text, 'NULL') || E'\t' ||
       start_value::text || E'\t' || increment_by::text
FROM pg_sequences WHERE schemaname = 'public'
ORDER BY schemaname, sequencename;
SQL
}

start="$(date +%s)"
snapshot ginbar_source "$tmp/before"
dump -Fc -d ginbar_source > "$tmp/backup.dump"
test -s "$tmp/backup.dump"
docker exec -i "$container" pg_restore -U ginbar_fixture -d ginbar_restore --exit-on-error --no-owner --no-privileges < "$tmp/backup.dump"
snapshot ginbar_restore "$tmp/after"
cmp "$tmp/before" "$tmp/after"

dump --schema-only -d ginbar_source > "$tmp/schema-before"
dump --schema-only -d ginbar_restore > "$tmp/schema-after"
cmp "$tmp/schema-before" "$tmp/schema-after"

# Exercise restored foreign keys and identity sequences without modifying source.
pg -d ginbar_restore >/dev/null <<'SQL'
BEGIN;
DO $$
DECLARE next_id bigint;
BEGIN
  INSERT INTO users(username) VALUES ('restored_sequence_probe') RETURNING id INTO next_id;
  IF next_id <= 42 THEN RAISE EXCEPTION 'restored user sequence failed: %', next_id; END IF;
  INSERT INTO user_roles(user_id, role) VALUES (next_id, 0);
END $$;
ROLLBACK;
SQL

printf 'v2-pg-backup-test: PASS tables=%s hash=%s dump_bytes=%s duration_s=%s\n' \
  "$(wc -l < "$tmp/before")" "$(sha256sum "$tmp/before" | cut -d ' ' -f 1)" \
  "$(wc -c < "$tmp/backup.dump")" "$(($(date +%s) - start))"
docker exec "$container" pg_dump --version
docker exec "$container" pg_restore --version
