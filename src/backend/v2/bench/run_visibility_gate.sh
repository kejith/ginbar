#!/usr/bin/env bash
set -euo pipefail

if [[ -z "${DATABASE_URL:-}" || -z "${GINBAR_BENCH_PG_CONTAINER:-}" ]]; then
  echo "DATABASE_URL and GINBAR_BENCH_PG_CONTAINER are required" >&2
  exit 2
fi

for command in git tar docker curl ps awk stat date grep seq ss cmp python3; do
  command -v "$command" >/dev/null 2>&1 || { echo "missing required command: $command" >&2; exit 2; }
done

docker inspect "$GINBAR_BENCH_PG_CONTAINER" >/dev/null 2>&1 || {
  echo "benchmark PostgreSQL container is unavailable" >&2
  exit 2
}

repo_root="$(git rev-parse --show-toplevel)"
commit="$(git -C "$repo_root" rev-parse HEAD)"
timestamp="$(date -u +%Y%m%dT%H%M%SZ)"
results_dir="${GINBAR_VIS_RESULTS_DIR:-/tmp/ginbar-m2-visibility-${timestamp}}"
work_dir="$(mktemp -d /tmp/ginbar-m2-vis-work.XXXXXX)"
go_cache_dir="$(mktemp -d /tmp/ginbar-m2-vis-gocache.XXXXXX)"
bin_dir="$work_dir/bin"
api_pid=""
sampler_pid=""

mkdir -p "$results_dir" "$bin_dir"

cleanup() {
  local status=$?
  if [[ -n "$sampler_pid" ]] && kill -0 "$sampler_pid" 2>/dev/null; then
    kill "$sampler_pid" 2>/dev/null || true
    wait "$sampler_pid" 2>/dev/null || true
  fi
  if [[ -n "$api_pid" ]] && kill -0 "$api_pid" 2>/dev/null; then
    kill "$api_pid" 2>/dev/null || true
    wait "$api_pid" 2>/dev/null || true
  fi
  rm -rf "$work_dir" "$go_cache_dir"
  echo "results: $results_dir"
  exit "$status"
}
trap cleanup EXIT INT TERM

current_db="$(docker exec "$GINBAR_BENCH_PG_CONTAINER" psql -U postgres -d ginbar_m2_bench -Atqc 'select current_database()')"
[[ "$current_db" == "ginbar_m2_bench" ]] || {
  echo "refusing database '$current_db'" >&2
  exit 2
}

git -C "$repo_root" archive HEAD src/backend/v2 | tar -x -C "$work_dir"
module_dir="$work_dir/src/backend/v2"

{
  echo "timestamp_utc=$timestamp"
  echo "commit=$commit"
  echo "database=$current_db"
  echo "postgres_server=$(docker exec "$GINBAR_BENCH_PG_CONTAINER" psql -U postgres -d ginbar_m2_bench -Atqc 'show server_version')"
  echo "go_version=$(docker run --rm golang:1.25 go version)"
  echo "requests_per_case=${GINBAR_BENCH_REQUESTS:-2000}"
  echo "concurrencies=${GINBAR_BENCH_CONCURRENCIES:-1 4 8 16 32}"
} > "$results_dir/environment.txt"

git -C "$repo_root" status --short --branch > "$results_dir/git-status.txt"

run_go() {
  docker run --rm --network host \
    --user "$(id -u):$(id -g)" \
    -e HOME=/tmp -e GOWORK=off \
    -e GOMODCACHE=/go-cache/mod -e GOCACHE=/go-cache/build \
    -v "$module_dir:/src" -v "$go_cache_dir:/go-cache" \
    -w /src golang:1.25 go "$@"
}

build_go() {
  local output="$1"
  shift
  docker run --rm --network host \
    --user "$(id -u):$(id -g)" \
    -e HOME=/tmp -e GOWORK=off \
    -e GOMODCACHE=/go-cache/mod -e GOCACHE=/go-cache/build \
    -v "$module_dir:/src" -v "$go_cache_dir:/go-cache" -v "$bin_dir:/out" \
    -w /src golang:1.25 go build -trimpath -o "/out/$output" "$@"
}

run_go mod tidy > "$results_dir/go-mod-tidy.log" 2>&1
run_go test ./... > "$results_dir/go-test.log" 2>&1
cmp -s "$module_dir/go.mod" "$repo_root/src/backend/v2/go.mod"
cmp -s "$module_dir/go.sum" "$repo_root/src/backend/v2/go.sum"
echo "module_files_match=1" >> "$results_dir/environment.txt"

build_go ginbar-api ./cmd/api > "$results_dir/go-build-api.log" 2>&1
build_go httpbench ./bench/httpbench.go > "$results_dir/go-build-httpbench.log" 2>&1
stat -c 'api_binary_bytes=%s' "$bin_dir/ginbar-api" >> "$results_dir/environment.txt"
stat -c 'httpbench_binary_bytes=%s' "$bin_dir/httpbench" >> "$results_dir/environment.txt"

schema_start="$(date +%s%N)"
docker exec -i "$GINBAR_BENCH_PG_CONTAINER" psql -U postgres -d ginbar_m2_bench -v ON_ERROR_STOP=1 \
  > "$results_dir/schema.log" 2> "$results_dir/schema.err" \
  < "$module_dir/internal/schema/migrations/001_core.sql"
schema_end="$(date +%s%N)"
echo "elapsed_ms=$(((schema_end - schema_start) / 1000000))" > "$results_dir/schema.time"

