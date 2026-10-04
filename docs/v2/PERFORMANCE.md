# Ginbar v2 performance record

Last consolidated: 2026-10-04

This file contains accepted performance evidence that should remain stable across handoff sessions. `STATE.md` should only carry facts needed for the immediate next task.

## Comparison policy

There is currently **no controlled apples-to-apples v1 versus v2 end-to-end benchmark**. Do not claim a global rewrite speedup yet.

Measured and safe to state:

- v2 frontend interaction costs and bundle sizes from M1;
- v2 backend/API/query performance from M2;
- within-v2 SQL/query-shape improvements;
- v2 media-ingestion/source-verification/publication/image/video processing costs;
- API/media-worker coexistence and capacity tradeoffs on the target host.

A direct v1/v2 comparison should be added only when both versions can be exercised with the same dataset, endpoint/interaction semantics, host state, concurrency and measurement method.

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

Accepted architecture: SolidJS + TypeScript + Vite, no virtualization initially, incremental retention + CSS containment, stable row identity and targeted selected-row/post reactivity.

Measured production assets from the first client run:

- JavaScript: **26,539 B raw / 10,122 B gzip**;
- CSS: **3,293 B raw / 1,402 B gzip**.

Representative corrected 10,000-post desktop run:

- same-row selection sync p95: **0.3 ms**;
- cross-row selection sync p95: **0.4 ms**;
- cross-row frame p95: **17.1 ms**;
- retained DOM nodes: **32,532**;
- Long Tasks during the matrix: **0**.

Prepend-anchor regression validation showed **0 px residual movement** at both 1440x900 and 390x844.

Frontend/browser numbers are client-machine evidence, not target-server evidence.

## M2 Go API / PostgreSQL

Accepted stack/limits: Go 1.25, standard `net/http`, pgx/v5, PostgreSQL 17, API pool cap **8**, no Redis dependency without measured need.

Important within-v2 query improvements:

- bounded per-post media lookup: **9.568 ms -> 0.156 ms** plan execution;
- tag + score: **71.240 ms -> 1.563 ms**;
- tag + excluded tag + score: **70.068 ms -> 1.899 ms**;
- included assignment scan: about **158,680 -> 1,587 rows**.

Final around-post HTTP gate, 2,000 successful requests and zero errors per cell:

| concurrency | p95 | throughput |
| ---: | ---: | ---: |
| 1 | **2.277 ms** | **557.8 req/s** |
| 4 | **2.628 ms** | **1,726.8 req/s** |
| 8 | **4.871 ms** | **2,448.5 req/s** |
| 16 | **8.443 ms** | **2,527.0 req/s** |
| 32 | **13.712 ms** | **2,718.6 req/s** |

Final corrected query plan: planning **1.981 ms**, execution **0.465 ms**, maximum 30 newer + selected + 30 older, with bounded media lookup on all branches.

Resource evidence: peak API CPU **34.9%**, peak API RSS about **18.9 MiB**, PostgreSQL active connections peak **7**. Decision: pool 8 remains sufficient; pool 16 is not justified.

## M3 ingestion / durable job boundary

Accepted measurements:

- 8 MiB durable source staging: **45.1468 ms/op**, **186.06 MB/s**;
- ingestion CTE c1: **0.348 ms**, **2,870.87 TPS**;
- ingestion CTE c4: **0.457 ms**, **8,761.36 TPS**;
- SHA-256 verification 8 MiB: **35.104 ms**, **227.91 MiB/s**;
- 64 MiB: **298.757 ms**, **214.92 MiB/s**;
- 256 MiB: **1,122.598 ms**, **228.05 MiB/s**;
- fenced publication SQL: **1,000/1,000 successful**, average statement latency **1.539 ms**, **644.22 TPS**, bounded one-row execution **0.949 ms**.

## M3 still-image processing

Accepted configuration:

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

## M3 production still-image runner target-host gate

Exact target-gated worker revision: `534f9c3add3b4bec4cab405544f7741fa11d1b30`.

Evidence bundle: `m3-runner-20261004T001829Z.zip`, SHA-256 `f709ebc3c097e1d4fb88903cbda1dcdb6c90babda4df2ce1058226a66bdd57a3`.

Idle runner with 500 ms polling: median CPU **0%**, peak **0.91%**, RSS **3,712 KiB**, clean SIGTERM in **30 ms**.

A forced 1 s lease on a long 8192x8192 job observed seven increasing lease-expiry values while attempt/generation 1 remained stable; the job succeeded.

Continuous 2,000-job drain:

- **2,000/2,000 succeeded**, zero failed/stale jobs;
- maximum running jobs: **1**;
- active duration: **1,328.759 s**;
- throughput: **1.505 jobs/s**, **90.31 jobs/min**;
- worker CPU median **93.69%**, mean **94.09%**, peak **99.01%**;
- RSS median **75,944 KiB**, mean **78,310 KiB**, peak **130,332 KiB**.

