# Ginbar v2 performance record

Last consolidated: 2026-10-03

This file contains the accepted performance evidence that should remain stable across handoff sessions. `STATE.md` should only carry performance facts needed for the immediate next task.

## Comparison policy

There is currently **no controlled apples-to-apples v1 versus v2 end-to-end benchmark**. Do not claim a global rewrite speedup yet.

What is measured and safe to state:

- v2 frontend interaction costs and bundle sizes from M1;
- v2 backend/API/query performance from M2;
- measured within-v2 SQL/query-shape improvements;
- v2 media-ingestion/source-verification/publication/image-processing costs;
- API/media-worker coexistence and capacity tradeoffs on the target host.

A direct v1/v2 comparison should be added only when both versions can be exercised with the same dataset, endpoint/interaction semantics, host state, concurrency, and measurement method.

## Target host

Backend/media measurements below were taken on the rewrite target host unless otherwise stated:

- Ubuntu 24.04;
- Intel i7-7700, 4 physical cores / 8 logical CPUs;
- ~64 GiB RAM;
- local NVMe RAID1;
- 1 Gbit/s network;
- shared host with other services left running for shared-host gates.

CPU is the constrained resource; RAM and NVMe have substantially more headroom.

## M1 frontend / board

Source: [`M1.md`](M1.md).

Accepted architecture:

- SolidJS + TypeScript + Vite;
- no virtualization initially;
- incremental retention + CSS containment;
- stable absolute row keys;
- targeted selected-row/post reactivity;
- explicit viewport preservation for prepends.

Measured production assets from the first client run:

- JavaScript: **26,539 B raw / 10,122 B gzip**;
- CSS: **3,293 B raw / 1,402 B gzip**.

Representative corrected 10,000-post desktop run:

- same-row selection sync p95: **0.3 ms**;
- cross-row selection sync p95: **0.4 ms**;
- cross-row frame p95: **17.1 ms**;
- retained DOM nodes: **32,532**;
- Long Tasks during the matrix: **0**.

Earlier client runs kept selection sync p95 around **0.4–0.5 ms** from 320 through 10,000 retained posts, indicating the selected-row interaction path did not scale with total retained post count in the tested range.

The prepend-anchor regression was corrected and validated with **0 px residual movement** at both 1440x900 and 390x844.

Frontend/browser numbers are client-machine evidence, not target-server evidence.

## M2 Go API / PostgreSQL

Source: [`M2.md`](M2.md).

Accepted stack/limits:

- Go 1.25;
- standard `net/http`;
- pgx/v5;
- PostgreSQL 17;
- PostgreSQL pool cap **8**;
- no Redis dependency without measured need.

### Within-v2 query-shape improvements

These are real before/after measurements within M2 and are not v1 comparisons.

Bounded per-post media lookup for the old-cursor path:

- before: **9.568 ms** plan execution;
- after: **0.156 ms**;
- same-run improvement: about **61x lower execution time** for that plan shape.

Included-tag filtering using resolved immutable tag IDs and the existing active partial index:

- tag + score: **71.240 ms -> 1.563 ms**;
- tag + excluded tag + score: **70.068 ms -> 1.899 ms**;
- included assignment scan: about **158,680 -> 1,587 rows**.

These improvements came from reducing scanned work and changing query shape/index use, consistent with the rewrite optimization order.

### Final around-post HTTP gate

2,000 successful requests and zero errors per concurrency cell:

| concurrency | p95 | throughput |
| ---: | ---: | ---: |
| 1 | **2.277 ms** | **557.8 req/s** |
| 4 | **2.628 ms** | **1,726.8 req/s** |
| 8 | **4.871 ms** | **2,448.5 req/s** |
| 16 | **8.443 ms** | **2,527.0 req/s** |
| 32 | **13.712 ms** | **2,718.6 req/s** |

Final corrected query plan:

- planning: **1.981 ms**;
- execution: **0.465 ms**;
- maximum rows: 30 newer + selected + 30 older;
- all branches use bounded media lookup.

Resource evidence:

- peak API CPU: **34.9%**;
- peak API RSS: about **18.9 MiB**;
- PostgreSQL active connections peaked at **7**.

Decision: pool 8 remains sufficient; pool 16 is not justified.

## M3 ingestion / durable job boundary

Accepted ingestion staging:

- 8 MiB durable source staging: **45.1468 ms/op**, **186.06 MB/s**.

Ingestion PostgreSQL CTE:

