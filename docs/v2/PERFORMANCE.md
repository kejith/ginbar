# Ginbar v2 performance record

Last consolidated: 2026-10-04

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
- local NVMe RAID1/ext4;
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

## M3 one-image worker / API coexistence

The earlier load-sensitivity curve used five baseline and five one-image worker-overlap rounds at each HTTP concurrency. All 40 primary HTTP rounds and all 20 worker fixtures were valid with zero HTTP errors.

Median baseline -> one-worker-overlap results:

| concurrency | baseline p95 | loaded p95 | p95 delta | baseline p99 | loaded p99 | RPS delta |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | 2.329 ms | 2.427 ms | **+4.21%** | 2.576 ms | 2.623 ms | **-7.87%** |
| 2 | 2.550 ms | 2.621 ms | **+2.78%** | 2.836 ms | 2.862 ms | **-6.03%** |
| 4 | 2.853 ms | 3.199 ms | **+12.12%** | 3.298 ms | 4.420 ms | **-4.75%** |
| 8 | 4.665 ms | 5.888 ms | **+26.22%** | 5.693 ms | 7.177 ms | **-12.65%** |

At concurrency 8, loaded median max latency was **10.949 ms** and all measured requests succeeded. This established that image encoding has a real shared-host peak-capacity cost and motivated validating the actual continuous production runner before adding scheduler complexity.

## M3 production runner target-host gate

Exact tested/integrated worker revision: `534f9c3add3b4bec4cab405544f7741fa11d1b30`.

Evidence bundle: `m3-runner-20261004T001829Z.zip`, SHA-256 `f709ebc3c097e1d4fb88903cbda1dcdb6c90babda4df2ce1058226a66bdd57a3`.

Environment: Ubuntu 24.04.3, Linux 6.8.0-88-generic, i7-7700, 62 GiB RAM, local ext4, Rust/Cargo 1.99.0, Go 1.25.14, PostgreSQL 17.11. Wallium and unrelated shared-host services remained running.

### Idle runner

With the default 500 ms idle poll and no runnable jobs, 61 samples over 60 seconds showed:

- median worker CPU: **0%**;
- peak worker CPU: **0.91%**;
- RSS: **3,712 KiB** throughout;
- PostgreSQL maximum: **2 client connections**, **1 active**;
- database commit count increased by 398 over 68 seconds, about **5.85 commits/s**, consistent with the polling/claim transaction path;
- SIGTERM exit: **30 ms**, status 0, with worker DB sessions gone afterward.

Decision: current single-worker polling overhead is acceptable; no Redis/wakeup service is justified.

### Lease renewal and shutdown/restart

A deterministic 8192x8192 PNG under a 1 s test lease ran for **2.322 s**. Seven distinct lease-expiry values were observed while attempt 1, generation 1 and worker ownership remained stable; the job then succeeded, media became ready and the post was released.

SIGTERM during another 8192x8192 encode exited in **1.691 s**. The job was safely returned to pending at attempt/generation 1 without media publication or post release. Restart reclaimed it at attempt/generation 2 and completed successfully. This matches the documented synchronous-codec cancellation boundary: encode may finish its current synchronous call, but stale work cannot perform authoritative fenced publication.

### Continuous drain

A 2,000-job drain completed with:

- **2,000 succeeded**, zero failed/stale running jobs;
- all successful jobs at attempt/generation 1;
- maximum concurrent running jobs: **1**;
- active duration: **1,328.759 s**;
- throughput: **1.505 jobs/s**, **90.31 jobs/min**;
- worker CPU during running jobs: median **93.69%**, mean **94.09%**, peak **99.01%**;
- worker RSS during running jobs: median **75,944 KiB**, mean **78,310 KiB**, peak **130,332 KiB**;
- PostgreSQL maximum: **3 connections**, **2 active**.

The worker therefore consumes roughly one logical CPU while continuously encoding the tested 2048x2048 workload, as intended by the one-job/one-encoder-thread limit.

### Continuous-runner API coexistence

The accepted same-run comparison used five baseline and five loaded rounds at each concurrency 1/2/4/8, 2,000 measured requests per round plus warmups. All **80,000 measured requests** succeeded with zero errors. Every loaded round had exactly one running media job at its before/after checks; baseline rounds had none.

Medians across five rounds per condition:

| concurrency | baseline p95 | loaded p95 | p95 delta | baseline p99 | loaded p99 | p99 delta | baseline RPS | loaded RPS | RPS delta |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | 2.183 ms | 2.283 ms | **+4.57%** | 2.388 ms | 2.548 ms | **+6.70%** | 563.74 | 533.11 | **-5.43%** |
| 2 | 2.360 ms | 2.435 ms | **+3.20%** | 2.551 ms | 2.696 ms | **+5.68%** | 991.21 | 960.90 | **-3.06%** |
| 4 | 2.625 ms | 2.830 ms | **+7.81%** | 2.894 ms | 3.183 ms | **+9.98%** | 1,753.34 | 1,688.46 | **-3.70%** |
| 8 | 3.947 ms | 4.692 ms | **+18.88%** | 4.833 ms | 5.537 ms | **+14.57%** | 2,667.60 | 2,406.90 | **-9.77%** |

At concurrency 8, loaded median max latency was **7.967 ms** versus **6.083 ms** baseline. PostgreSQL active connections peaked at 7 in both baseline and loaded c8 windows against the API pool cap of 8; this is context, not evidence of pool exhaustion.

Decision: accept the simple production runner architecture. The shared-host capacity cost is measurable near saturation but absolute latency remained low, all requests succeeded, and the one-job/one-encoder-thread limit kept interference within the current project budget. Do not add cgroups, core pinning, quotas, Redis wakeups, or load-admission mechanisms from current evidence.

## Scheduling/isolation experiments

Earlier saturated-c8 experiments tested unrestricted worker execution, reduced cgroup CPU weight, a 0.5 CPU quota, and safe physical-core partitioning. Low weight did not materially protect the API; the 0.5 CPU quota made the standalone worker roughly **3.69x slower**; reserving a physical core imposed a large API capacity tax before worker work began on the four-core host.

`perf` call-stack profiling was unavailable because the host used `perf_event_paranoid=4`; that security setting was intentionally not changed.

The accepted continuous-runner gate confirms that none of these mechanisms is currently justified.

## Current performance conclusions

1. M1 demonstrated that the chosen board architecture keeps selection-state work effectively constant through the tested 10,000-post retained range.
2. M2 hot SQL benefited substantially from eliminating scans and bounding work; the resulting around endpoint remains low-millisecond through useful concurrency on the target host.
3. Durable media ingestion/publication overhead is small relative to codec work.
4. Still-image processing is CPU-heavy enough to reduce peak API capacity on the four-core target host, but one job / one encoder thread preserves acceptable absolute latency and zero-error behavior in both one-shot and continuous-runner gates.
5. The production PostgreSQL polling/lease-renewal runner is accepted as the M3 baseline; no additional scheduler/wakeup/admission infrastructure is justified now.
6. Video processing must receive its own explicit codec/thread/resource/interference benchmark rather than inheriting image assumptions.
7. There is still no valid overall v1-versus-v2 speedup number. Add one only after equivalent end-to-end behavior exists on both sides.