Continuous-runner API coexistence used five baseline and five loaded rounds at c1/c2/c4/c8, 2,000 measured requests each. All **80,000** requests succeeded; every loaded round had exactly one running media job.

| concurrency | baseline p95 | loaded p95 | p95 delta | baseline p99 | loaded p99 | p99 delta | baseline RPS | loaded RPS | RPS delta |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | 2.183 ms | 2.283 ms | **+4.57%** | 2.388 ms | 2.548 ms | **+6.70%** | 563.74 | 533.11 | **-5.43%** |
| 2 | 2.360 ms | 2.435 ms | **+3.20%** | 2.551 ms | 2.696 ms | **+5.68%** | 991.21 | 960.90 | **-3.06%** |
| 4 | 2.625 ms | 2.830 ms | **+7.81%** | 2.894 ms | 3.183 ms | **+9.98%** | 1,753.34 | 1,688.46 | **-3.70%** |
| 8 | 3.947 ms | 4.692 ms | **+18.88%** | 4.833 ms | 5.537 ms | **+14.57%** | 2,667.60 | 2,406.90 | **-9.77%** |

Decision: keep PostgreSQL polling/heartbeat, one media job at a time and one AVIF encoder thread. Do not add Redis wakeups, cgroups, affinity, quotas or load admission from this evidence.

## M3 compatible-MP4 zero-transcode target-host gate

Exact tested feature revision: `e437e36194013579f3778292d81ddfe0ddf6a138`.

Exact pre-target CI run: `37223604648`, success including the hermetic release-worker build.

Accepted implementation was replayed onto current `v2` as `2f88bccaa22895a28da67bbb02cbac193df2598f`; post-integration CI run `37229800177` succeeded.

Target evidence package name: `m3-video-gate-20261004T182342Z.zip`.

Evidence identity caveat: the target agent reported retained-ZIP SHA-256 `c0da513144a14b0b709c180b7e4a3e1e3dc1847438b04a5fb5e825264f6f89d0`, while the exact uploaded archive analyzed in the primary session hashes to `10fe7cc9544ec6fb42b0493af367a796fde6b3e8dd7840200c1b290132c279ea`. The uploaded package's internal `ZIP-MANIFEST.tsv` contains **523 entries** and all 523 files matched their recorded SHA-256 values. Record both hashes; do not claim that the uploaded bytes were the same outer ZIP as the retained local file without resolving that difference.

Environment: Ubuntu 24.04, kernel 6.8.0-88-generic, i7-7700 (4 cores / 8 threads), 62 GiB RAM, ext4 target storage. Wallium and the pre-existing Ginbar CI VM remained running and untouched.

### Fixtures and functional correctness

Selected fixtures:

- representative: **31,179,285 B**, SHA-256 `37bf25b410af877bcb6a66966a474f759af952633cab5cf8d14285551c994d07`;
- sustained load: **121,948,657 B**, SHA-256 `9a84db6c268eecf52103ff6fb65888a4f49eaa459052e2d92a597459042ed6ac`;
- long operation: **364,334,721 B**, SHA-256 `1fdd2f0850bc7d65aba92fd9f860b854a5c12806d25ca0824b6f70c156b3d25d`.

Single-job processing passed at attempt/generation 1. The canonical `video/mp4` object had the exact source byte size/SHA, validated 1280x720 dimensions and 30,000 ms duration, and the deterministic AVIF thumbnail existed before ready/release state.

### Probe and thumbnail costs

Thirty measurements each:

- FFprobe median **56.076 ms**, measured p95 about **57.1 ms**, max **87.957 ms**;
- thumbnail extraction median **86.267 ms**, measured p95 about **87.1 ms**, max **87.853 ms**.

Observed process thread maxima were **1** for FFprobe and **3** for FFmpeg. The FFmpeg command retained explicit decoder/filter/encoder thread limits; the additional observed threads are process/runtime implementation threads rather than evidence of unbounded codec parallelism.

### 200-job zero-transcode drain

Representative 31.2 MiB compatible MP4 source, unique post/source identities, one production worker:

- **200/200 succeeded**;
- zero failed or stale-running jobs;
- all successful jobs remained attempt/generation 1;
- maximum running jobs: **1**;
- all **200 filesystem validations passed**;
- active wall time: **114.238 s**;
- throughput: **1.751 jobs/s**;
- source bytes processed: about **54.586 MB/s**;
- worker CPU samples: median **61.0%**, mean **60.51%**, p95 **61.3%**, peak **75.0%**;
- worker RSS: median **18,986 KiB**, mean **18,740 KiB**, p95 **19,428 KiB**, peak **19,444 KiB**;
- worker thread count: median **10**, p95/peak **12**.

### Lease renewal and shutdown/restart