- concurrency 1: **0.348 ms**, **2,870.87 TPS**;
- concurrency 4: **0.457 ms**, **8,761.36 TPS**.

Source SHA-256 verification, release mode:

- 8 MiB: **35.104 ms**, **227.91 MiB/s**;
- 64 MiB: **298.757 ms**, **214.92 MiB/s**;
- 256 MiB: **1,122.598 ms**, **228.05 MiB/s**.

Fenced publication SQL on PostgreSQL 17.11:

- 1,000/1,000 successful publications;
- average statement latency: **1.539 ms**;
- throughput: **644.22 TPS**;
- bounded/indexed one-row execution: **0.949 ms**.

## M3 still-image processing

Current accepted configuration:

- one media job at a time;
- one AVIF encoder thread;
- `ravif` speed 10;
- main quality 75;
- thumbnail quality 60;
- canonical max dimension 1,280 px;
- 256x256 thumbnail;
- deterministic no-overwrite durable publication.

Real-media processing medians:

| source | process median | output |
| --- | ---: | --- |
| 172x178 JPEG | **79.145 ms** | AVIF + thumbnail |
| 10109x4542 JPEG | **1,292.111 ms** | 1280x575 canonical + thumbnail |
| 800x600 PNG | **259.259 ms** | AVIF + thumbnail |
| 550x368 WebP | **170.883 ms** | AVIF + thumbnail |
| deterministic 8192x8192 PNG | **1,923.785 ms** standalone | ~370 MiB max RSS |

The 8192x8192 case intentionally probes the large-valid-image path rather than typical image latency.

## Media worker / API coexistence

Final load-sensitivity curve used five baseline and five worker-overlap rounds at each HTTP concurrency. All 40 primary HTTP rounds and all 20 worker fixtures were valid with zero HTTP errors.

Median baseline -> one-worker-overlap results:

| concurrency | baseline p95 | loaded p95 | p95 delta | baseline p99 | loaded p99 | RPS delta |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | 2.329 ms | 2.427 ms | **+4.21%** | 2.576 ms | 2.623 ms | **-7.87%** |
| 2 | 2.550 ms | 2.621 ms | **+2.78%** | 2.836 ms | 2.862 ms | **-6.03%** |
| 4 | 2.853 ms | 3.199 ms | **+12.12%** | 3.298 ms | 4.420 ms | **-4.75%** |
| 8 | 4.665 ms | 5.888 ms | **+26.22%** | 5.693 ms | 7.177 ms | **-12.65%** |

At concurrency 8:

- loaded median max latency: **10.949 ms**;
- all measured requests succeeded;
- baseline API + PostgreSQL CPU median: about **6.16 logical CPUs**;
- worker CPU: about **1.12–1.15 logical CPUs**.

Interpretation:

- the image worker has a real shared-host peak-capacity cost;
- the effect is small at lower API concurrency and grows near CPU saturation;
- absolute API tail latency remained within the project's current low-single-digit/low-tens-of-ms target in the measured gate;
- do not call background image processing free;
- do not increase media concurrency above one job or AVIF threads above one without new measurements.

## Scheduling/isolation experiments

At saturated c8 load, the following were tested and did not justify permanent complexity:

- unrestricted worker;
- reduced cgroup CPU weight;
- 0.5 CPU quota;
- safe cpuset/physical-core partitioning.

Low weight did not materially protect the API. A 0.5 CPU quota reduced some interference but made the standalone worker roughly **3.69x slower** in that experiment. Reserving a physical core imposed a large API capacity tax before worker work began because the target host has only four physical cores.

`perf` call-stack profiling was unavailable because the host used `perf_event_paranoid=4`; that security setting was intentionally not changed.

Decision: wait for the real continuous worker runner, then benchmark its actual wakeup/renewal behavior before adding admission/scheduler mechanisms.

## Current performance conclusions

1. M1 demonstrated that the chosen board architecture keeps selection-state work effectively constant through the tested 10,000-post retained range.
2. M2 hot SQL benefited substantially from eliminating scans and bounding work; the resulting around endpoint remains low-millisecond through useful concurrency on the target host.
3. Durable media ingestion/publication overhead is small relative to large-image codec work.
4. Image processing is CPU-heavy enough to reduce peak API capacity on the 4-core target host, but the current one-job/one-encoder-thread limit preserved acceptable absolute latency in the accepted gate.
5. The next meaningful performance test is the **real long-running worker**, not additional synthetic scheduler tuning.
6. There is still no valid overall v1-versus-v2 speedup number. Add one only after equivalent end-to-end behavior exists on both sides.
