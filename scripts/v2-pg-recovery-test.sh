#!/usr/bin/env bash
set -Eeuo pipefail

ROOT="$(git rev-parse --show-toplevel)"
cd "$ROOT"
command -v docker >/dev/null || { echo 'v2-pg-recovery-test: Docker required' >&2; exit 1; }
docker info >/dev/null || { echo 'v2-pg-recovery-test: Docker daemon unavailable' >&2; exit 1; }
command -v sha256sum >/dev/null || { echo 'v2-pg-recovery-test: sha256sum required' >&2; exit 1; }

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


# Run every checked-in migration, sorted by name, against the disposable source.
mapfile -t migrations < <(find src/backend/v2/internal/schema/migrations -maxdepth 1 -type f -name '*.sql' | LC_ALL=C sort)
(("${#migrations[@]}" > 0)) || { echo 'v2-pg-recovery-test: no migrations' >&2; exit 1; }
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

# Use the real operator command in a PostgreSQL client container sharing the
# disposable server's network namespace. The host bind mount is disposable.
backup_client() {
  docker run --rm --network "container:$container" \
    -v "$tmp:/work" -v "$ROOT/scripts/v2-pg-backup.sh:/backup.sh:ro" \
    -e PGHOST=127.0.0.1 -e PGPORT=5432 \
    -e PGUSER=ginbar_fixture -e PGDATABASE=ginbar_source \
    -e PGPASSWORD=disposable_only \
    "$image" bash /backup.sh "$@"
}
backup_client /work/artifact > "$tmp/backup.log" 2>&1
test -s "$tmp/artifact/backup.dump"
test -s "$tmp/artifact/metadata.txt"
test "$(stat -c %a "$tmp/artifact")" = 700
test "$(stat -c %a "$tmp/artifact/backup.dump")" = 600
test "$(stat -c %a "$tmp/artifact/metadata.txt")" = 600
grep -Fx 'format=postgresql-custom' "$tmp/artifact/metadata.txt"
grep -Fx "sha256=$(sha256sum "$tmp/artifact/backup.dump" | cut -d ' ' -f 1)" "$tmp/artifact/metadata.txt"
grep -Fx "bytes=$(wc -c < "$tmp/artifact/backup.dump")" "$tmp/artifact/metadata.txt"
grep -Eq '^created_utc=[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z
snapshot ginbar_restore "$tmp/after"
cmp "$tmp/before" "$tmp/after"

# Compare schema object inventories from PostgreSQL's native archive TOC.
# Textual pg_dump DDL is not byte-stable across restore: PostgreSQL may
# deparse a semantically identical CHECK expression with different parentheses.
# Strict pg_restore already verifies each object could be reconstructed.
schema_inventory() {
  local db="$1" out="$2"
  dump -Fc --schema-only --no-owner --no-privileges -d "$db" > "$tmp/schema-$db.dump"
  docker exec -i "$container" pg_restore --list < "$tmp/schema-$db.dump" |
    sed -nE '/^[[:digit:]]+; /{s/^[[:digit:]]+; [[:digit:]]+ [[:digit:]]+ / /;p;}' |
    LC_ALL=C sort > "$out"
}
schema_inventory ginbar_source "$tmp/schema-before"
schema_inventory ginbar_restore "$tmp/schema-after"
test -s "$tmp/schema-before"
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

printf 'v2-pg-recovery-test: PASS tables=%s hash=%s dump_bytes=%s duration_s=%s\n' \
  "$(wc -l < "$tmp/before")" "$(sha256sum "$tmp/before" | cut -d ' ' -f 1)" \
  "$(wc -c < "$tmp/artifact/backup.dump")" "$(($(date +%s) - start))"
docker exec "$container" pg_dump --version
docker exec "$container" pg_restore --version
 "$tmp/artifact/metadata.txt"
grep -Eq '^server_version=17\\.11
snapshot ginbar_restore "$tmp/after"
cmp "$tmp/before" "$tmp/after"

# Compare schema object inventories from PostgreSQL's native archive TOC.
# Textual pg_dump DDL is not byte-stable across restore: PostgreSQL may
# deparse a semantically identical CHECK expression with different parentheses.
# Strict pg_restore already verifies each object could be reconstructed.
schema_inventory() {
  local db="$1" out="$2"
  dump -Fc --schema-only --no-owner --no-privileges -d "$db" > "$tmp/schema-$db.dump"
  docker exec -i "$container" pg_restore --list < "$tmp/schema-$db.dump" |
    sed -nE '/^[[:digit:]]+; /{s/^[[:digit:]]+; [[:digit:]]+ [[:digit:]]+ / /;p;}' |
    LC_ALL=C sort > "$out"
}
schema_inventory ginbar_source "$tmp/schema-before"
schema_inventory ginbar_restore "$tmp/schema-after"
test -s "$tmp/schema-before"
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

printf 'v2-pg-recovery-test: PASS tables=%s hash=%s dump_bytes=%s duration_s=%s\n' \
  "$(wc -l < "$tmp/before")" "$(sha256sum "$tmp/before" | cut -d ' ' -f 1)" \
  "$(wc -c < "$tmp/backup.dump")" "$(($(date +%s) - start))"
