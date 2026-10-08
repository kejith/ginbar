#!/usr/bin/env bash
# Disposable benchmark data and API: never connects to an existing PostgreSQL URL.
set -Eeuo pipefail

for cmd in docker git python3 mktemp; do
  command -v "$cmd" >/dev/null 2>&1 || { echo "v2-perf-regression: missing $cmd" >&2; exit 2; }
done

root="$(git rev-parse --show-toplevel)"
bench="$root/src/backend/v2/bench"
[[ -f "$bench/seed.sql" && -f "$bench/explain.sql" && -f "$bench/check_regression.py" ]] || {
  echo "v2-perf-regression: missing existing benchmark inputs" >&2; exit 2;
}
docker info >/dev/null 2>&1 || { echo "v2-perf-regression: Docker daemon unavailable" >&2; exit 2; }

requests="${GINBAR_PERF_REQUESTS:-2000}"
[[ "$requests" =~ ^[0-9]+$ ]] && (( requests >= 100 )) || {
  echo "v2-perf-regression: GINBAR_PERF_REQUESTS must be >= 100" >&2; exit 2;
}
tmp="$(mktemp -d "${TMPDIR:-/tmp}/ginbar-v2-perf.XXXXXX")"
results="${GINBAR_PERF_RESULTS_DIR:-$tmp/results}"
mkdir -p "$results"
pg="ginbar-v2-perf-pg-$$"
api="ginbar-v2-perf-api-$$"
network="ginbar-v2-perf-$$"
pg_running=0
api_running=0
network_created=0
cleanup() {
  local status=$?
  trap - EXIT
  if (( api_running )); then
    docker logs "$api" > "$results/api.log" 2>&1 || true
    docker rm -f "$api" >/dev/null 2>&1 || true
  fi
  if (( pg_running )); then
    docker logs "$pg" > "$results/postgres.log" 2>&1 || true
    docker rm -f "$pg" >/dev/null 2>&1 || true
  fi
  if (( network_created )); then
    docker network rm "$network" >/dev/null 2>&1 || true
  fi
  rm -rf "$tmp/bin"
  if [[ "$results" != "$tmp/results" ]]; then
    rm -rf "$tmp"
  fi
  printf 'v2-perf-regression: exit=%s sha=%s results=%s containers_removed=yes\n' \
    "$status" "$(git -C "$root" rev-parse HEAD)" "$results"
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

pg_image="postgres:17.11-alpine"
go_image="golang:1.25.0-bookworm"
cache="${GINBAR_CI_CACHE_DIR:-${HOME:-/tmp}/.cache/ginbar-v2-ci}"
mkdir -p "$cache/go-mod" "$cache/go-build" "$tmp/bin"
{
  echo "tested_sha=$(git -C "$root" rev-parse HEAD)"
  echo "tested_branch=$(git -C "$root" branch --show-current)"
  echo "started_utc=$(date -u +%FT%TZ)"
  echo "requests_per_case=$requests"
  echo "postgres_image=$pg_image"
  echo "go_image=$go_image"
  echo "budget_file=${GINBAR_PERF_BUDGET_FILE:-none}"
  echo "isolation=temporary Docker network + ephemeral PostgreSQL filesystem; no published ports"
} > "$results/environment.txt"
git -C "$root" status --short --branch > "$results/git-status-before.txt"

docker network create "$network" >/dev/null
network_created=1
docker run -d --rm --name "$pg" --network "$network" --network-alias pg \
  -e POSTGRES_USER=perf -e POSTGRES_PASSWORD=perf_ci_only -e POSTGRES_DB=ginbar_perf \
  "$pg_image" >/dev/null
pg_running=1

ready=0
for _ in $(seq 1 60); do
  if docker exec "$pg" pg_isready -U perf -d ginbar_perf >/dev/null 2>&1; then ready=1; break; fi
  sleep 1
done
(( ready )) || { echo "v2-perf-regression: disposable PostgreSQL not ready" >&2; exit 1; }
docker exec "$pg" psql -X -U perf -d ginbar_perf -Atqc 'SHOW server_version' \
  > "$results/postgres-version.txt"
docker exec -i "$pg" psql -X -v ON_ERROR_STOP=1 -U perf -d ginbar_perf \
  < "$root/src/backend/v2/internal/schema/migrations/001_core.sql" > "$results/schema.log" 2>&1
docker exec -i "$pg" psql -X -v ON_ERROR_STOP=1 -U perf -d ginbar_perf \
  < "$bench/seed.sql" > "$results/seed.log" 2>&1
docker exec "$pg" psql -X -U perf -d ginbar_perf -Atqc \
  'SELECT count(*) FROM posts' > "$results/post-count.txt"
[[ "$(cat "$results/post-count.txt")" == "100000" ]] || {
  echo "v2-perf-regression: seed did not create 100000 posts" >&2; exit 1;
}
docker exec -i "$pg" psql -X -v ON_ERROR_STOP=1 -P pager=off -U perf -d ginbar_perf \
  < "$bench/explain.sql" > "$results/explain.txt" 2> "$results/explain.err"

docker run --rm --network "$network" \
  -e GOTOOLCHAIN=local -e GOFLAGS=-mod=readonly \
  -e GOMODCACHE=/go-mod -e GOCACHE=/go-build \
  -v "$root/src/backend/v2:/src:ro" \
  -v "$cache/go-mod:/go-mod" -v "$cache/go-build:/go-build" \
  -v "$tmp/bin:/out" -w /src "$go_image" \
  sh -ceu 'go build -trimpath -o /out/ginbar-api ./cmd/api; go build -trimpath -o /out/httpbench ./bench/httpbench.go' \
  > "$results/build.log" 2>&1

docker run -d --rm --name "$api" --network "$network" --network-alias api \
  -v "$tmp/bin:/out:ro" \
  -e DATABASE_URL='postgres://perf:perf_ci_only@pg:5432/ginbar_perf?sslmode=disable' \
  -e DB_MAX_CONNS=8 -e LISTEN_ADDR=0.0.0.0:8080 \
  -e GINBAR_MEDIA_SOURCE_ROOT=/tmp/ginbar-perf-media \
  --entrypoint /out/ginbar-api "$go_image" >/dev/null
api_running=1

api_ready=0
for _ in $(seq 1 60); do
  if docker run --rm --network "$network" -v "$tmp/bin:/out:ro" \
    --entrypoint /out/httpbench "$go_image" \
    -url http://api:8080/readyz -requests 1 -concurrency 1 -warmup 0 -timeout 1s \
    > "$results/ready.json" 2> "$results/ready.err"; then
    api_ready=1; break
  fi
  if [[ "$(docker inspect -f '{{.State.Running}}' "$api" 2>/dev/null || true)" != "true" ]]; then
    echo "v2-perf-regression: API exited unexpectedly" >&2; exit 1
  fi
  sleep 1
done
(( api_ready )) || { echo "v2-perf-regression: API never became ready" >&2; exit 1; }

cases=(
  'feed-first|1|/api/v2/feed?limit=60'
  'feed-cursor|1|/api/v2/feed?before=50000&limit=60'
  'search-tag-score|1|/api/v2/feed?before=50000&limit=60&q=tag-42%20score:%3E%3D100'
  'around-50000|1|/api/v2/posts/50000/around?radius=30'
  'around-50000|8|/api/v2/posts/50000/around?radius=30'
)
for case in "${cases[@]}"; do
  IFS='|' read -r name concurrency path <<< "$case"
  output="$results/http-$name-c$concurrency.json"
  if ! docker run --rm --network "$network" -v "$tmp/bin:/out:ro" \
    --entrypoint /out/httpbench "$go_image" \
    -url "http://api:8080$path" -concurrency "$concurrency" \
    -requests "$requests" -warmup 20 -timeout 5s \
    > "$output" 2> "$output.err"; then
    echo "v2-perf-regression: failed HTTP case $name c$concurrency; see $output.err" >&2
    exit 1
  fi
done

check_args=("$results" --requests "$requests")
if [[ -n "${GINBAR_PERF_BUDGET_FILE:-}" ]]; then
  check_args+=(--budget-file "$GINBAR_PERF_BUDGET_FILE")
fi
python3 "$bench/check_regression.py" "${check_args[@]}" | tee "$results/validation.txt"
printf '\n== raw PostgreSQL EXPLAIN (ANALYZE, BUFFERS) ==\n'
cat "$results/explain.txt"
git -C "$root" status --short --branch > "$results/git-status-after.txt"