seed_start="$(date +%s%N)"
docker exec -i "$GINBAR_BENCH_PG_CONTAINER" psql -U postgres -d ginbar_m2_bench -v ON_ERROR_STOP=1 \
  > "$results_dir/seed.log" 2> "$results_dir/seed.err" \
  < "$module_dir/bench/seed.sql"
seed_end="$(date +%s%N)"
echo "elapsed_ms=$(((seed_end - seed_start) / 1000000))" > "$results_dir/seed.time"

docker exec -i "$GINBAR_BENCH_PG_CONTAINER" psql -U postgres -d ginbar_m2_bench -v ON_ERROR_STOP=1 \
  > "$results_dir/explain-visibility.txt" 2> "$results_dir/explain-visibility.err" \
  < "$module_dir/bench/explain_visibility.sql"

port_in_use() {
  local port="$1"
  ss -H -ltn | awk '{print $4}' | grep -Eq ":${port}$"
}

bench_port=""
for candidate in $(seq 18100 18199); do
  if ! port_in_use "$candidate"; then
    bench_port="$candidate"
    break
  fi
done
[[ -n "$bench_port" ]] || { echo "no free API port in 18100-18199" >&2; exit 2; }

echo "api_port=$bench_port" >> "$results_dir/environment.txt"
base_url="http://127.0.0.1:${bench_port}"
DATABASE_URL="$DATABASE_URL" DB_MAX_CONNS=8 LISTEN_ADDR="127.0.0.1:${bench_port}" \
  "$bin_dir/ginbar-api" > "$results_dir/api.log" 2>&1 &
api_pid=$!

for _ in {1..60}; do
  kill -0 "$api_pid" 2>/dev/null || { echo "API exited before health" >&2; exit 1; }
  curl -fsS "$base_url/healthz" >/dev/null 2>&1 && break
  sleep 0.25
done
curl -fsS "$base_url/healthz" > "$results_dir/smoke-health.json"
curl -fsS "$base_url/api/v2/posts/49999/around?radius=30" > "$results_dir/smoke-visible.json"
curl -fsS "$base_url/api/v2/posts/49999/around?radius=30&q=tag-42" > "$results_dir/smoke-search-context.json"
denied_status="$(curl -sS -o "$results_dir/smoke-hidden.json" -w '%{http_code}' "$base_url/api/v2/posts/50000/around?radius=30")"
echo "hidden_status=$denied_status" >> "$results_dir/environment.txt"
[[ "$denied_status" == "404" ]] || { echo "expected NSFW post 50000 to be hidden with HTTP 404, got $denied_status" >&2; exit 1; }

python3 - "$results_dir" <<'PY'
import json, pathlib, sys
root = pathlib.Path(sys.argv[1])
visible = json.loads((root / "smoke-visible.json").read_text())
search = json.loads((root / "smoke-search-context.json").read_text())
hidden = json.loads((root / "smoke-hidden.json").read_text())
for name, payload in (("visible", visible), ("search", search)):
    assert payload["selectedId"] == 49999, (name, payload.get("selectedId"))
    selected = [p for p in payload["posts"] if p["id"] == 49999]
    assert len(selected) == 1, (name, len(selected))
    assert selected[0]["filter"] == 0, (name, selected[0]["filter"])
assert len(visible["posts"]) == 61, len(visible["posts"])
assert all(p["filter"] == 0 for p in visible["posts"])
assert hidden["error"]["code"] == "post_not_found", hidden
PY

echo "smoke_assertions=pass" >> "$results_dir/environment.txt"

pg_counts() {
  docker exec "$GINBAR_BENCH_PG_CONTAINER" psql -U postgres -d ginbar_m2_bench -AtF' ' -c \
    "select count(*) filter (where pid <> pg_backend_pid()), count(*) filter (where pid <> pg_backend_pid() and state = 'active') from pg_stat_activity where datname = current_database();"
}

sample_loop() {
  printf 'timestamp\tapi_cpu_pct\tapi_rss_kb\tpg_total_connections\tpg_active_connections\tload1\n'
  while kill -0 "$api_pid" 2>/dev/null; do
    read -r cpu rss < <(ps -p "$api_pid" -o %cpu=,rss= | awk 'NR==1 {print $1, $2}')
    read -r total active < <(pg_counts)
    read -r load1 _ < /proc/loadavg
    printf '%s\t%s\t%s\t%s\t%s\t%s\n' "$(date -u +%FT%T.%3NZ)" "${cpu:-0}" "${rss:-0}" "${total:-0}" "${active:-0}" "$load1"
    sleep 1
  done
}
sample_loop > "$results_dir/resource-samples.tsv" &
sampler_pid=$!

requests="${GINBAR_BENCH_REQUESTS:-2000}"
concurrencies="${GINBAR_BENCH_CONCURRENCIES:-1 4 8 16 32}"
: > "$results_dir/http-status.tsv"
for concurrency in $concurrencies; do
  output="$results_dir/http-around-visible-c${concurrency}.json"
  set +e
  "$bin_dir/httpbench" \
    -url "$base_url/api/v2/posts/49999/around?radius=30" \
    -concurrency "$concurrency" -requests "$requests" -warmup 20 -timeout 5s \
    > "$output" 2> "$output.err"
  status=$?
  set -e
  printf 'around-visible\t%s\t%s\n' "$concurrency" "$status" >> "$results_dir/http-status.tsv"
  (( status == 0 )) || { echo "HTTP benchmark failed at concurrency $concurrency" >&2; exit 1; }
  sleep 1
done

echo "complete=1" >> "$results_dir/environment.txt"
