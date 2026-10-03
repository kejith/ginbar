# Ginbar v2

Ginbar v2 is a performance-first clean-slate rewrite of the authenticated pr0gramm-style media board that previously lived on `master`.

## Branches

- `master` is legacy/v1 and is read-only for rewrite work.
- `v2` is the rewrite integration branch.
- Feature work should branch from the current `v2`, be validated/benchmarked, then fast-forward back into `v2`.

Do not use `master` as an implementation base for v2 work.

## Current status

- M1 board performance prototype: complete.
- M2 fresh PostgreSQL schema + core Go API: complete.
- M3 media pipeline: in progress.
- Durable media-job ownership/recovery, upload/URL ingestion, source verification, fenced publication, and still-image AVIF/thumbnail processing are integrated.
- Next M3 slice: long-running worker loop with lease renewal, cancellation/lost-ownership handling, graceful shutdown, and continuous-runner API interference benchmarks.

The authoritative resume point is [`docs/v2/STATE.md`](docs/v2/STATE.md).

## Rewrite layout

- `src/frontend/` — accepted SolidJS + TypeScript + Vite board prototype from M1.
- `src/backend/v2/` — clean v2 Go API, schema, migrations, query/search code, and benchmarks.
- `src/worker/v2/` — clean v2 Rust media worker and processing pipeline.
- `docs/v2/` — rewrite plan, current handoff state, milestone records, and benchmark summaries.

Legacy source remains in adjacent non-v2 paths as reference material while the rewrite is incomplete. Do not extend legacy code for v2 features.

## Validation commands

Frontend:

```sh
cd src/frontend
npm ci
npm run validate
```

Backend:

```sh
cd src/backend/v2
go test ./...
```

Worker:

```sh
cargo test --locked --manifest-path src/worker/v2/Cargo.toml
cargo clippy --locked --manifest-path src/worker/v2/Cargo.toml --all-targets -- -D warnings
```

Target-server measurements are authoritative for backend/media performance. Browser/frontend timings belong to the client machine.

## Documentation

Start with [`docs/v2/README.md`](docs/v2/README.md). The important files are:

- [`STATE.md`](docs/v2/STATE.md) — current integrated state and single next task.
- [`PLAN.md`](docs/v2/PLAN.md) — rewrite architecture, milestone gates, and workflow.
- [`PERFORMANCE.md`](docs/v2/PERFORMANCE.md) — consolidated accepted measurements and performance decisions.
- [`M1.md`](docs/v2/M1.md) / [`M2.md`](docs/v2/M2.md) — completed milestone records.

## Legacy root tooling

Some root Docker/deployment scripts and non-v2 source directories still describe or support the legacy application. They are retained as reference until later production-hardening/deployment work replaces them. They are not authoritative descriptions of the v2 runtime architecture.
