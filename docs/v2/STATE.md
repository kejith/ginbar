# Ginbar v2 state / handoff

Last updated: 2026-10-05
Phase: **M3 media pipeline — perceptual duplicate detection accepted; integration into `v2` in progress**
Integration branch: `v2`
Legacy branch: `master` (read-only for rewrite work)

Read this file first. Use [`PLAN.md`](PLAN.md) for stable milestone/architecture rules and [`PERFORMANCE.md`](PERFORMANCE.md) for accepted historical benchmark detail. Do not use chat history as project memory.

## Current rewrite status

- M1 board performance prototype: **complete and integrated**.
- M2 fresh PostgreSQL schema + core Go API: **complete and integrated**.
- M3 durable PostgreSQL media jobs, ingestion/source verification, deterministic publication and generation fencing: **integrated**.
- M3 still-image JPEG/PNG/WebP -> deterministic AVIF canonical + thumbnail processing: **accepted and integrated**.
- M3 production worker runner, lease renewal, cancellation/recovery and hermetic release build: **accepted and integrated**.
- M3 compatible-MP4 H.264/AAC zero-transcode publication + deterministic AVIF thumbnail: **accepted and integrated**.
- M3 perceptual duplicate detection: **accepted after two target gates; ready to integrate into `v2`**.
- M3 remains **in progress** after duplicate integration: regeneration and progress/status UI remain. Broader video transcoding is still deferred until a concrete product/input requirement defines the codec/container contract.

## Perceptual duplicate detection — accepted contract

Feature branch: `astra/m3-perceptual-duplicates`, originally branched from `v2` at `ac980f99bf1706557a48eb420d4600b6d20a2f96`.

Exact executable candidate validated on target and accepted:

`2cef9a9d309639855a9e5b14cfa19191f40acbe5`

Applicable CI:

- workflow run `37237731218`: **success** on exact SHA `2cef9a9d309639855a9e5b14cfa19191f40acbe5`;
- scoped v2 correctness gate passed;
- PostgreSQL-backed tests passed;
- tracked checkout remained clean.

Hash contract:

- explicit `PERCEPTUAL_HASH_VERSION = 1`;
- 64-bit horizontal difference hash / dHash;
- representative pixels resize directly to 9x8 with the existing `image` crate Triangle filter, convert to luma, then pack 64 left-vs-right comparisons row-major into PostgreSQL `bigint`;
- still images hash the already-decoded/orientation-applied source before canonical resize;
- videos hash the already-extracted bounded representative frame used for thumbnail generation;
- no second source decode and no additional full-resolution copy solely for hashing;
- successful current image/video processing produces a non-null hash;
- output-affecting hash changes require a new explicit hash/processing contract; incompatible versions must not be exact-compared;
- PostgreSQL publication remains generation/source-digest/lease fenced, so stale work cannot authoritatively publish a hash or release a post.

Duplicate semantics:

- exact perceptual-hash equality only; no Hamming-distance/ANN lookup in this slice;
- a match is a **candidate**, never an automatic reject/merge/link decision;
- cross-kind image/video matches are allowed because both use the same representative-pixel contract;
- source post is excluded;
- candidate media must be ready;
- candidate post must be released and non-deleted;
- caller-supplied content filters remain enforced;
- default limit 20, hard maximum 100;
- results order by post ID descending;
- source `NULL` hash returns no candidates;
- no public HTTP duplicate endpoint yet; the boundary remains internal until product/API visibility semantics require exposure.

Accepted SQL/index shape:

- source perceptual hash is resolved as a scalar subquery / PostgreSQL InitPlan;
- candidate access is constrained by equality on that scalar hash;
- `media_phash_post_idx ON media (perceptual_hash, post_id DESC) WHERE perceptual_hash IS NOT NULL` supplies both bucket selection and result order;
- the old single-column `media_phash_idx` is removed by migration `004_media_perceptual_hash_lookup.sql`;
- visibility checks remain against authoritative `posts` rows rather than denormalizing release/deletion/content-filter state into `media`.

## Accepted target evidence

### Processing/correctness gate

Evidence package: `m3-perceptual-duplicates-20261004T203424Z.zip`

Uploaded SHA-256:
`4544bcf2afdfdc11fcc53198be38b44de1da92505e0b0ce1ef3fe9de81707344`

Manifest: **455/455 entries verified**.

Tested executable SHA: `4051dd6a86664809fce98823806f707d0b2947ff` before the later SQL/index-only correction.

Accepted processing evidence:

- still image, three paired 20-job rounds: median wall time **16.069143 -> 16.288370 s**, about **+1.36%**;
- compatible video, three paired 20-job rounds: median wall time **10.977987 -> 10.959615 s**, about **-0.17%**;
- CPU/RSS showed no meaningful regression;
- all paired canonical/thumbnail byte-equivalence checks passed;
- all measured jobs succeeded and max running jobs remained one;
- repeated image/video processing produced deterministic non-null hashes;
- cross-kind exact-match and visibility/null-hash correctness checks passed.

Decision: **accept hashing/processing**. No additional 80,000-request API coexistence rerun is justified by the measured hash cost.

That first gate rejected the original duplicate SQL under a 50,001-row same-hash collision bucket because PostgreSQL used backward `media_pkey` access and scanned large ranges of unrelated media to satisfy ordering. A diagnostic composite index alone did not fix that join-shaped SQL.

### Corrected query-plan gate

Evidence package: `m3-perceptual-query-recheck-20261004T220113Z.zip`

Uploaded SHA-256:
`709397fb3cd88b12f9017a083bbf0a2d513fd73b2d4170c6caf2164d761f0e06`

