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

The benchmark runner accepts host Go 1.25+ or, when host Go is older, uses an existing Docker installation with `golang:1.25`. It does not install system packages.

## 2. Disposable benchmark database

Create a fresh PostgreSQL database whose name is either:

```text
ginbar_m2_bench
ginbar_m2_bench_<suffix>
```

Never point the runner at production or Wallium data. The runner refuses any other database name.

Set `DATABASE_URL` for that disposable database without printing credentials into chat/log summaries.

The full run applies `001_core.sql`, seeds 1,000 users / 100,000 posts / 100,000 media rows / approximately three tags per post, analyzes the hot tables, and captures query plans before HTTP load testing.

## 3. Full shared-host run

With the server in its normal state (including Wallium running), execute from the isolated Ginbar worktree root:

```bash
export DATABASE_URL='<disposable ginbar_m2_bench URL>'
GINBAR_BENCH_LABEL=shared bash src/backend/v2/bench/run_gate.sh
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

If and only if Wallium can be stopped safely without persistent configuration changes:

1. record exactly how Wallium is currently running;
2. stop only Wallium application/backend workload, not PostgreSQL or unrelated services needed by the benchmark;
3. verify the disposable benchmark database is still reachable;
4. rerun only validation/build + HTTP load against the existing seeded benchmark database:

```bash
GINBAR_BENCH_LABEL=quiet \
GINBAR_BENCH_SKIP_DB_PREP=1 \
DATABASE_URL="$DATABASE_URL" \
bash src/backend/v2/bench/run_gate.sh
```

5. restore Wallium exactly as it was and verify it is healthy before ending.

If stopping Wallium is ambiguous or unsafe, do not stop it; report that the quiet comparison was skipped.

## 5. Conditional pool-size probe

Do not tune the implementation during the run.

Only if the 8-connection run shows a clear latency cliff at concurrency 16/32 while server/PostgreSQL CPU still has substantial headroom, collect one additional measurement with a 16-connection pool. This is a measurement, not a configuration decision:

```bash
GINBAR_BENCH_LABEL=quiet-pool16 \
GINBAR_BENCH_SKIP_DB_PREP=1 \
DB_MAX_CONNS=16 \
DATABASE_URL="$DATABASE_URL" \
bash src/backend/v2/bench/run_gate.sh
```

Do not test arbitrary pool sizes or change source code.

## 6. Query-plan evidence to extract

From `explain.txt`, report for each of these shapes:

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

## 8. Review rule

Optimization order after evidence returns:

1. eliminate scans/work/round trips;
2. fix query/index/data layout;
3. only then consider cache/precompute/concurrency changes.

Do not introduce Redis for feed/search merely to mask an unmeasured SQL shape.

Do not modify `docs/v2/STATE.md` during the benchmark. Return the information to the primary engineering session; that session will decide changes and update durable state.
