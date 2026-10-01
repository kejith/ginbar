#!/usr/bin/env bash
set -euo pipefail

if [[ -z "${DATABASE_URL:-}" ]]; then
  echo "DATABASE_URL must point to a disposable ginbar_m2_bench* database" >&2
  exit 2
fi

for command in git tar curl ps awk sed stat date; do
  command -v "$command" >/dev/null 2>&1 || { echo "missing required command: $command" >&2; exit 2; }
done

repo_root="$(git rev-parse --show-toplevel)"
branch="$(git -C "$repo_root" branch --show-current || true)"
commit="$(git -C "$repo_root" rev-parse HEAD)"
run_label="${GINBAR_BENCH_LABEL:-shared}"
timestamp="$(date -u +%Y%m%dT%H%M%SZ)"
results_dir="${GINBAR_BENCH_RESULTS_DIR:-/tmp/ginbar-m2-gate-${timestamp}-${run_label}}"
work_dir="$(mktemp -d /tmp/ginbar-m2-work.XXXXXX)"
go_cache_dir="$(mktemp -d /tmp/ginbar-m2-gocache.XXXXXX)"
bin_dir="$work_dir/bin"
api_pid=""
sampler_pid=""

mkdir -p "$results_dir" "$bin_dir"

psql_mode=""
if command -v psql >/dev/null 2>&1; then
  psql_mode="host"
elif command -v docker >/dev/null 2>&1; then
  psql_mode="docker"
else
  echo "PostgreSQL client is unavailable and Docker is not installed" >&2
  exit 2
fi

psql_exec() {
  if [[ "$psql_mode" == "host" ]]; then
    psql "$@"
  else
    docker run --rm -i --network host postgres:17-alpine psql "$@"
  fi
}

cleanup() {
  local status=$?
  if [[ -n "$sampler_pid" ]] && kill -0 "$sampler_pid" 2>/dev/null; then
    kill "$sampler_pid" 2>/dev/null || true
    wait "$sampler_pid" 2>/dev/null || true
  fi
  if [[ -n "$api_pid" ]] && kill -0 "$api_pid" 2>/dev/null; then
    kill "$api_pid" 2>/dev/null || true
    for _ in {1..20}; do
      kill -0 "$api_pid" 2>/dev/null || break
      sleep 0.25
    done
    kill -9 "$api_pid" 2>/dev/null || true
    wait "$api_pid" 2>/dev/null || true
  fi
  rm -rf "$work_dir" "$go_cache_dir"
  echo "results: $results_dir"
  exit "$status"
}
trap cleanup EXIT INT TERM

{
  echo "timestamp_utc=$timestamp"
  echo "run_label=$run_label"
  echo "branch=$branch"
  echo "commit=$commit"
  echo "repository=$repo_root"
  echo "kernel=$(uname -srmo)"
  echo "uptime=$(uptime)"
  echo "psql_mode=$psql_mode"
  echo "postgres_client=$(psql_exec --version)"
  echo "database=$(psql_exec "$DATABASE_URL" -Atqc 'select current_database()')"
  echo "postgres_server=$(psql_exec "$DATABASE_URL" -Atqc 'show server_version')"
  echo "db_max_conns=${DB_MAX_CONNS:-8}"
  echo "requests_per_case=${GINBAR_BENCH_REQUESTS:-2000}"
  echo "concurrencies=${GINBAR_BENCH_CONCURRENCIES:-1 4 8 16 32}"
} > "$results_dir/environment.txt"

uname -a >> "$results_dir/environment.txt"
{ lscpu || true; } > "$results_dir/lscpu.txt" 2>&1
{ free -h || true; } > "$results_dir/memory.txt" 2>&1
git -C "$repo_root" status --short --branch > "$results_dir/git-status.txt"

current_db="$(psql_exec "$DATABASE_URL" -Atqc 'select current_database()')"
if [[ ! "$current_db" =~ ^ginbar_m2_bench([_].*)?$ ]]; then
  echo "refusing to use database '$current_db'; expected name ginbar_m2_bench or ginbar_m2_bench_*" >&2
  exit 2
fi

git -C "$repo_root" archive HEAD src/backend/v2 | tar -x -C "$work_dir"
module_dir="$work_dir/src/backend/v2"

for required_file in \
  "$module_dir/internal/schema/migrations/001_core.sql" \
  "$module_dir/bench/seed.sql" \
  "$module_dir/bench/explain.sql"; do
  [[ -f "$required_file" ]] || { echo "required benchmark file missing after archive: $required_file" >&2; exit 1; }
done

psql_stdin_probe="$(printf 'select 1;\n' | psql_exec "$DATABASE_URL" -Atq)"
[[ "$psql_stdin_probe" == "1" ]] || { echo "psql stdin probe failed" >&2; exit 1; }

