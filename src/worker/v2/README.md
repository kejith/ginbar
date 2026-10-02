# Ginbar v2 media worker boundary

This crate is the first M3 slice. It implements durable PostgreSQL media-job ownership and fencing only. It does **not** process images or video yet.

## State contract

`media_jobs.state` keeps the M2 numeric values:

- `0`: pending;
- `1`: running with an active lease;
- `2`: succeeded;
- `3`: failed.

Each successful claim increments both `attempts` and `lease_generation`. `claimed_by` is the explicit worker identity. Completion, failure, and lease renewal require the same worker ID, lease generation, and an unexpired lease. A stale worker therefore cannot commit after another worker reclaims the job.

Lifecycle mutations first lock the exact owned row and only then evaluate expiry against `clock_timestamp()`. A mutation that starts before expiry but waits on another row lock until after expiry is rejected.

If the final allowed attempt dies, the next bounded claim pass converts the expired row to terminal failure instead of starting an attempt beyond `max_attempts`.

Lease acquisition and renewal accept a `NonZeroU64` millisecond duration. Zero and sub-millisecond lease durations are therefore not representable at the `JobStore` API boundary.

## Retry and idempotency contract

Retryable failures return the row to `pending` with a caller-supplied `available_at` delay until `max_attempts` is reached. Non-retryable failures become terminal immediately.

Processing is deliberately outside the claim transaction. A process can die after a durable side effect but before recording completion, so future processors must be idempotent. Durable media side effects must use deterministic keys/upserts or an equivalent atomic publication rule. A lease prevents concurrent ownership; it does not provide exactly-once external side effects.

No database transaction remains open while media processing runs. Claim/renew/complete/fail are each one short atomic SQL statement. If a process is cancelled after a claim commits but before it observes the result, the lease expires and another worker can reclaim the job.

Claims order by runnable timestamp first, then priority and ID. This keeps the hot query aligned with one partial expression index and prevents future-scheduled high-priority rows from causing an unbounded readiness scan. Priority is therefore a tie-breaker among similarly ready work, not a strict global preemption rule.

## Concurrency and polling

The initial worker concurrency target is one job at a time. This crate intentionally contains no production polling loop yet, so it cannot create idle polling load before real processing exists. Redis is not required.

The `claim-once` binary command is a boundary/crash-recovery probe: it claims at most one job and exits without completing it, intentionally leaving the lease to expire.

```sh
DATABASE_URL='postgres://...' \
GINBAR_WORKER_ID='m3-probe-1' \
GINBAR_WORKER_LEASE_MS=1000 \
cargo run --manifest-path src/worker/v2/Cargo.toml -- claim-once
```
