#!/usr/bin/env bash
set -Eeuo pipefail
umask 077
ROOT="$(git rev-parse --show-toplevel)"
cd "$ROOT"
fail() { printf 'v2-migration-compat-test: %s\n' "$*" >&2; exit 1; }
command -v python3 >/dev/null || fail 'Python 3 required'
command -v docker >/dev/null || fail 'Docker required'
docker info >/dev/null 2>&1 || fail 'Docker daemon unavailable'
tmp="$(mktemp -d "${TMPDIR:-/tmp}/ginbar-v2-migration-compat.XXXXXX")"
container="ginbar-v2-migration-compat-$$"
cleanup() {
  local status=$?
  trap - EXIT
  docker rm -f "$container" >/dev/null 2>&1 || true
  rm -rf -- "$tmp"
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

fixtures=scripts/testdata/v2-migration-compat
python3 scripts/v2-migration-guard.py --file "$fixtures/010_add_nullable.sql"
python3 scripts/v2-migration-guard.py --file "$fixtures/024_add_with_default.sql"
for name in 020_destructive 021_type_change 022_mandatory 023_drop_table; do
  if python3 scripts/v2-migration-guard.py --file "$fixtures/$name.sql" > "$tmp/$name.log" 2>&1; then
    fail "unsafe SQL accepted: $name"
  fi
  grep -q 'v2-migration-guard: REJECT' "$tmp/$name.log" || fail "missing rejection: $name"
done
printf 'v2-migration-guard-test: PASS positive=2 rejected=4\n'

image=postgres:17.11-alpine
if [[ -n "${GINBAR_MIGRATION_COMPAT_IMAGE:-}" ]]; then image="$GINBAR_MIGRATION_COMPAT_IMAGE"; fi
docker run --rm -d --name "$container" \
  -e POSTGRES_USER=ginbar_fixture -e POSTGRES_PASSWORD=disposable_only \
  -e POSTGRES_DB=postgres "$image" > /dev/null
for i in $(seq 1 60); do
  if docker exec "$container" pg_isready -U ginbar_fixture -d postgres >/dev/null 2>&1; then break; fi
  sleep 1
done
docker exec "$container" pg_isready -U ginbar_fixture -d postgres >/dev/null || fail 'PostgreSQL not ready'
pg() { docker exec -i "$container" psql -X -v ON_ERROR_STOP=1 -U ginbar_fixture "$@"; }
pg -d postgres -c 'CREATE DATABASE ginbar_base' > /dev/null
pg -d postgres -c 'CREATE DATABASE ginbar_additive' > /dev/null
count=0
while IFS= read -r migration; do
  for db in ginbar_base ginbar_additive; do pg -d "$db" < "$migration" > /dev/null; done
  count=$((count+1))
done < <(find src/backend/v2/internal/schema/migrations -maxdepth 1 -type f -name '*.sql' | LC_ALL=C sort)
((count > 0)) || fail 'missing checked-in migrations'

seed() {
  pg -d "$1" > /dev/null <<'SQL'
INSERT INTO users(id,username) OVERRIDING SYSTEM VALUE VALUES(42,'compat_fixture');
INSERT INTO user_credentials(user_id,kind,secret_hash) VALUES(42,0,'fixture_not_secret');
INSERT INTO posts(id,author_user_id,release_state,released_at)
  OVERRIDING SYSTEM VALUE VALUES(100,42,1,'2025-01-01T00:00:00Z');
INSERT INTO tags(id,name,normalized_name,created_by_user_id)
  OVERRIDING SYSTEM VALUE VALUES(55,'Compatible','compatible',42);
INSERT INTO post_tags(post_id,tag_id,added_by_user_id) VALUES(100,55,42);
INSERT INTO comments(id,post_id,user_id,body)
  OVERRIDING SYSTEM VALUE VALUES(77,100,42,'compat');
INSERT INTO post_votes(post_id,user_id,value) VALUES(100,42,1);
SELECT setval(pg_get_serial_sequence('users','id'),42,true);
SELECT setval(pg_get_serial_sequence('posts','id'),100,true);
SELECT setval(pg_get_serial_sequence('tags','id'),55,true);
SELECT setval(pg_get_serial_sequence('comments','id'),77,true);
SQL
}
seed ginbar_base
seed ginbar_additive

# This is a representative existing-column board query, not a real N-1 binary.
# Snapshot includes sequence state; fixed ID probe inserts avoid sequence drift.
snapshot() {
  pg -d "$1" -At <<'SQL'
SELECT 'board:' || md5(coalesce(string_agg(
  concat_ws('|',u.id::text,u.username,p.id::text,p.release_state::text,
    t.id::text,t.normalized_name,c.id::text,c.body,v.value::text),
  E'\n' ORDER BY u.id,p.id,t.id,c.id),''))
FROM users u JOIN posts p ON p.author_user_id=u.id
JOIN post_tags pt ON pt.post_id=p.id JOIN tags t ON t.id=pt.tag_id
JOIN comments c ON c.post_id=p.id AND c.user_id=u.id
JOIN post_votes v ON v.post_id=p.id AND v.user_id=u.id;
SELECT 'credentials:' || count(*) || ':' ||
 md5(coalesce(string_agg(user_id::text || ':' || kind::text || ':' || secret_hash,
  ',' ORDER BY user_id),''))
FROM user_credentials;
SELECT 'sequences:' || coalesce(string_agg(schemaname || '.' || sequencename ||
 '=' || coalesce(last_value::text,'NULL'),',' ORDER BY schemaname,sequencename),'')
FROM pg_sequences WHERE schemaname='public';
SQL
}
snapshot ginbar_base > "$tmp/base-before"
snapshot ginbar_additive > "$tmp/add-before"
cmp "$tmp/base-before" "$tmp/add-before" || fail 'baseline seeds unequal'

pg -d ginbar_additive < "$fixtures/010_add_nullable.sql" > /dev/null
test "$(pg -d ginbar_additive -Atc \
 "SELECT is_nullable FROM information_schema.columns WHERE table_schema='public' AND table_name='users' AND column_name='compatibility_probe_note'")" = YES ||
 fail 'synthetic nullable column missing'
test -z "$(pg -d ginbar_base -Atc \
 "SELECT column_name FROM information_schema.columns WHERE table_schema='public' AND table_name='users' AND column_name='compatibility_probe_note'")" ||
 fail 'baseline schema unexpectedly modified'
snapshot ginbar_additive > "$tmp/add-expanded"
cmp "$tmp/add-before" "$tmp/add-expanded" || fail 'expansion mutated existing data'

# Exercise real board-table FK relationships and explicit-column writes in a
# rolled-back transaction on each schema. No production/app executable is used.
write_probe() {
  pg -d "$1" > /dev/null <<'SQL'
BEGIN;
INSERT INTO users(id,username) OVERRIDING SYSTEM VALUE VALUES(99,'compat_write');
-- Explicit credential identity avoids nontransactional nextval() on rollback.
INSERT INTO user_credentials(id,user_id,kind,secret_hash)
  OVERRIDING SYSTEM VALUE VALUES(99,99,0,'fixture');
INSERT INTO posts(id,author_user_id,release_state,released_at)
  OVERRIDING SYSTEM VALUE VALUES(101,99,1,'2025-01-01T00:00:00Z');
INSERT INTO comments(id,post_id,user_id,body)
  OVERRIDING SYSTEM VALUE VALUES(88,101,99,'compat_write');
INSERT INTO post_votes(post_id,user_id,value) VALUES(101,99,1);
DO $$
BEGIN
 IF (SELECT count(*) FROM users u JOIN posts p ON p.author_user_id=u.id
  JOIN comments c ON c.post_id=p.id AND c.user_id=u.id
  JOIN post_votes v ON v.post_id=p.id AND v.user_id=u.id
  WHERE u.id=99 AND p.id=101 AND c.id=88 AND v.value=1) <> 1
 THEN RAISE EXCEPTION 'board FK read/write probe failed'; END IF;
END $$;
ROLLBACK;
SQL
}
write_probe ginbar_base
write_probe ginbar_additive
snapshot ginbar_base > "$tmp/base-after"
snapshot ginbar_additive > "$tmp/add-after"
cmp "$tmp/base-before" "$tmp/base-after" || fail 'baseline mutated'
cmp "$tmp/add-before" "$tmp/add-after" || fail 'additive mutated'
cmp "$tmp/base-after" "$tmp/add-after" || fail 'pre-addition projection changed'

# A destructive fixture is rejected before database execution.
if python3 scripts/v2-migration-guard.py --file "$fixtures/020_destructive.sql" \
  > "$tmp/destructive.log" 2>&1; then fail 'destructive DDL accepted'; fi
grep -q REJECT "$tmp/destructive.log" || fail 'missing destructive error'
snapshot ginbar_additive > "$tmp/rejected-after"
cmp "$tmp/add-before" "$tmp/rejected-after" || fail 'rejection mutated data'

printf 'v2-migration-compat-test: PASS migrations=%s matrix=3/3 rejected=4 source=unchanged\n' "$count"
docker exec "$container" psql --version
