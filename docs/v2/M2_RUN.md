# M2 backend validation / benchmark runbook

Target-server measurements are authoritative for backend/PostgreSQL performance. Execution must use an isolated detached worktree and disposable PostgreSQL database; never Wallium data.

## Current evidence

The full shared-host gate completed on commit `1b74ae6e271c544847e7bbbc9e23c8a136007298` with Wallium left running:

- Docker Go 1.25.14 + real pgx: `go mod tidy` and `go test ./...` pass;
- committed `go.mod` / `go.sum` match tidy output byte-for-byte;
- PostgreSQL 17.11 schema + deterministic 100k-post seed succeed;
- all five query plans are bounded and use the intended indexes;
- all 25 HTTP cells completed with 2,000 successes and zero errors each;
- pool 8 reached eight active PostgreSQL connections in only 1 of 52 resource samples;
- peak API CPU was 51%, peak RSS about 20 MiB;
- pool 16 is not justified by current evidence.

Validated query decisions:

- bounded per-post media lookup: keep;
- resolved tag-ID include filter using `post_tags_tag_post_active_idx`: keep;
- no additional search index justified;
- hyphenated tags fixed and validated.

After Run 7, scope review found one correctness issue: the selected around-post branch bypassed the request's content-visibility filters. `Filters` represent allowed visibility, not merely feed context, so this could expose NSFW/secret content through a direct link before authenticated visibility is wired in.

Current corrected semantics:

- newer/older context: allowed visibility + search predicates;
- selected canonical post: allowed visibility, but not search predicates;
- release/deletion/media-readiness constraints always apply.

The full Run 7 feed/search/pool evidence remains valid. Only a targeted around visibility/performance regression is still required.

## 1. Safety / checkout

Use a fresh detached worktree at the exact `origin/astra/m2-core-schema-api` commit requested by the primary engineering session.

Do not modify deployed `master`, `v2`, Wallium, production checkouts, source, SQL, docs, config, commits, or indexes during execution.

Verify:

```bash
git fetch origin +refs/heads/astra/m2-core-schema-api:refs/remotes/origin/astra/m2-core-schema-api
git rev-parse origin/astra/m2-core-schema-api
git rev-parse HEAD
git status --short --branch
bash -n src/backend/v2/bench/run_visibility_gate.sh
```

## 2. Disposable PostgreSQL 17

Use a fresh loopback-only database:

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

If 55432 is occupied, choose another unused loopback port and adjust only the temporary `DATABASE_URL`.

## 3. Targeted visibility gate

Keep Wallium running.

Run exactly:

```bash
GINBAR_BENCH_PG_CONTAINER="$BENCH_PG_CONTAINER" \
DATABASE_URL="$DATABASE_URL" \
bash src/backend/v2/bench/run_visibility_gate.sh
```

The script archives the exact HEAD into `/tmp`, runs real Go 1.25 tidy/tests/builds there, applies schema + seed, captures the corrected around EXPLAIN, starts the API on a free loopback port, performs visibility/search smoke assertions, and benchmarks only the around endpoint at concurrency 1 / 4 / 8 / 16 / 32.

Expected correctness assertions:

- `/api/v2/posts/49999/around?radius=30` -> 200;
- selected ID 49999 appears exactly once and is SFW;
- response is 61 posts and all are inside default SFW visibility;
- `/api/v2/posts/49999/around?radius=30&q=tag-42` -> 200 and still contains selected ID 49999 even though search is surrounding-context state;
- `/api/v2/posts/50000/around?radius=30` -> 404 because seed post 50000 is NSFW and default allowed visibility is SFW.

This simultaneously verifies canonical reconstruction and the visibility boundary.

## 4. Performance evidence to return

From `explain-visibility.txt`, report:

- planning/execution time;
- branch structure and row counts;
- indexes used;
- shared buffers;
- confirmation that newer/older use strict `>` / `<`, selected uses exact `= 49999`, and all three enforce `content_filter=0`;
- confirmation that media access remains bounded per post.

From the five `http-around-visible-c*.json` files return p50/p95/p99/max, requests/sec, successes/errors for concurrency 1/4/8/16/32.

Compare against Run 7 around-post measurements:

| concurrency | Run 7 p95 ms | Run 7 RPS |
|---:|---:|---:|
| 1 | 2.161 | 579.73 |
| 4 | 2.650 | 1776.76 |
| 8 | 4.119 | 2666.89 |
| 16 | 7.523 | 2729.06 |
| 32 | 13.396 | 2784.65 |

Minor run-to-run variation is expected. Report material regression rather than tuning on noise.

Also return peak API CPU/RSS and PostgreSQL total/active connection counts from `resource-samples.tsv`.

## 5. Failure rule

If a prepared stage fails, preserve the result directory and return the exact failure plus all generated evidence. Do not patch or bypass the failure.

## 6. Cleanup

After execution:

```bash
docker stop "$BENCH_PG_CONTAINER"
docker volume rm "$BENCH_PG_VOLUME"
unset DATABASE_URL BENCH_PG_PASSWORD BENCH_PG_CONTAINER BENCH_PG_VOLUME
```

Verify Wallium remains in its prior state, production checkouts are untouched, and the detached worktree is clean. Keep `/tmp/ginbar-m2-visibility-*` evidence for follow-up.

Do not update `STATE.md` from the local agent. Return evidence to the primary engineering session; integration decisions happen there.
