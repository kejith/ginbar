# M2 backend validation / benchmark runbook

This gate runs on the remote target server. Backend/PostgreSQL measurements execute on the actual server and may be treated as target-server evidence.

The runner is execution-only: it archives the exact checked-out commit into `/tmp`, validates/tests/builds there, uses only a disposable PostgreSQL database, starts the API on loopback, and writes raw evidence under `/tmp`. It must not modify the checked-out repository.

## Current evidence

Target-server runs already establish:

- Docker Go 1.25.14 + real pgx validation works;
- `go mod tidy` and `go test ./...` pass;
- committed `go.mod` / `go.sum` match real tidy output;
- schema and 100k-post seed execute successfully on PostgreSQL 17.11;
- around-post SQL now executes correctly;
- bounded per-post media lookup removes the old-cursor full-media scan: same-run EXPLAIN improved about 18.447 ms -> 0.357 ms;
- the first included-tag rewrite did not improve the planner: required-tag queries still scanned about 158,680 `post_tags` rows and took about 69-71 ms;
- the HTTP matrix has not yet completed because the prior runner used an already-occupied API port.

The current query resolves included tag names to tag IDs before scanning `post_tags`, allowing PostgreSQL to consider the existing partial `(tag_id, post_id DESC)` index directly. No new index has been added.

The runner now also chooses a free loopback API port and verifies its own API process is alive before accepting health.

## 1. Safety / checkout

Use an isolated detached worktree of the exact `origin/astra/m2-core-schema-api` commit requested by the primary engineering session.

Do not switch or modify deployed `main`, legacy `master`, `v2`, or production checkouts. Do not edit, tune, commit, merge, or push anything during the run.

Verify:

```bash
git fetch origin +refs/heads/astra/m2-core-schema-api:refs/remotes/origin/astra/m2-core-schema-api
git rev-parse origin/astra/m2-core-schema-api
git status --short --branch
git rev-parse HEAD
bash -n src/backend/v2/bench/run_gate.sh
```

The detached worktree must be clean and at the exact requested SHA.

## 2. Dedicated disposable PostgreSQL 17

Never use Wallium PostgreSQL or production data.

Use an unused loopback port, with 55432 preferred:

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

until docker exec "$BENCH_PG_CONTAINER" \
  psql -U postgres -d ginbar_m2_bench -Atqc 'select 1' 2>/dev/null | grep -qx 1; do
  sleep 0.5
done

export DATABASE_URL="postgres://postgres:${BENCH_PG_PASSWORD}@127.0.0.1:55432/ginbar_m2_bench?sslmode=disable"
```

If 55432 is occupied, choose another unused loopback port and adjust `DATABASE_URL`. Do not print credentials in returned evidence.

The runner independently checks the database name and SQL stdin path before expensive work.

## 3. Full shared-host run

Keep Wallium in its normal running state.

Pass the disposable PostgreSQL container name so per-second connection sampling uses `docker exec` into that existing container rather than creating a fresh client container on every sample:

```bash
GINBAR_BENCH_LABEL=shared \
GINBAR_BENCH_PG_CONTAINER="$BENCH_PG_CONTAINER" \
DATABASE_URL="$DATABASE_URL" \
bash src/backend/v2/bench/run_gate.sh
```

The runner selects a free loopback API port from 18080-18179 unless `GINBAR_BENCH_PORT` is explicitly supplied. An explicitly supplied occupied port is an error.

Default HTTP matrix:

- first feed page;
- old cursor;
- required tag + score;
- required + excluded tag + score;
- around-post reconstruction;
- concurrency 1 / 4 / 8 / 16 / 32;
- 2,000 measured requests per cell after warmup;
- PostgreSQL pool cap 8.

The runner captures exact Git status/SHA, environment, real Go validation/builds, schema/seed timings, five tuned `EXPLAIN (ANALYZE, BUFFERS)` plans, same-run baseline/tuned plans, relation/index stats, smoke responses, HTTP latency/throughput/errors, API CPU/RSS, system load, PostgreSQL connection counts, and DB counters.

After the run verify `go.mod.after` and `generated-go.sum` are byte-identical to committed files.

## 4. Query-plan evidence

From `explain.txt`, report for all five tuned shapes:

- planning/execution time;
- important nodes and indexes;
- rows/loops;
- shared buffer hits/reads;
- rows removed by filters;
- unexpectedly large scans.

Pay special attention to the two included-tag shapes. Confirm whether `post_tags_tag_post_active_idx` is actually used and whether the previous roughly 158,680-row assignment scan collapses.

Return full plan text for the two slowest tuned shapes.

From `explain-compare.txt`, report numerical baseline-vs-tuned deltas for first feed, old cursor, required-tag+score, and required+excluded+score.

## 5. HTTP/resource evidence

For every case at concurrency 1 / 4 / 8 / 16 / 32 return:

- p50 / p95 / p99 / max milliseconds;
- requests/sec;
- successes/errors/status failures.

Also report:

- peak API CPU and RSS;
- peak PostgreSQL total/active connections from `resource-samples.tsv`;
- load1 range;
- any timeout or HTTP 5xx;
- whether the pool repeatedly reaches 8 active connections.

The connection sampler excludes its own PostgreSQL session from the counts.

## 6. Optional quiet-host comparison

Only if Wallium backend and worker can be stopped safely and reversibly without persistent configuration changes. Do not stop Wallium merely to complete M2.

If performed, keep the seeded disposable PostgreSQL container running and use:

```bash
GINBAR_BENCH_LABEL=quiet \
GINBAR_BENCH_SKIP_DB_PREP=1 \
GINBAR_BENCH_PG_CONTAINER="$BENCH_PG_CONTAINER" \
DATABASE_URL="$DATABASE_URL" \
bash src/backend/v2/bench/run_gate.sh
```

Restore Wallium immediately and verify its prior state. Never stop Wallium PostgreSQL/Redis for this comparison.

## 7. Conditional pool-16 probe

Run only if all are true:

1. at least two substantive endpoints show a clear p95 cliff at concurrency 16/32 versus 8;
2. PostgreSQL active connections repeatedly reach about 8;
3. CPU still has substantial headroom.

If triggered:

```bash
GINBAR_BENCH_LABEL=pool16 \
GINBAR_BENCH_SKIP_DB_PREP=1 \
GINBAR_BENCH_PG_CONTAINER="$BENCH_PG_CONTAINER" \
DB_MAX_CONNS=16 \
DATABASE_URL="$DATABASE_URL" \
bash src/backend/v2/bench/run_gate.sh
```

This is measurement only. Do not change application defaults.

## 8. Failure rule

If a prepared stage fails, stop dependent work and return the exact failure plus all evidence already generated. Do not edit source, SQL, scripts, indexes, or configuration to bypass it.

## 9. Cleanup

After measurements and after Wallium is confirmed running:

```bash
docker stop "$BENCH_PG_CONTAINER"
docker volume rm "$BENCH_PG_VOLUME"
unset DATABASE_URL BENCH_PG_PASSWORD BENCH_PG_CONTAINER BENCH_PG_VOLUME
```

Keep `/tmp/ginbar-m2-gate-*` result directories for follow-up. Verify production checkouts were untouched and the benchmark worktree remains clean.

Do not modify `docs/v2/STATE.md` during execution. Return evidence to the primary engineering session; architecture, fixes, commits, and durable state are decided there.