version_at_least_125() {
  local raw major minor
  raw="$1"
  raw="${raw#go}"
  major="${raw%%.*}"
  raw="${raw#*.}"
  minor="${raw%%.*}"
  [[ "$major" -gt 1 || ( "$major" -eq 1 && "$minor" -ge 25 ) ]]
}

go_mode=""
if command -v go >/dev/null 2>&1 && version_at_least_125 "$(go env GOVERSION)"; then
  go_mode="host"
  echo "go_mode=host" >> "$results_dir/environment.txt"
  echo "go_version=$(go version)" >> "$results_dir/environment.txt"
elif command -v docker >/dev/null 2>&1; then
  go_mode="docker"
  echo "go_mode=docker:golang:1.25" >> "$results_dir/environment.txt"
else
  echo "Go 1.25+ is unavailable and Docker is not installed" >&2
  exit 2
fi

go_exec() {
  if [[ "$go_mode" == "host" ]]; then
    (cd "$module_dir" && GOWORK=off go "$@")
  else
    docker run --rm --network host \
      --user "$(id -u):$(id -g)" \
      -e HOME=/tmp \
      -e GOWORK=off \
      -e GOMODCACHE=/go-cache/mod \
      -e GOCACHE=/go-cache/build \
      -v "$module_dir:/src" \
      -v "$go_cache_dir:/go-cache" \
      -w /src \
      golang:1.25 go "$@"
  fi
}

go_build() {
  local output_name="$1"
  shift
  if [[ "$go_mode" == "host" ]]; then
    (cd "$module_dir" && GOWORK=off go build -trimpath -o "$bin_dir/$output_name" "$@")
  else
    docker run --rm --network host \
      --user "$(id -u):$(id -g)" \
      -e HOME=/tmp \
      -e GOWORK=off \
      -e GOMODCACHE=/go-cache/mod \
      -e GOCACHE=/go-cache/build \
      -v "$module_dir:/src" \
      -v "$go_cache_dir:/go-cache" \
      -v "$bin_dir:/out" \
      -w /src \
      golang:1.25 go build -trimpath -o "/out/$output_name" "$@"
  fi
}

set +e
go_exec mod tidy > "$results_dir/go-mod-tidy.log" 2>&1
tidy_status=$?
go_exec test ./... > "$results_dir/go-test.log" 2>&1
test_status=$?
set -e
printf 'go_mod_tidy=%d\ngo_test=%d\n' "$tidy_status" "$test_status" > "$results_dir/validation-status.txt"
if [[ -f "$module_dir/go.sum" ]]; then
  cp "$module_dir/go.sum" "$results_dir/generated-go.sum"
fi
cp "$module_dir/go.mod" "$results_dir/go.mod.after"
if (( tidy_status != 0 || test_status != 0 )); then
  echo "Go dependency/test validation failed; see $results_dir" >&2
  exit 1
fi

go_build ginbar-api ./cmd/api > "$results_dir/go-build-api.log" 2>&1
go_build httpbench ./bench/httpbench.go > "$results_dir/go-build-httpbench.log" 2>&1
[[ -x "$bin_dir/ginbar-api" ]] || { echo "API binary missing after build" >&2; exit 1; }
[[ -x "$bin_dir/httpbench" ]] || { echo "HTTP benchmark binary missing after build" >&2; exit 1; }
stat -c 'api_binary_bytes=%s' "$bin_dir/ginbar-api" >> "$results_dir/environment.txt"
stat -c 'httpbench_binary_bytes=%s' "$bin_dir/httpbench" >> "$results_dir/environment.txt"

if [[ "${GINBAR_BENCH_SKIP_DB_PREP:-0}" != "1" ]]; then
  schema_start_ns="$(date +%s%N)"
  psql_exec "$DATABASE_URL" -v ON_ERROR_STOP=1 \
    > "$results_dir/schema.log" 2> "$results_dir/schema.err" \
    < "$module_dir/internal/schema/migrations/001_core.sql"
  schema_end_ns="$(date +%s%N)"
  echo "elapsed_ms=$(((schema_end_ns - schema_start_ns) / 1000000))" > "$results_dir/schema.time"

  seed_start_ns="$(date +%s%N)"
  psql_exec "$DATABASE_URL" -v ON_ERROR_STOP=1 \
    > "$results_dir/seed.log" 2> "$results_dir/seed.err" \
    < "$module_dir/bench/seed.sql"
  seed_end_ns="$(date +%s%N)"
  echo "elapsed_ms=$(((seed_end_ns - seed_start_ns) / 1000000))" > "$results_dir/seed.time"

  psql_exec "$DATABASE_URL" -v ON_ERROR_STOP=1 \
    > "$results_dir/explain.txt" 2> "$results_dir/explain.err" \
    < "$module_dir/bench/explain.sql"
fi

