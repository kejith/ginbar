# Ginbar v2 state / handoff

Last updated: 2026-10-04
Phase: **M3 media pipeline — perceptual duplicate processing accepted; duplicate query corrected after target collision evidence; focused query-plan revalidation pending**
Integration branch: `v2`
Legacy branch: `master` (read-only for rewrite work)

Read this file first. Use [`PLAN.md`](PLAN.md) for stable milestone/architecture rules and [`PERFORMANCE.md`](PERFORMANCE.md) for accepted benchmark history. Do not use chat history as project memory.

## Current rewrite status

- M1 board performance prototype: **complete and integrated**.
- M2 fresh PostgreSQL schema + core Go API: **complete and integrated**.
- M3 durable PostgreSQL media jobs, ingestion, source verification, deterministic output publication, generation-fenced DB publication: **integrated**.
- M3 still-image JPEG/PNG/WebP -> deterministic AVIF canonical + thumbnail pipeline: **accepted and integrated**.
- M3 production worker runner with polling, lease renewal, cancellation, reconnect/backoff, graceful shutdown and crash/restart recovery: **accepted and integrated**.
- M3 bounded video probe/compatibility boundary: **integrated**.
- M3 compatible-MP4 H.264/AAC zero-transcode canonical publication + deterministic AVIF thumbnail + production runner dispatch: **accepted and integrated**.
- Hermetic target worker release-build contract: **integrated**.
- Self-hosted v2 CI: **integrated**.
- M3 perceptual duplicate detection: **hashing/processing cost and correctness accepted on target; original exact-match query rejected under a 50,001-row collision bucket; corrected scalar-hash query plus ordered composite index is CI-green and needs one focused target query-plan recheck before integration**.
- M3 remains **in progress**: duplicate candidate revalidation/integration, regeneration and progress/status UI remain; broader video transcoding is not yet justified or scoped.

## Current integration commits / validation

Important commits on `v2`:

- `76942cb8f43fa686546f3e87c3bca6ce1cea12d2` — hermetic worker release build (`scripts/v2-worker-build.sh`);
- `2f88bccaa22895a28da67bbb02cbac193df2598f` — accepted compatible-MP4 video processor integrated onto current `v2`;
- `4830d137d59b57ea4abcd2092e3c9010f7e2683e` — benchmark seed keeps standard around-post anchor 50000 SFW, matching the accepted target measurement fixture;
- `40223d30a629bcace48240e8e222de54ddd347a8` — accepted video performance evidence recorded in `PERFORMANCE.md`.

Relevant accepted CI:

- feature video candidate `e437e36194013579f3778292d81ddfe0ddf6a138`: run `37223604648`, **success**;
- post-integration video commit `2f88bccaa22895a28da67bbb02cbac193df2598f`: run `37229800177`, **success**;
- benchmark-seed cleanup `4830d137d59b57ea4abcd2092e3c9010f7e2683e`: run `37229977045`, **success**.

The applicable CI gate includes Rust 1.99 format/check/tests/Clippy, PostgreSQL-backed worker tests, lockfile stability, clean checkout and the repository-owned hermetic release-worker build when worker/build-contract inputs change.

## Perceptual duplicate-detection candidate

Feature branch: `astra/m3-perceptual-duplicates`, branched from `v2` at `ac980f99bf1706557a48eb420d4600b6d20a2f96`.

Current validated executable candidate head before this state-only commit: `2cef9a9d309639855a9e5b14cfa19191f40acbe5`.

Key candidate commits:

- `6508a7c1503769c2ab7c3caa7b11cb3594fe674a` — add explicit versioned perceptual-hash contract;
- `cc78fa00f01c9568f7cf9479d93d4e574305e4f9` — compute hash from the already-decoded still image and carry it through `ProcessedMedia`;
- `94436f55d5c3b1a820ab25240e9e5925cd813b45` — PostgreSQL-backed exact-duplicate visibility/boundedness tests on top of the duplicate service/query/store slice;
- `627f8b4302244f9e45b9b06092aadb0c2a84ebc7` — compute the same hash contract from the already-extracted representative video frame;
- `4051dd6a86664809fce98823806f707d0b2947ff` — formatting-only correction after the first video-hash CI run; exact executable revision used for the first target duplicate gate;
- `d211518580466c669d852166d3e361af105a60a6` — rewrite duplicate lookup so the source perceptual hash is a scalar subquery rather than a join variable;
- `82b86a1438068137b2c3e1bb8640f6656ca9e937` — add ordered partial `(perceptual_hash, post_id DESC)` lookup index and drop the now-redundant single-column hash index;
- `37564a7afa8b64d7729f4a9df0a2a1b58b49b69c` — lock the perceptual lookup migration contract;
- `2cef9a9d309639855a9e5b14cfa19191f40acbe5` — lock the scalar-source-hash SQL shape that the target recheck must validate.

