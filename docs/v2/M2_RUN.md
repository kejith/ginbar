# M2 backend validation / benchmark runbook

This gate runs on the remote target server. Backend/PostgreSQL measurements execute on the actual server and may be treated as target-server evidence.

The runner is execution-only: it archives the exact checked-out commit into `/tmp`, performs Go dependency validation/tests/builds there, uses only a disposable PostgreSQL database, starts the API on loopback, and writes raw evidence under `/tmp`. It must not modify the checked-out repository.

## Previous execution evidence

The first server attempt tested `0b1727ffa1638d43b8800170d005b119f591cc67` and established:

- Docker Go 1.25.14 + real pgx dependency resolution succeeded;
- `go mod tidy` succeeded;
- `go test ./...` succeeded for all packages;
- the resulting 26-line `go.sum` and tidied indirect requirements are now committed on the M2 branch;
- the run stopped before schema application because containerized `go build -o <host-temp-path>` wrote binaries only into the ephemeral Go container.

The runner now builds through an explicit `/out` bind mount and verifies both binaries exist before touching the database. This was a harness failure, not backend performance evidence.

## 1. Safety / checkout

Use an isolated detached worktree of the current `origin/astra/m2-core-schema-api`. Do not switch or modify deployed `main`, legacy `master`, `v2`, or a production checkout.

Do not edit, tune, commit, merge, or push anything during this run. If something fails, preserve the output and report it.

Verify before running:

```bash
git fetch origin
git rev-parse origin/astra/m2-core-schema-api
git status --short --branch
git rev-parse HEAD
```

The detached worktree must be at the exact commit requested by the primary engineering session.

The runner accepts host Go 1.25+ or uses existing Docker with `golang:1.25`. If host `psql` is absent, it uses `postgres:17-alpine` as a disposable client. It installs no system packages.

## 2. Dedicated disposable PostgreSQL 17

Do not use Wallium PostgreSQL or production data.

Choose an unused loopback port (55432 below) and start a disposable database:

```bash
export BENCH_PG_CONTAINER="ginbar-m2-bench-pg-$(date +%s)"
export BENCH_PG_VOLUME="${BENCH_PG_CONTAINER}-data"
export BENCH_PG_PASSWORD="ginbar-bench-${RANDOM}-${RANDOM}-$(date +%s)"

docker volume create "$BENCH_PG_VOLUME"
docker run -d --rm \
  --name "$BENCH_PG_CONTAINER" \
  -e POSTGRES_PASSWORD="$BENCH_PG_PASSWORD" \
  -e POSTGRES_DB=ginbar_m2_bench \
  -p 127.0.0.1:55432:5432 \
  -v "$BENCH_PG_VOLUME:/var/lib/postgresql/data" \
  postgres:17-alpine

until docker exec "$BENCH_PG_CONTAINER" pg_isready -U postgres -d ginbar_m2_bench >/dev/null 2>&1; do
  sleep 0.5
done

export DATABASE_URL="postgres://postgres:${BENCH_PG_PASSWORD}@127.0.0.1:55432/ginbar_m2_bench?sslmode=disable"
```

Do not print credentials in the returned report.

The runner refuses any database whose name does not begin with `ginbar_m2_bench`.

The full run applies `001_core.sql`, seeds 1,000 users / 100,000 posts / 100,000 media rows / approximately three tags per post, analyzes hot tables, and captures query plans before HTTP load testing.

## 3. Full shared-host run

Keep Wallium in its normal running state.

```bash
GINBAR_BENCH_LABEL=shared \
DATABASE_URL="$DATABASE_URL" \
bash src/backend/v2/bench/run_gate.sh
```

Default HTTP matrix:

- cases: first feed page, old cursor, tag+score, tag+excluded-tag+score, around-post;
- concurrency: 1 / 4 / 8 / 16 / 32;
- 2,000 measured requests per case/concurrency after warmup;
- API PostgreSQL pool: 8 connections.

The runner captures exact Git SHA/status, environment, real `go mod tidy`, `go test ./...`, module files after tidy, binary sizes, schema/seed timings, five `EXPLAIN (ANALYZE, BUFFERS)` plans, table/index sizes, smoke responses, HTTP latency/throughput/errors, API CPU/RSS, system load, PostgreSQL connection counts, and database counters.

After the run, compare `go.mod.after` and `generated-go.sum` with the committed module files. They should now be identical. Report any difference; do not commit it.

## 4. Optional quiet-host comparison

Only if Wallium backend/API and media worker can be stopped safely without persistent configuration changes:

1. record their exact running state;
2. stop only those application workloads;
3. leave the dedicated benchmark PostgreSQL running;
4. rerun against the existing seeded DB:

```bash
GINBAR_BENCH_LABEL=quiet \
GINBAR_BENCH_SKIP_DB_PREP=1 \
DATABASE_URL="$DATABASE_URL" \
bash src/backend/v2/bench/run_gate.sh
```

5. restore Wallium exactly as it was and verify health immediately.

If stopping Wallium is ambiguous or unsafe, skip this comparison.

## 5. Conditional pool-size probe

Do not tune during the run.

Only if the 8-connection data shows a clear latency cliff at concurrency 16/32, PostgreSQL active connections reach the pool limit, and CPU still has substantial headroom, collect one additional 16-connection measurement:

```bash
GINBAR_BENCH_LABEL=quiet-pool16 \
GINBAR_BENCH_SKIP_DB_PREP=1 \
DB_MAX_CONNS=16 \
DATABASE_URL="$DATABASE_URL" \
bash src/backend/v2/bench/run_gate.sh
```

This is measurement only. Do not change defaults or source.

## 6. Query-plan evidence

For each shape in `explain.txt` report planning/execution time, important nodes/indexes, actual rows/loops, shared buffer hits/reads, rows removed by filters, and any large sequential scan:

- first feed page;
- old cursor (`id < 50000`);
- required tag + score;
- required + excluded tag;
- around post 50000.

Return complete plan text for the two slowest shapes. Do not create indexes or rewrite queries.

## 7. HTTP/resource evidence

For every case at concurrency 1 / 4 / 8 / 16 / 32 return:

- p50 / p95 / p99 / max milliseconds;
- requests/sec;
- successes/errors/status failures.

Also report peak API CPU/RSS, peak PostgreSQL total/active connections, system load range, any 5xx/timeouts, shared-vs-quiet deltas if available, and pool-16 delta only if triggered.

## 8. Cleanup

After measurements and after Wallium is restored:

```bash
docker stop "$BENCH_PG_CONTAINER"
docker volume rm "$BENCH_PG_VOLUME"
unset DATABASE_URL BENCH_PG_PASSWORD BENCH_PG_CONTAINER BENCH_PG_VOLUME
```

Keep `/tmp/ginbar-m2-gate-*` result directories for follow-up. Verify production checkouts were not modified and the benchmark worktree remains clean.

Do not modify `docs/v2/STATE.md`. Return evidence to the primary engineering session; that session owns architecture, fixes, commits, and durable state.