With a 1 s lease on the 364.3 MiB long fixture, **11 distinct increasing lease-expiry values** were observed while ownership stayed with the same worker at attempt/generation 1. The job completed successfully with exact canonical size/SHA and ready/released DB state.

SIGTERM during another long operation:

- signal-to-exit: **5.575 ms**;
- immediately after exit the job was pending at attempt/generation 1;
- media rows: **0**;
- post remained unreleased;
- no canonical or thumbnail output existed at that point;
- restart completed successfully at attempt/generation **2/2** with exact source size/SHA.

This validates cancellation/requeue/fencing for the observed shutdown point. It did not exercise restart reuse of an already-durable canonical file because no canonical object existed when SIGTERM landed; idempotent durable-file reuse remains covered by CI/integration tests.

### API coexistence

The accepted API gate used the around endpoint `/api/v2/posts/50000/around?radius=30`, pool cap 8, five baseline and five loaded rounds at c1/c2/c4/c8, 2,000 measured requests per round. All **80,000/80,000** measured requests succeeded with zero errors. All 20 loaded rounds had exactly one running video job before and after measurement.

Medians across five rounds per condition:

| concurrency | baseline p95 | loaded p95 | p95 delta | baseline p99 | loaded p99 | p99 delta | baseline RPS | loaded RPS | RPS delta |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | 2.219 ms | 2.431 ms | **+9.55%** | 2.404 ms | 2.591 ms | **+7.78%** | 562.45 | 510.73 | **-9.20%** |
| 2 | 2.384 ms | 2.544 ms | **+6.73%** | 2.566 ms | 2.783 ms | **+8.43%** | 996.41 | 920.75 | **-7.59%** |
| 4 | 2.590 ms | 2.887 ms | **+11.49%** | 2.811 ms | 3.234 ms | **+15.03%** | 1,764.62 | 1,628.06 | **-7.74%** |
| 8 | 4.111 ms | 5.246 ms | **+27.61%** | 4.857 ms | 6.364 ms | **+31.02%** | 2,604.32 | 2,233.88 | **-14.22%** |

At c8, median max latency was **9.084 ms** loaded versus **8.405 ms** baseline. Loaded API sampling showed worker CPU median about **81.4%**, worker RSS median about **17,424 KiB**, exactly one running job, and host load peaking around **4.10**. No HTTP errors occurred.

The benchmark seed originally made post 50000 content filter 2 while the prescribed endpoint uses the default SFW filter. The target run changed only that row in the disposable DB to filter 0. Commit `4830d137d59b57ea4abcd2092e3c9010f7e2683e` codifies that exact benchmark anchor in `bench/seed.sql`; CI run `37229977045` passed.

### Acceptance decision and caveats

Accept the zero-transcode compatible-MP4 path and retain the simple worker architecture.

Rationale:

- correctness/fencing/idempotency gates passed;
- canonical bytes are copied without video transcoding and verified exactly;
- worker concurrency stayed at one;
- memory use was small;
- API interference is measurable, especially at c8, but absolute p95/p99 remained **5.246/6.364 ms** with zero errors and useful throughput;
- the observed penalty is not sufficient evidence for cgroups, affinity, quotas, Redis wakeups or load admission on the current host.

Caveats:

- video c8 interference is worse than the accepted continuous still-image runner and should remain visible in regression testing;
- host shared-workload evidence includes measurement-window samples plus pre-/post-cleanup observations, but the requested three separate raw before/during/after host snapshots were not captured; future target gates must capture them explicitly;
- the outer ZIP hash mismatch described above remains an evidence-provenance caveat, although the uploaded package's internal manifest verified fully.

## Scheduling/isolation experiments

Earlier saturated-c8 experiments tested unrestricted worker execution, reduced cgroup CPU weight, a 0.5 CPU quota and physical-core partitioning. Low weight did not materially protect the API; the 0.5 CPU quota made the standalone worker roughly **3.69x slower**; reserving a physical core imposed a large API capacity tax before worker work began. `perf` call-stack profiling was unavailable because the host used `perf_event_paranoid=4`; that setting was intentionally not changed.

## Current performance conclusions

1. M1 demonstrated effectively constant selected-row interaction work through the tested 10,000-post retained range.
2. M2 hot SQL benefited substantially from eliminating scans and bounding work; the around endpoint remains low-millisecond through useful target-host concurrency.
3. Durable media ingestion/publication overhead is small relative to codec and large-file work.
4. Still-image processing is CPU-heavy enough to reduce peak API capacity, but one job / one encoder thread preserves low absolute latency and zero-error behavior.
5. Compatible-MP4 passthrough is accepted with one active media job: it avoids video transcoding, keeps memory small, and preserves correctness, while imposing a measurable c8 capacity cost that should be regression-tested.
6. The PostgreSQL polling/lease-renewal runner remains the accepted baseline; current evidence does not justify scheduler/wakeup/admission infrastructure.
7. There is still no valid overall v1-versus-v2 speedup number.