CI:

- `94436f55d5c3b1a820ab25240e9e5925cd813b45`: run `37231221069`, **success**;
- `627f8b4302244f9e45b9b06092aadb0c2a84ebc7`: run `37231454607`, **failed only at Rust formatting** before later gates executed;
- `4051dd6a86664809fce98823806f707d0b2947ff`: run `37231518697`, **success**, including scoped v2 correctness, PostgreSQL-backed tests, `Verify target worker release build`, and clean tracked checkout;
- corrected query/index revision `2cef9a9d309639855a9e5b14cfa19191f40acbe5`: run `37237731218`, **success**, including scoped v2 correctness and clean tracked checkout.

Candidate contract:

- 64-bit horizontal difference hash / dHash;
- representative pixels are resized directly to 9x8 with the existing `image` crate's Triangle filter, converted to luma at that bounded size, then 64 left-vs-right comparisons are packed row-major into the stored bigint bit pattern;
- explicit `PERCEPTUAL_HASH_VERSION = 1`; output-affecting hash changes require a new processing/hash contract and incompatible versions must not be exact-compared;
- images hash the already-decoded, orientation-applied source before canonical output resize;
- videos hash the already-extracted bounded representative frame used for thumbnail generation;
- no second source decode and no additional full-resolution image copy solely for duplicate detection;
- successful current image/video processing returns `Some(perceptual_hash)`; legacy/no-hash rows may remain `NULL`;
- PostgreSQL remains authoritative; the hash is persisted only by the existing generation/source-digest-fenced publication transaction;
- retries compute the same deterministic hash and stale/lost ownership cannot publish a media row or release the post.

Duplicate semantics for this slice:

- **exact perceptual-hash equality only**; no Hamming-distance/ANN lookup yet;
- an exact match is only a duplicate **candidate**, not an automatic reject/merge/link decision;
- image/video cross-kind matches are allowed as candidates because both use the same representative-pixel contract;
- lookup excludes the source post, requires candidate media `processing_state = ready`, candidate post `release_state = released`, non-deleted post, and caller-supplied allowed content filters;
- default query limit 20, hard maximum 100, ordered by post ID descending;
- source `NULL` hash returns no candidates;
- visibility/moderation/release rules remain separate from detection and are not bypassed.

Dependency decision: do not add `image_hasher` for this slice. Its established gradient-hash behavior was evaluated, but pulling its wider transitive hashing/DCT dependency set was disproportionate when the current worker already owns the decoded `image` buffer and the required dHash operation is one bounded resize plus 64 comparisons. The algorithm/configuration is documented and locked by focused tests rather than relying on library defaults.

### Target duplicate gate reviewed

Evidence package: `m3-perceptual-duplicates-20261004T203424Z.zip`.

Uploaded archive SHA-256: `4544bcf2afdfdc11fcc53198be38b44de1da92505e0b0ce1ef3fe9de81707344`.

The archive contained **455 manifest-listed files and all 455 matched their SHA-256 values**. The tested baseline was `v2` at `ac980f99bf1706557a48eb420d4600b6d20a2f96`; the tested duplicate executable candidate was `4051dd6a86664809fce98823806f707d0b2947ff`.

Accepted processing/correctness evidence:

- three paired 20-job still-image rounds: median wall time **16.069143 -> 16.288370 s**, about **+1.36%**; median worker CPU time **15.17 -> 15.28 s** and mean CPU **93.236% -> 93.276%**; no meaningful RSS regression;
- three paired 20-job compatible-video rounds: median wall time **10.977987 -> 10.959615 s**, about **-0.17%**; CPU/RSS effectively unchanged;
- all six paired canonical/thumbnail output-byte comparisons passed;
- all warmup/measured jobs succeeded and observed max running jobs stayed one;
- repeated image/video processing produced deterministic non-null hashes and identical retry output bytes;
- exact cross-kind image/video candidate lookup worked; source `NULL`, unreleased, deleted, non-ready and disallowed-filter cases were excluded as required.

Decision for hashing/processing: **accept**. The measured still-image delta is small relative to existing encoding work, compatible-video cost is effectively unchanged, correctness passed, and the evidence does not justify an API coexistence rerun.

Representative query evidence for the original SQL was good: the 100k selective dataset used `media_phash_idx`; 30-sample median lookup was **0.171 ms** with p95 **0.533 ms**. No-candidate median was **0.125 ms**, p95 **0.154 ms**.

Pathological collision evidence rejected the original query shape:

- the deliberate bucket held source + 50,000 other media rows with the same hash;
- visible collision medians were about **19.0 ms** and restrictive-filter medians about **61.8-62.0 ms**;
- PostgreSQL chose a backward `media_pkey` scan to satisfy `ORDER BY m.post_id DESC LIMIT`, scanning tens of thousands of unrelated media rows instead of bounding work to the matching hash bucket;
- a temporary diagnostic `(perceptual_hash, post_id DESC)` index alone did **not** fix the plan or timings because the original `WITH source ... JOIN media m ON m.perceptual_hash = s.perceptual_hash` exposed the hash as a join variable rather than a fixed scalar value.

Decision for the original lookup: **reject**. A hot duplicate query must not turn a high-collision value into a near-whole-media-table ordered scan.

The corrected candidate keeps one round trip but changes the source hash to a scalar subquery and replaces the single-column partial hash index with `media_phash_post_idx ON media (perceptual_hash, post_id DESC) WHERE perceptual_hash IS NOT NULL`. The intended plan is a source-row PK lookup plus a candidate scan bounded to one perceptual-hash bucket, with post-ID order supplied by the same index. This correction is CI-green but is **not accepted until target `EXPLAIN (ANALYZE, BUFFERS)` confirms the intended bounded plan on both representative and 50,001-row collision fixtures**.

Do not rerun the processing-cost comparison: the correction changes only Go SQL/schema migration inputs, not hashing or worker processing. Do not rerun the 80,000-request API coexistence gate unless the focused DB recheck unexpectedly shows material continuous database load.

No public HTTP duplicate endpoint is added in this slice; the backend exposure remains an internal bounded service/store boundary until product/API visibility semantics need a route.

## Retained architecture / invariants

Keep unless new evidence justifies change:

- Go 1.25 + standard `net/http` + pgx/v5;
- PostgreSQL authoritative for application state and durable jobs;
- no Redis baseline dependency without measured need;
- Rust media worker;
- local NVMe media storage;
- immutable numeric relational IDs;
- post-ID cursor pagination, never OFFSET;
- real search lexer/parser/AST; parameterized SQL only;
- durable jobs use PostgreSQL ownership + generation fencing;
- filesystem/codec work stays outside DB transactions;
- stale work can never release a post or authoritatively complete a job;
- deterministic/idempotent no-overwrite filesystem effects for at-least-once work;
- one active media job per worker;
- one still-image/video-thumbnail AVIF encoder thread;
- production idle poll **500 ms**;
- production lease baseline **30 s**, heartbeat around one third of lease;
- PostgreSQL worker connect/statement budget `min(lease / 10, 3s)`, 1 ms floor;
- output-affecting media changes require explicit processing-version identity.

Do not add Redis wakeups, cgroups, CPU pinning, quotas, scheduler tuning or load admission from current evidence.

## Accepted still-image contract

Processing version 1 supports non-animated JPEG/PNG/WebP input.

Intentional terminal/out-of-scope inputs remain animated PNG/WebP, GIF and AVIF/HEIF input.

Accepted limits/output:

- max source dimension 16,384 px;
- max decoded pixels 80,000,000;
- decoder allocation hint 384 MiB;
- canonical max dimension 1,280 px without upscale;
- 256x256 center-crop AVIF thumbnail;
- `ravif` speed 10, main quality 75, thumbnail quality 60;
- deterministic versioned output keys;
- staged write + fsync + atomic no-overwrite hard-link publication;
- exact size/SHA verification on reuse;
- collision never overwrites existing bytes.

