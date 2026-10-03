# Ginbar v2 documentation

This directory is the authoritative documentation set for the clean-slate rewrite on branch `v2`.

## Read order

1. [`STATE.md`](STATE.md) — current integrated state, invariants, unresolved items, and the single next task.
2. [`PLAN.md`](PLAN.md) — architecture, product requirements, milestone gates, and engineering workflow.
3. [`PERFORMANCE.md`](PERFORMANCE.md) — consolidated accepted benchmark results and performance decisions.
4. Completed milestone records as needed: [`M1.md`](M1.md) and [`M2.md`](M2.md).

Historical execution runbooks are retained under [`archive/`](archive/) for reproducibility but are not active instructions.

## Milestone status

| Milestone | Status | Result |
| --- | --- | --- |
| M1 board benchmark | Complete | SolidJS accepted; inline-row board model validated through 10,000 retained fake posts. |
| M2 schema + core Go API | Complete | Fresh PostgreSQL schema, cursor feed/search/around endpoints, bounded query plans, shared-host load gate. |
| M3 media pipeline | In progress | Durable jobs, ingestion/enqueue, source verification, fenced publication, and still-image AVIF/thumbnail processing integrated. Production runner/video/etc. remain. |
| M4 core product | Not started | Connect real auth/board/search/votes/tags/comments/profiles after M3 gate. |
| M5 moderation/admin/imports | Not started | Later. |
| M6 private messages | Not started | Later. |
| M7 production hardening | Not started | Later. |

## Source boundaries

- `src/frontend/` is the accepted v2 board/frontend prototype.
- `src/backend/v2/` is the clean v2 backend.
- `src/worker/v2/` is the clean v2 media worker.
- Adjacent legacy source remains reference-only until the rewrite no longer needs it.

`master` is legacy/v1 and must not be modified by rewrite work.

## Documentation rules

- Keep `STATE.md` operational and short. Do not turn it back into a chronological benchmark log.
- Put stable benchmark numbers and accepted performance decisions in `PERFORMANCE.md`.
- Put enduring architecture/product requirements in `PLAN.md`.
- Keep milestone-specific implementation history in `M1.md`, `M2.md`, and future milestone records.
- Move obsolete execution prompts/runbooks to `archive/` rather than leaving them beside active handoff docs.
- Do not claim v2 is faster than v1 without an apples-to-apples benchmark. Current evidence primarily measures v2 itself and within-v2 optimizations.