docker exec "$container" pg_dump --version
docker exec "$container" pg_restore --version
 "$tmp/artifact/metadata.txt"
grep -q '^pg_dump_version=pg_dump (PostgreSQL) 17.11' "$tmp/artifact/metadata.txt"
grep -q '^pg_restore_version=pg_restore (PostgreSQL) 17.11' "$tmp/artifact/metadata.txt"
! grep -Fq 'disposable_only' "$tmp/backup.log"
! grep -Fq 'disposable_only' "$tmp/artifact/metadata.txt"
docker exec -i "$container" pg_restore --list < "$tmp/artifact/backup.dump" > "$tmp/archive.toc"
test -s "$tmp/archive.toc"

# Source must remain byte-for-byte equivalent after the backup command.
snapshot ginbar_source "$tmp/source-after"
cmp "$tmp/before" "$tmp/source-after"

# Existing destination is never overwritten, including its metadata.
sha256sum "$tmp/artifact/backup.dump" "$tmp/artifact/metadata.txt" > "$tmp/collision-before"
if backup_client /work/artifact > "$tmp/collision.log" 2>&1; then
  echo 'v2-pg-recovery-test: collision unexpectedly succeeded' >&2; exit 1
fi
sha256sum "$tmp/artifact/backup.dump" "$tmp/artifact/metadata.txt" > "$tmp/collision-after"
cmp "$tmp/collision-before" "$tmp/collision-after"

# Failed dump must not publish an artifact or leave partial directories.
mkdir "$tmp/wrappers"
cat > "$tmp/wrappers/pg_dump" <<'WRAPPER'
#!/bin/sh
exit 37
WRAPPER
chmod 700 "$tmp/wrappers/pg_dump"
if docker run --rm --network "container:$container" \
    -v "$tmp:/work" -v "$ROOT/scripts/v2-pg-backup.sh:/backup.sh:ro" \
    -e PGHOST=127.0.0.1 -e PGUSER=ginbar_fixture \
    -e PGDATABASE=ginbar_source -e PGPASSWORD=disposable_only \
    -e PATH=/work/wrappers:/usr/local/bin:/usr/bin:/bin \
    "$image" bash /backup.sh /work/failed > "$tmp/failed.log" 2>&1; then
  echo 'v2-pg-recovery-test: injected failure unexpectedly succeeded' >&2; exit 1
fi
test ! -e "$tmp/failed"
! find "$tmp" -maxdepth 1 -name '.v2-pg-backup.partial.*' | grep -q .
! grep -Fq 'disposable_only' "$tmp/failed.log"
! grep -Fq 'disposable_only' "$tmp/collision.log"

# Truncation must fail strict restoration.
head -c 128 "$tmp/artifact/backup.dump" > "$tmp/truncated.dump"
pg -d postgres -c 'CREATE DATABASE ginbar_corrupt'
if docker exec -i "$container" pg_restore -U ginbar_fixture \
    -d ginbar_corrupt --exit-on-error --no-owner --no-privileges \
    < "$tmp/truncated.dump" > "$tmp/corrupt.log" 2>&1; then
  echo 'v2-pg-recovery-test: truncated restore succeeded' >&2; exit 1
fi

# Simulate complete loss of the source database, then cold restore from the
# independently published logical artifact into a new empty database.
pg -d postgres -c 'DROP DATABASE ginbar_source WITH (FORCE)'
pg -d postgres -c 'CREATE DATABASE ginbar_restore'
docker exec -i "$container" pg_restore -U ginbar_fixture -d ginbar_restore \
  --exit-on-error --no-owner --no-privileges < "$tmp/artifact/backup.dump"

snapshot ginbar_restore "$tmp/after"
cmp "$tmp/before" "$tmp/after"

# Compare schema object inventories from PostgreSQL's native archive TOC.
# Textual pg_dump DDL is not byte-stable across restore: PostgreSQL may
# deparse a semantically identical CHECK expression with different parentheses.
# Strict pg_restore already verifies each object could be reconstructed.
schema_inventory() {
  local db="$1" out="$2"
  dump -Fc --schema-only --no-owner --no-privileges -d "$db" > "$tmp/schema-$db.dump"
  docker exec -i "$container" pg_restore --list < "$tmp/schema-$db.dump" |
    sed -nE '/^[[:digit:]]+; /{s/^[[:digit:]]+; [[:digit:]]+ [[:digit:]]+ / /;p;}' |
    LC_ALL=C sort > "$out"
}
schema_inventory ginbar_source "$tmp/schema-before"
schema_inventory ginbar_restore "$tmp/schema-after"
test -s "$tmp/schema-before"
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

printf 'v2-pg-recovery-test: PASS tables=%s hash=%s dump_bytes=%s duration_s=%s\n' \
  "$(wc -l < "$tmp/before")" "$(sha256sum "$tmp/before" | cut -d ' ' -f 1)" \
  "$(wc -c < "$tmp/backup.dump")" "$(($(date +%s) - start))"
docker exec "$container" pg_dump --version
docker exec "$container" pg_restore --version
