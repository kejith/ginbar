#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

# Destination is a new directory containing backup.dump and metadata.txt.
# Libpq connection parameters are supplied through PGHOST, PGPORT, PGUSER,
# PGDATABASE and PGPASSFILE (or PGPASSWORD); never pass a password on argv.
fail() { printf 'v2-pg-backup: %s\n' "$1" >&2; exit 1; }
[[ $# == 1 && -n "$1" ]] || fail 'usage: v2-pg-backup.sh DESTINATION_DIRECTORY'
[[ -n "${PGDATABASE:-}" && -n "${PGUSER:-}" ]] || fail 'PGDATABASE and PGUSER required'
[[ "$PGDATABASE" != *://* && "$PGDATABASE" != *"="* ]] || fail 'PGDATABASE must be a plain database name'
for command in pg_dump pg_restore psql sha256sum mktemp mv stat date; do
  command -v "$command" >/dev/null 2>&1 || fail "missing required command: $command"
done

dest="$1"
[[ "$dest" != */ && "$dest" != '.' && "$dest" != '..' ]] || fail 'destination must be a new directory path without trailing slash'
parent="$(dirname -- "$dest")"
[[ -d "$parent" && -w "$parent" ]] || fail 'destination parent is not writable'
[[ ! -e "$dest" && ! -L "$dest" ]] || fail 'destination already exists'
tmp=''
cleanup() {
  status=$?
  trap - EXIT
  if [[ -n "$tmp" && -d "$tmp" ]]; then rm -rf -- "$tmp"; fi
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
trap 'exit 129' HUP

tmp="$(mktemp -d "$parent/.v2-pg-backup.partial.XXXXXXXX")" || fail 'cannot allocate partial directory'
[[ -d "$tmp" ]] || fail 'partial directory missing'
# Do not echo stderr from PostgreSQL tools: connection errors could include
# user-supplied connection information. Any failure is a generic diagnostic.
pg_dump -Fc --file="$tmp/backup.dump" >/dev/null 2>&1 || fail 'pg_dump failed'
[[ -s "$tmp/backup.dump" ]] || fail 'empty dump'
pg_restore --list "$tmp/backup.dump" >/dev/null 2>&1 || fail 'archive validation failed'
server_version="$(psql -X -At -v ON_ERROR_STOP=1 -c 'SHOW server_version' 2>/dev/null)" || fail 'server version query failed'
[[ "$server_version" =~ ^[0-9][0-9A-Za-z.+_-]*$ ]] || fail 'invalid server version'
dump_version="$(pg_dump --version 2>/dev/null)" || fail 'pg_dump version unavailable'
restore_version="$(pg_restore --version 2>/dev/null)" || fail 'pg_restore version unavailable'
[[ "$dump_version" =~ ^pg_dump\ \(PostgreSQL\)\  && "$restore_version" =~ ^pg_restore\ \(PostgreSQL\)\  ]] || fail 'invalid tool version'
checksum="$(sha256sum "$tmp/backup.dump")"
checksum="${checksum%% *}"
size="$(stat -c %s "$tmp/backup.dump")"
timestamp="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
printf 'format=postgresql-custom\nsha256=%s\nbytes=%s\ncreated_utc=%s\nserver_version=%s\npg_dump_version=%s\npg_restore_version=%s\n' \
  "$checksum" "$size" "$timestamp" "$server_version" "$dump_version" "$restore_version" > "$tmp/metadata.txt"
chmod 600 "$tmp/backup.dump" "$tmp/metadata.txt"
chmod 700 "$tmp"
[[ ! -e "$dest" && ! -L "$dest" ]] || fail 'destination already exists'
# GNU mv -n refuses an existing destination. The staging directory must disappear,
# otherwise no publication occurred (mv -n can return zero when skipping).
mv -T -n -- "$tmp" "$dest" || fail 'atomic publication failed'
[[ ! -e "$tmp" ]] || fail 'destination collision during publication'
tmp=''
printf 'v2-pg-backup: PASS\n'
