# M2 backend validation / benchmark runbook

This gate runs on the remote target server. Backend/PostgreSQL measurements execute on the actual server and may be treated as target-server evidence.

The runner is intentionally execution-only: it archives the exact checked-out commit into `/tmp`, performs Go dependency resolution/tests/builds there, uses only a disposable PostgreSQL database, starts the API on loopback, and writes raw evidence under `/tmp`. It does not modify the checked-out repository.

## 1. Safety / checkout

Use an isolated worktree of `astra/m2-core-schema-api`. Do not switch or modify deployed `main`, legacy `master`, or a production checkout.

Do not edit, tune, commit, merge, or push anything during this run. If something fails, preserve the output and report it.

Verify before running:

```bash
git status --short --branch
git rev-parse HEAD
```

The benchmark runner accepts host Go 1.25+ or, when host Go is older, uses an existing Docker installation with `golang:1.25`. It does not install system packages. If host `psql` is absent, it uses `postgres:17-alpine` as a disposable client through Docker.

## 2. Dedicated disposable PostgreSQL 17

Do not use Wallium's PostgreSQL instance. Run a dedicated PostgreSQL 17 container bound only to loopback so the benchmark cannot touch production data and still executes on the target server hardware.

Choose an unused loopback port (55432 below) and start the disposable database:

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

Do not print `BENCH_PG_PASSWORD` or `DATABASE_URL` in the returned report.

The gate runner also verifies that the connected database name begins with `ginbar_m2_bench`; it refuses any other database.

The full run applies `001_core.sql`, seeds 1,000 users / 100,000 posts / 100,000 media rows / approximately three tags per post, analyzes the hot tables, and captures query plans before HTTP load testing.

## 3. Full shared-host run

With the server in its normal state, including Wallium backend/worker running, execute from the isolated Ginbar worktree root:

```bash
GINBAR_BENCH_LABEL=shared \
DATABASE_URL="$DATABASE_URL" \
bash src/backend/v2/bench/run_gate.sh
```

Default HTTP matrix:

- cases: first feed page, old cursor, tag+score, tag+excluded-tag+score, around-post reconstruction
- concurrency: 1 / 4 / 8 / 16 / 32
- measured requests: 2,000 per case/concurrency after warmup
- API PostgreSQL pool: 8 connections

The runner captures:

- exact Git SHA and working-tree status
- CPU/RAM/OS and PostgreSQL versions
- real `go mod tidy` and `go test ./...` in a temporary archive
- generated `go.sum` as evidence only (outside the repository)
- schema/seed timings
- full `EXPLAIN (ANALYZE, BUFFERS)` output for five query shapes
- relation/index sizes and index scan counters
- smoke endpoint responses
- p50/p95/p99/max, requests/sec, status counts, and errors for each HTTP case
- API RSS/CPU, system load, and PostgreSQL connection counts sampled during the matrix
- PostgreSQL database counters after load

At completion it prints the results directory, normally `/tmp/ginbar-m2-gate-...-shared`.

## 4. Optional quiet-host comparison

If and only if the Wallium application workload can be stopped safely and reversibly:

1. record the exact Wallium backend and worker container/service names and their running state;
2. stop only Wallium backend and media worker workload; leave Wallium PostgreSQL/Redis running unless there is a specific reason to stop them;
3. do not change persistent Compose/system configuration;
4. verify the dedicated benchmark PostgreSQL container is still healthy;
5. rerun only validation/build + HTTP load against the existing seeded benchmark database:

```bash
GINBAR_BENCH_LABEL=quiet \
GINBAR_BENCH_SKIP_DB_PREP=1 \
DATABASE_URL="$DATABASE_URL" \
bash src/backend/v2/bench/run_gate.sh
```

6. restore the exact Wallium backend/worker state and verify they are healthy before doing anything else.

If stopping Wallium is ambiguous or unsafe, do not stop it; report that the quiet comparison was skipped.

## 5. Conditional pool-size probe

Do not tune the implementation during the run.

Only if the 8-connection run shows a clear latency cliff at concurrency 16/32 while server/PostgreSQL CPU still has substantial headroom, collect one additional measurement with a 16-connection pool. This is measurement only, not a configuration decision:

```bash
GINBAR_BENCH_LABEL=quiet-pool16 \
GINBAR_BENCH_SKIP_DB_PREP=1 \
DB_MAX_CONNS=16 \
DATABASE_URL="$DATABASE_URL" \
bash src/backend/v2/bench/run_gate.sh
```

Do not test arbitrary pool sizes or change source code.

## 6. Query-plan evidence to extract

From the shared run's `explain.txt`, report for each shape:

- first feed page
- old cursor (`id < 50000`)
- required tag + score
- required + excluded tag
- around post 50000

For each capture:

- planning time and execution time
- top-level and important child plan nodes
- index names used
- actual rows / loops
- shared buffer hits / reads
- rows removed by filters
- any sequential scan over a large relation

Do not propose or create indexes during this execution task.

## 7. HTTP evidence to return

For each benchmark case and concurrency 1 / 4 / 8 / 16 / 32, return:

- p50 / p95 / p99 / max milliseconds
- requests/sec
- success/error counts

Also report:

- peak observed API CPU and RSS
- peak PostgreSQL total/active connections from `resource-samples.tsv`
- system load range during the run
- any 5xx/timeouts
- shared-host versus quiet-host delta if the quiet run was possible
- pool-16 delta only if the conditional probe was triggered

## 8. Cleanup

After all measurements and after Wallium has been restored, remove only the disposable benchmark PostgreSQL resources:

```bash
docker stop "$BENCH_PG_CONTAINER"
docker volume rm "$BENCH_PG_VOLUME"
unset DATABASE_URL BENCH_PG_PASSWORD BENCH_PG_CONTAINER BENCH_PG_VOLUME
```

Keep the `/tmp/ginbar-m2-gate-*` result directories long enough to extract/report the evidence. They contain no intended production secrets, but still inspect/redact before sharing if an unexpected error message includes connection information.

## 9. Review rule

Optimization order after evidence returns:

1. eliminate scans/work/round trips;
2. fix query/index/data layout;
3. only then consider cache/precompute/concurrency changes.

Do not introduce Redis for feed/search merely to mask an unmeasured SQL shape.

Do not modify `docs/v2/STATE.md` during the benchmark. Return the information to the primary engineering session; that session will decide changes and update durable state.
