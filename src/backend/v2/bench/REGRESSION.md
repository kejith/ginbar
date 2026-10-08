# M7 disposable performance-regression fixture

Status: candidate fixture; numerical latency budget **not yet calibrated or accepted**.
This is not a target-host benchmark result or a v1/v2 speedup claim.

Run from the repository root, with Docker access and Python 3:

```sh
GINBAR_PERF_EXPECT_SHA="$(git rev-parse HEAD)" bash scripts/v2-perf-regression-test.sh
```

This command starts an isolated, ephemeral PostgreSQL 17.11 container on a
private temporary Docker network (no published ports), loads the existing
`001_core.sql` and `bench/seed.sql` with exactly 100,000 synthetic posts,
and executes the five existing `bench/explain.sql` plans with
`EXPLAIN (ANALYZE, BUFFERS)`. It compiles the existing Go API and
`bench/httpbench.go` in Go 1.25.0, starts the API in another temporary
container with pool cap 8, and benchmarks:

| HTTP case | Concurrency | Requests |
| --- | ---: | ---: |
| First feed page, limit 60 | 1 | 2,000 |
| Post-ID cursor page, before 50000, limit 60 | 1 | 2,000 |
| Tag+score filtered cursor page | 1 | 2,000 |
| Around post 50000, radius 30 | 1 | 2,000 |
| Around post 50000, radius 30 | 8 | 2,000 |

Each cell has 20 warmup requests; all requests must complete with HTTP 200,
zero errors, and finite positive latency/throughput metrics. SQL plans must
have completed execution and buffer measurements, use indexed paths, have
**zero** hot-table (`posts`/`media`) sequential scans and **zero**
external sorts or temporary spills. The fixed post-ID cursor query shapes
come directly from the accepted M2 benchmark. Results include the exact Git
SHA, Git status, environment, server version, seed/schema logs, five plans,
HTTP JSON, API/PG logs, and validated measurement summary. Temporary
containers/network and compiled binaries are removed on all exits; result
files are kept under a temporary directory by default.

## Latency calibration is deliberately separate

`docs/v2/PERFORMANCE.md` records the accepted M2 around-post
p95 at c1 **2.277 ms** and c8 **4.871 ms**, 2,000 successful requests/cell.
These numbers were measured on the target host and cannot be applied as
hard thresholds to an uncalibrated CI VM or a different PostgreSQL/Go/host
combination. Other v2 product changes may also affect comparison. The new
fixture therefore **does not report numeric latency PASS/FAIL by default**.
Its CI gate enforces deterministic HTTP and SQL plan-shape checks and
captures real p95, p99, and RPS for later calibration.

Before adopting numeric budgets, perform repeated baseline runs of this
*exact fixture* on an authorized disposable PostgreSQL target with the same
host class, DB/Go versions, workload and concurrency; retain raw per-run
measurements, background load snapshots, exact SHAs, plans and cleanup.
Review variability and choose defensible per-case p95/p99 and throughput
thresholds in a separate coding decision. Do not silently treat a single
best run or the historical M2 figures as a comparable baseline.

To enforce **already accepted** environment-specific budgets, set
`GINBAR_PERF_BUDGET_FILE=/path/to/accepted-budget.json`. The file must be
JSON with `schema: 1`, non-empty `provenance` explaining the accepted
measurement source, and a `cases` mapping containing **exactly** these five
keys: `feed-first-c1`, `feed-cursor-c1`, `search-tag-score-c1`,
`around-50000-c1`, `around-50000-c8`. Each key requires at least one
positive numeric bound from `maxP95Ms`, `maxP99Ms`, and
`minRequestsPerSecond`; the validator fails any exceeded bound. No numeric
budget file is bundled yet, to avoid inventing unverified limits.

## Execution and boundaries

- `GINBAR_PERF_EXPECT_SHA`: required explicit 40-character exact checked-out Git
  commit SHA when running manually (in CI, the workflow provides `GITHUB_SHA`).
  Execute from a **clean checkout at this exact SHA**, never from a copied
  archive embedded in another repository. The fixture resolves its own
  source root and refuses mismatched SHA, dirty tracked files, or an
  archive without its own checkout.
- `GINBAR_PERF_RESULTS_DIR`: retain raw results at the specified path.
- `GINBAR_PERF_REQUESTS`: measured requests/case (default 2,000; minimum 100,
  useful only for diagnostics; do not mix with acceptance baselines).
- `GINBAR_CI_CACHE_DIR`: optional shared Docker Go module/build cache.
- All API and DB traffic remains inside a disposable Docker network; the
  fixture intentionally ignores production `DATABASE_URL` and never
  writes to an existing PostgreSQL instance.
- If Docker/CI is unavailable, delegate a **LOCAL DIAGNOSTIC EXECUTION**
  at the exact candidate SHA. Diagnostic evidence cannot establish green
  exact-candidate CI or independent acceptance.
- PostgreSQL planner variation can legitimately change access paths; a
  plan-shape FAIL must be investigated against the raw plan before modifying
  SQL or indexes. This checks five representative feed/search/around queries,
  not auth, messaging, media processing, or arbitrary production traffic.