## Accepted compatible-MP4 contract

Processing version 1 video passthrough accepts only:

- MP4 container;
- exactly one video stream;
- H.264;
- `yuv420p` / `yuvj420p` 8-bit 4:2:0;
- AAC audio or no audio;
- positive finite duration;
- positive dimensions, max dimension 16,384 px, max frame pixels 80,000,000;
- sample aspect ratio 1:1;
- orthogonal display rotation; 90/270 degrees swap display dimensions.

Reject rather than silently transcode/mislabel:

- EBML/WebM for now;
- HEVC;
- 10-bit/other pixel formats;
- non-AAC audio;
- non-square pixels;
- conflicting/non-orthogonal rotation metadata;
- other formats requiring conversion.

Do **not** add a transcode fallback until a concrete required input/browser/product capability is identified and the exact output codec/container contract is defined.

Compatible MP4 publication:

- source bytes are not transcoded;
- canonical key uses post/source-digest/processing-version identity and `.mp4`;
- publication streams rather than buffering the entire video;
- bytes are re-sized/re-hashed during staging to detect mutation after initial verification;
- staging is fsynced and atomically linked without overwrite;
- identical existing output is verified/reused; conflicting output is terminal;
- canonical MP4 is durable before thumbnail extraction;
- one bounded cancellable FFmpeg subprocess extracts exactly one frame;
- extraction timeout 20 s, polling 20 ms, stdout capped 4 MiB, stderr bounded;
- FFmpeg decoder/filter/encoder thread limits are one where exposed;
- extracted frame reuses the still-image AVIF thumbnail encoder;
- canonical + thumbnail must exist durably before generation-fenced ready/release publication.

## Accepted target-host video gate

Exact tested feature revision: `e437e36194013579f3778292d81ddfe0ddf6a138`.

Evidence package: `m3-video-gate-20261004T182342Z.zip`.

Evidence identity caveat:

- local agent reported retained ZIP SHA-256 `c0da513144a14b0b709c180b7e4a3e1e3dc1847438b04a5fb5e825264f6f89d0`;
- uploaded archive analyzed in the primary session hashed to `10fe7cc9544ec6fb42b0493af367a796fde6b3e8dd7840200c1b290132c279ea`;
- uploaded `ZIP-MANIFEST.tsv` contained **523 entries** and all 523 matched their recorded SHA-256 values.

Keep both outer hashes in the record; do not claim they identify identical ZIP bytes unless the retained local file is rechecked later.

Key accepted evidence:

- functional compatible-MP4 job succeeded at attempt/generation 1 with exact source/canonical size and SHA;
- representative fixture 31,179,285 B; load fixture 121,948,657 B; long fixture 364,334,721 B;
- 200-job drain: **200/200 succeeded**, zero failures/stale jobs, all 200 filesystem validations passed, max running 1;
- drain throughput **1.751 jobs/s**, about **54.586 MB/s** source bytes;
- representative-drain worker CPU median **61.0%**, RSS median **18,986 KiB**;
- FFprobe median **56.076 ms**; one-frame thumbnail extraction median **86.267 ms**;
- observed process thread maxima: FFprobe **1**, FFmpeg **3**;
- forced 1 s lease observed **11 distinct increasing expiry values** while attempt/generation 1 remained stable and the job succeeded;
- SIGTERM during long work exited in **5.575 ms**, requeued pending with zero media rows and unreleased post; restart succeeded at attempt/generation 2;
- API coexistence: **80,000/80,000 requests succeeded**, zero errors; every loaded round had exactly one running video job before/after.

Median API baseline -> loaded:

| c | p95 | p99 | throughput |
| ---: | ---: | ---: | ---: |
| 1 | 2.219 -> 2.431 ms (**+9.55%**) | 2.404 -> 2.591 ms (**+7.78%**) | 562.45 -> 510.73 req/s (**-9.20%**) |
| 2 | 2.384 -> 2.544 ms (**+6.73%**) | 2.566 -> 2.783 ms (**+8.43%**) | 996.41 -> 920.75 req/s (**-7.59%**) |
| 4 | 2.590 -> 2.887 ms (**+11.49%**) | 2.811 -> 3.234 ms (**+15.03%**) | 1,764.62 -> 1,628.06 req/s (**-7.74%**) |
| 8 | 4.111 -> 5.246 ms (**+27.61%**) | 4.857 -> 6.364 ms (**+31.02%**) | 2,604.32 -> 2,233.88 req/s (**-14.22%**) |

