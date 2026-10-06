# M3 ingestion boundary

This package stages upload or URL-import bytes before media codec work. The v2 API wires
it to authenticated posting routes while keeping HTTP parsing, session identity, and
same-origin policy outside this package. Callers supply the authenticated numeric user
ID; ingestion remains independent of credential type.

## Ordering and durability

1. Acquire one bounded ingestion slot.
2. Stream the input to local source storage outside any database transaction.
3. Enforce the configured staging deadline and byte limit while hashing the bytes.
4. `fsync` the file and publish it under a random relative `sources/...` storage key.
5. In one short PostgreSQL transaction, atomically create the unreleased post,
   `media_sources` row, and initial durable media job.

A definite database failure removes the staged object using a cleanup context that
survives request cancellation. A non-rollback commit error is treated as ambiguous and
keeps the staged object; deleting it could otherwise leave a successfully committed
PostgreSQL row pointing at a missing file.

A process crash between publishing a source object and starting/finishing the database
transaction can leave an unreferenced source object. That failure mode is safe for
referenced data but requires an orphan-reconciliation/janitor path before production
rollout.

Duplicate bytes intentionally receive distinct source keys. Exact/perceptual duplicate
policy belongs to media processing; ingestion must not silently alias posts or delete a
source another workflow might need.

## URL imports

`HTTPURLFetcher` accepts only `http` and `https`, does not use environment HTTP proxies,
validates every redirect, resolves the destination itself, and refuses loopback,
private, link-local, multicast, shared-address, benchmark, and other explicitly blocked
address ranges before dialing. The local stager independently enforces the byte cap, so
incorrect or compressed `Content-Length` values cannot bypass the source-size bound.
