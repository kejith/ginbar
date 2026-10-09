# M7: Forward-only v2 schema expansion and deployment compatibility

This is a **bounded test and policy** for future v2 PostgreSQL changes. The
existing release-switch fixture only rolls application release pointers; neither
the release switch nor the logical backup/cold restore drills reverse committed
SQL migrations.

## Expand/contract policy

1. **Expand first.** Publish new optional schema elements before deploying any
   binary that depends on them. Prefer nullable columns. A non-null addition
   needs a simple non-NULL literal default; more complex backfills require a
   separate, explicitly reviewed migration plan.
2. **Keep existing reads and writes stable.** Specify column names in inserts
   and queries. Avoid `SELECT *` and positional result decoding across schema
   changes. Consider old and new writers, default expressions, constraints,
   locks, long-running queries, indexes and query plans.
3. **Use a compatibility window.** During release N→N+1, ensure both binaries
   can use the expanded schema before switching pointers. Prove relevant
   existing read/write paths on both schemas, and separately test a real N-1
   executable before asserting genuine binary backward compatibility.
4. **Contract later.** Remove deprecated columns, constraints or semantics
   only in a subsequent independently reviewed release once every retained
   rollback target no longer depends on them. Taking an application pointer
   back does not revert a completed database migration.
5. **Forward-only is not reversible.** Do not add Down migrations, automatic
   downgrade calls or implicit production DDL to a release rollback.
   Restore-from-backup is a separate, destructive operational recovery choice.

## Guard limitations

`python3 scripts/v2-migration-guard.py --base BASE_SHA` inspects newly added
SQL files in `src/backend/v2/internal/schema/migrations` (based on the Git
diff), and refuses mutations/deletions of previously committed migrations.
It currently **only** permits constrained
`ALTER TABLE [public.]table ADD COLUMN col type` statements with supported
simple data types, an optional NULL marker, or a simple literal DEFAULT with
optional NOT NULL. DROP, ALTER TYPE, mandatory additions without safe literal
defaults, and all otherwise unsupported DDL fail closed with a rejection
diagnostic. The rule is intentionally conservative: even safe CREATE TABLE /
CREATE INDEX / complex schema changes need a separately reviewed guard update.

The guard is **not a PostgreSQL SQL parser** and is **not sufficient** to
establish semantic compatibility, safe defaults for every workload, lock cost,
index performance, cross-version correctness, or migration downgrade safety.
Its recognized syntax is a narrow allowlist. Before operationally adopting
new migrations, require code review, real-executable version-skew tests and
applicable production safety approvals.

## Disposable compatibility matrix

`bash scripts/v2-migration-compat-test.sh` uses only an isolated
PostgreSQL 17.11 Docker container. It applies every embedded v2 migration
in deterministic filename order into two separate disposable databases. It
seeds existing board relations (users, credentials, posts, tags, comments and
votes), compares explicit-column snapshots/sequence state, and exercises
FK-linked transactional writes and reads on both the baseline and a
synthetically expanded schema.

The synthetic addition is
`scripts/testdata/v2-migration-compat/010_add_nullable.sql`. It is **not**
under the embedded migration tree and must never be packaged as a production
migration. Negative fixtures exercise DROP TABLE, DROP COLUMN, ALTER TYPE and
an unsafe non-nullable addition; the guard rejects them without running DDL.

The matrix's third path is a **simulated pre-addition client SQL projection**,
not an actual historical N-1 binary. The existing general backend correctness
suite exercises backend code separately, but this matrix does not run the
compiled Go API or Rust worker against both schemas. Therefore its PASS means
only that the sampled explicit-column board operations remain valid across this
**test-only nullable addition**. It cannot establish all live API paths or any
general N/N-1 version-skew guarantee.

No production database, host or real user data may be targeted. No PITR, WAL
archive, migration rollback, scheduler, retention, RPO/RTO or performance
budget is established by this fixture.