Decision: **accept** the zero-transcode compatible-MP4 path and retain the simple one-job worker architecture. The c8 capacity cost is material and must remain visible in regression testing, but absolute latency stayed low, no HTTP errors occurred, memory use stayed small, and current evidence does not justify scheduler/isolation/admission infrastructure.

Evidence caveats:

- the shutdown sample did not have an already-durable canonical MP4 at signal time; durable-output retry/reuse is covered by CI/integration tests rather than this target shutdown sample;
- requested shared-host evidence was not captured as three separate raw before/during/after snapshots. Measurement-window resource samples plus pre-/post-cleanup service observations show Wallium and the Ginbar CI VM remained running; future target gates must capture the three explicit snapshots.

Full accepted measurements and historical comparisons live in [`PERFORMANCE.md`](PERFORMANCE.md).

## Hermetic target worker build rule

The first target-video attempt was blocked because a plain Rust environment lacked NASM for `rav1e`. That attempt produced no performance result.

Permanent rule:

```bash
bash scripts/v2-worker-build.sh <output-path-outside-repository>
```

The script uses Rust 1.99 and installs NASM only inside an ephemeral compiler container; repository source is read-only and Cargo uses `--release --locked`. Local/target agents must not install Rust/NASM packages on the host or invent a different worker build command.

A worker target handoff is blocked unless exact-revision CI is green including `Verify target worker release build`.

## Benchmark reproducibility note

The standard around-post benchmark endpoint is `/api/v2/posts/50000/around?radius=30`. The old seed formula made post 50000 filter 2, causing the default SFW request to fail. The accepted video gate changed only that disposable DB row to filter 0.

Commit `4830d137d59b57ea4abcd2092e3c9010f7e2683e` codifies post 50000 as SFW in `src/backend/v2/bench/seed.sql`, matching the measured target state and removing the need for future ad-hoc DB mutation.

## Remaining M3 work

- run the focused target query-plan recheck for the corrected perceptual duplicate lookup and integrate only if it is bucket-bounded under both representative and high-collision data;
- regeneration;
- progress/status UI;
- decide whether broader video transcoding is actually required only after concrete input/product evidence defines the needed codec/container contract;
- exact WebM/EBML acceptance remains deferred until container distinction and browser-compatibility behavior are explicit.

Deferred operational cleanup:

- ingestion-source and processed-staging orphan reconciliation/janitor;
- deployment UID/GID/media-storage permissions;
- stronger `openat2`/`O_NOFOLLOW` hardening only if the local media-tree threat model changes;
- v1-v2 apples-to-apples benchmark once equivalent end-to-end behavior exists.

## Local-agent rule

Use local/server agents for browser/DevTools, SSH, real PostgreSQL, target-server benchmarks, temporary deployments or unavailable toolchains.

Before handoff, the applicable CI for the exact revision must be green. Default is read-only/execution-only; agents must not modify tracked source/docs/config, branches, deployments or persistent state without explicit approval. Return one retained evidence ZIP with exact SHA, commands, raw outputs, environment, measurements, errors and cleanup/restoration evidence.

## Single best next task

Run a **read-only/execution-only focused target PostgreSQL recheck** for exact executable revision `2cef9a9d309639855a9e5b14cfa19191f40acbe5`: apply migrations through `004_media_perceptual_hash_lookup.sql` in a disposable PostgreSQL instance, repeat the 100k representative/no-candidate cases and the exact 50,001-row high-collision visible/restrictive-filter cases, and capture `EXPLAIN (ANALYZE, BUFFERS, VERBOSE, SETTINGS)` plus repeated lookup latency. Accept only if work is bounded to the matching perceptual-hash bucket rather than reverting to an ordered scan of unrelated `media` rows. Do not rerun worker processing or the 80,000-request API coexistence gate for this recheck.