psql_exec "$DATABASE_URL" -v ON_ERROR_STOP=1 -P pager=off -c \
  "select relname, pg_size_pretty(pg_total_relation_size(relid)) as total_size, n_live_tup from pg_stat_user_tables order by pg_total_relation_size(relid) desc;" \
  > "$results_dir/table-sizes.txt"
psql_exec "$DATABASE_URL" -v ON_ERROR_STOP=1 -P pager=off -c \
  "select relname, indexrelname, idx_scan, pg_size_pretty(pg_relation_size(indexrelid)) as index_size from pg_stat_user_indexes order by relname, indexrelname;" \
  > "$results_dir/index-stats-before.txt"

listen_addr="127.0.0.1:${GINBAR_BENCH_PORT:-18080}"
base_url="http://$listen_addr"
DATABASE_URL="$DATABASE_URL" DB_MAX_CONNS="${DB_MAX_CONNS:-8}" LISTEN_ADDR="$listen_addr" \
  "$bin_dir/ginbar-api" > "$results_dir/api.log" 2>&1 &
api_pid=$!
echo "api_pid=$api_pid" >> "$results_dir/environment.txt"

for _ in {1..60}; do
  if curl -fsS "$base_url/healthz" >/dev/null 2>&1; then
    break
  fi
  if ! kill -0 "$api_pid" 2>/dev/null; then
    echo "API exited before becoming healthy" >&2
    exit 1
  fi
  sleep 0.25
done
curl -fsS "$base_url/healthz" > "$results_dir/smoke-health.json"
curl -fsS "$base_url/api/v2/feed?limit=60" > "$results_dir/smoke-feed.json"
curl -fsS "$base_url/api/v2/feed?before=50000&limit=60&q=tag-42%20score:%3E%3D100" > "$results_dir/smoke-search.json"
curl -fsS "$base_url/api/v2/posts/50000/around?radius=30" > "$results_dir/smoke-around.json"

sample_loop() {
  printf 'timestamp\tapi_cpu_pct\tapi_rss_kb\tpg_total_connections\tpg_active_connections\tload1\tload5\tload15\n'
  while kill -0 "$api_pid" 2>/dev/null; do
    read -r api_cpu api_rss < <(ps -p "$api_pid" -o %cpu=,rss= | awk 'NR==1 {print $1, $2}')
    read -r pg_total pg_active < <(psql_exec "$DATABASE_URL" -AtF' ' -c \
      "select count(*), count(*) filter (where state = 'active') from pg_stat_activity where datname = current_database();")
    read -r load1 load5 load15 _ < /proc/loadavg
    printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' \
      "$(date -u +%FT%T.%3NZ)" "${api_cpu:-0}" "${api_rss:-0}" \
      "${pg_total:-0}" "${pg_active:-0}" "$load1" "$load5" "$load15"
    sleep 1
  done
}
sample_loop > "$results_dir/resource-samples.tsv" &
sampler_pid=$!

requests="${GINBAR_BENCH_REQUESTS:-2000}"
concurrencies="${GINBAR_BENCH_CONCURRENCIES:-1 4 8 16 32}"
cat > "$results_dir/cases.tsv" <<CASES
feed-first\t$base_url/api/v2/feed?limit=60
feed-cursor\t$base_url/api/v2/feed?before=50000&limit=60
search-tag-score\t$base_url/api/v2/feed?before=50000&limit=60&q=tag-42%20score:%3E%3D100
search-tag-exclude-score\t$base_url/api/v2/feed?before=50000&limit=60&q=tag-42%20-tag-77%20score:%3E%3D100
around-50000\t$base_url/api/v2/posts/50000/around?radius=30
CASES

: > "$results_dir/http-status.tsv"
while IFS=$'\t' read -r case_name url; do
  for concurrency in $concurrencies; do
    output="$results_dir/http-${case_name}-c${concurrency}.json"
    set +e
    "$bin_dir/httpbench" \
      -url "$url" \
      -concurrency "$concurrency" \
      -requests "$requests" \
      -warmup 20 \
      -timeout 5s > "$output" 2> "$output.err"
    bench_status=$?
    set -e
    printf '%s\t%s\t%s\n' "$case_name" "$concurrency" "$bench_status" >> "$results_dir/http-status.tsv"
    sleep 1
  done
done < "$results_dir/cases.tsv"

psql_exec "$DATABASE_URL" -v ON_ERROR_STOP=1 -P pager=off -c \
  "select relname, indexrelname, idx_scan from pg_stat_user_indexes order by relname, indexrelname;" \
  > "$results_dir/index-stats-after.txt"
psql_exec "$DATABASE_URL" -v ON_ERROR_STOP=1 -P pager=off -c \
  "select datname, numbackends, xact_commit, xact_rollback, blks_read, blks_hit, tup_returned, tup_fetched from pg_stat_database where datname = current_database();" \
  > "$results_dir/pg-database-stats.txt"

echo "complete=1" >> "$results_dir/environment.txt"