Manifest: **252/252 entries verified**. Exact tested executable SHA: `2cef9a9d309639855a9e5b14cfa19191f40acbe5` in a detached worktree. The later docs-only feature tip was not used for execution.

PostgreSQL 17.11, 100k standard seed plus representative and deliberate 50,001-row same-hash fixtures:

| case | candidate index entries visited | median | p95 |
| --- | ---: | ---: | ---: |
| selective L20 | 4 | **0.163 ms** | **0.207 ms** |
| selective L100 | 4 | **0.136 ms** | **0.192 ms** |
| no candidates L20 | 1 | **0.142 ms** | **0.261 ms** |
| visible collision L20 | 21 | **0.230 ms** | **0.327 ms** |
| visible collision L100 | 101 | **0.481 ms** | **0.545 ms** |
| restrictive collision L20 | 49,921 | **64.765 ms** | **71.978 ms** |
| restrictive collision L100 | 50,001 | **64.572 ms** | **67.525 ms** |

Plan evidence:

- source lookup appears as `InitPlan 1` using `media_pkey` for exactly one source row;
- candidate scans use `media_phash_post_idx` with equality on the InitPlan hash;
- no explicit sort node appears; the composite index supplies descending post ID order;
- visible collision stops after 21/101 bucket entries for limits 20/100;
- restrictive collision scans most/all of the **matching hash bucket** because only 100 low-ID posts are visible;
- no unrelated-hash/global backward `media_pkey` candidate scan remains;
- all DB-only correctness spot checks passed.

Decision: **accept corrected query/index shape**. The remaining ~65 ms restrictive case is a deliberately pathological 50k-collision bucket whose cost is now bounded by the matching bucket and authoritative visibility checks. Do not denormalize post visibility or add another cache/index solely for this synthetic case. Revisit only if real production collision distributions or a future high-rate public/ingestion duplicate lookup demonstrate material load.

The target agent's canonical local checkout was clean but had a stale local `v2` ref at old commit `38c515afd2862cb6a3b6a6c677c9b34fa210b662`. This did not affect the gate: exact baseline/candidate detached worktrees were used. GitHub `v2` remained `ac980f99bf1706557a48eb420d4600b6d20a2f96` when acceptance was reviewed.

## Retained architecture / invariants

Keep unless new evidence justifies change:

- nginx for TLS/static/media/reverse proxy;
- Go 1.25 + standard `net/http` + pgx/v5;
- PostgreSQL authoritative for application state and durable jobs;
- no Redis baseline dependency without measured need;
- Rust media worker;
- local NVMe media storage;
- immutable numeric relational IDs; never usernames as foreign keys;
- post-ID cursor pagination, never OFFSET;
- real search lexer/parser/AST and parameterized SQL only;
- durable jobs use PostgreSQL ownership + generation fencing;
- filesystem/codec work stays outside DB transactions;
- stale work cannot release a post or authoritatively complete a job;
- deterministic/idempotent no-overwrite filesystem effects for at-least-once work;
- one active media job per worker;
- one still-image/video-thumbnail AVIF encoder thread;
- production idle poll 500 ms;
- production lease baseline 30 s, heartbeat around one third of lease;
- PostgreSQL worker connect/statement budget `min(lease / 10, 3s)`, 1 ms floor;
- output-affecting media changes require explicit processing-version identity.

Do not add Redis wakeups, cgroups, CPU affinity, quotas, scheduler tuning or load admission from current evidence.

## Accepted media scope

Still-image processing version 1 supports non-animated JPEG/PNG/WebP -> AVIF canonical + 256x256 AVIF thumbnail with deterministic durable publication. Animated PNG/WebP, GIF and AVIF/HEIF input remain out of scope for this contract.

Compatible-MP4 processing version 1 accepts MP4 with one H.264 8-bit 4:2:0 video stream, AAC or no audio, square pixels and supported orthogonal rotation. It publishes source bytes without transcoding plus one deterministic AVIF thumbnail. HEVC, 10-bit/other pixel formats, non-AAC audio, non-square pixels and unsupported/conflicting rotation are rejected rather than silently transcoded or mislabeled.

Do not add broader video transcoding until a concrete required input/browser/product capability defines the exact output codec/container contract.

## Remaining M3 work

- regeneration;
- progress/status UI;
- decide whether broader video transcoding is required only from concrete product/input evidence;
- exact WebM/EBML acceptance remains deferred until container distinction and browser-compatibility behavior are explicit.

Deferred operational cleanup:

- ingestion-source and processed-staging orphan reconciliation/janitor;
- deployment UID/GID/media-storage permissions;
- stronger `openat2`/`O_NOFOLLOW` hardening only if the local media-tree threat model changes;
- v1-v2 apples-to-apples benchmark once equivalent end-to-end behavior exists.

## Local-agent rule

Use local/server agents for browser/DevTools, SSH, real PostgreSQL, target-server benchmarks, temporary deployments or unavailable toolchains. Before handoff, exact-revision applicable CI must be green. Default is read-only/execution-only; agents must not modify tracked source/docs/config, branches, deployments or persistent state without explicit approval. Return one retained evidence ZIP with exact SHA, commands, raw outputs, environment, measurements, errors and cleanup/restoration evidence.

## Single best next task

After fast-forward integration of the accepted duplicate slice into `v2`, begin the **M3 regeneration contract** from current `v2`: define the smallest durable regeneration workflow that reuses the existing processing-version identity, PostgreSQL job ownership/generation fencing and deterministic no-overwrite outputs without creating a second media-processing path. Inspect current schema/job/publication boundaries first, then implement and validate the minimal coherent slice.