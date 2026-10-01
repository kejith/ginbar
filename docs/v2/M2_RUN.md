# M2 backend validation / benchmark runbook

Run this on the remote target server. Unlike M1 browser profiling, these measurements execute in the backend/PostgreSQL environment and may be treated as target-server evidence.

## 1. Checkout and toolchain

Use an isolated worktree based on `astra/m2-core-schema-api`. Do not switch or modify deployed `main`, legacy `master`, or the normal service checkout.

Verify Go 1.25+ is available. A containerized Go toolchain is fine; do not mutate system packages solely for the benchmark.

From `src/backend/v2`:

```bash
go mod tidy
go test ./...
```

Commit `go.sum` only after the real dependency-backed test passes.

## 2. Fresh benchmark database

Use a disposable PostgreSQL database. Never run the benchmark seed against production data.

Apply the schema:

```bash
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f internal/schema/migrations/001_core.sql
```

Seed representative data:

```bash
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f bench/seed.sql
```

The seed creates 100,000 released posts with media and three tags per post, enough to exercise cursor and tag query plans without pretending this is final production cardinality.

## 3. Query plans

Run:

```bash
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f bench/explain.sql
```

Save the full output. For each shape record:

- planning time
- execution time
- actual rows
- buffers hit/read
- scan/index type
- rows removed by filter
- whether work grows unexpectedly for an old cursor or rare tag

Do not tune indexes until the plans are captured.

## 4. API benchmark

Start the API against the disposable DB:

```bash
DATABASE_URL="$DATABASE_URL" DB_MAX_CONNS=8 LISTEN_ADDR=127.0.0.1:18080 go run ./cmd/api
```

Smoke test:

```bash
curl -fsS 'http://127.0.0.1:18080/healthz'
curl -fsS 'http://127.0.0.1:18080/api/v2/feed?limit=60'
curl -fsS 'http://127.0.0.1:18080/api/v2/feed?before=50000&limit=60&q=tag-42%20score:%3E%3D100'
curl -fsS 'http://127.0.0.1:18080/api/v2/posts/50000/around?radius=30'
```

Use an already-installed HTTP load generator if available. Do not install a large benchmarking stack just for this gate. Suggested cases:

- feed first page, 60 posts
- feed around cursor 50,000
- one required tag
- one required + one excluded tag + score predicate
- around post 50,000, radius 30

Run low concurrency first (1, 4, 8), then enough concurrency to reveal whether the 8-connection PostgreSQL pool is saturated. Record latency p50/p95/p99, requests/sec, errors, CPU, and PostgreSQL connection count.

## 5. Review rule

Optimization order:

1. eliminate scans/work/round trips;
2. fix query/index/data layout;
3. only then consider cache/precompute/concurrency changes.

Do not introduce Redis for feed/search unless the PostgreSQL path is measured and a clear ephemeral caching benefit remains.

## 6. Handoff

Update `docs/v2/STATE.md` with:

- exact commit tested
- Go/PostgreSQL versions
- `go test ./...` result
- schema/seed result
- query-plan headline findings
- API latency/concurrency results
- any index/query changes with before/after evidence
- unresolved issues
- one best next task
