# Ginbar v2 native service limits

These are standalone **systemd service templates** for the v2 native Go API and Rust media worker. They are not installed by any tracked deployment script. The live legacy `wallium.service` Docker Compose deployment remains untouched. This slice assumes a Linux host with systemd and cgroup v2.

Prerequisites for a future reviewed deployment:

- dedicated `ginbar` user/group with read/write access to the shared `/srv/ginbar/media` source and processed-media tree;
- built Go API installed at `/usr/local/bin/ginbar-api-v2` (from `src/backend/v2/cmd/api`), and Rust worker at `/usr/local/bin/ginbar-worker-v2` (from `src/worker/v2`); the worker runtime must also have its existing FFmpeg/FFprobe dependencies;
- separate `/etc/ginbar/v2/api.env` and `worker.env` containing the required `DATABASE_URL` credentials; restrict file permissions and do not commit credentials;
- nginx v2 serves processed media from `/srv/ginbar/media/media` and proxies API to localhost port 8080; do not expose the ingestion `sources` directory.
- The Go API distrusts all `X-Forwarded-Proto` headers by default. The supplied native API unit explicitly sets `GINBAR_TRUST_LOOPBACK_PROXY=true` because the accepted nginx upstream uses `127.0.0.1:8080`; the API only trusts a single canonical `http` or `https` value from an immediate loopback TCP peer. Keep the API listener loopback-only when enabling this switch. Other local processes with access to the loopback API socket are part of this host-local trust boundary; do not enable the flag on a publicly reachable listener.

| Service | Open files per process | Tasks per service cgroup | Cgroup memory maximum |
| --- | ---: | ---: | ---: |
| Go API | 4,096 | 256 | 1 GiB |
| Rust media worker | 1,024 | 128 | 4 GiB |

`TasksMax` includes threads and subprocesses, including the worker's FFmpeg/FFprobe children. `MemoryMax` is a hard **cgroup** bound, not just a Rust/Go heap setting; worker child processes count toward it. `OOMPolicy=kill` tears down the group on OOM; `Restart=on-failure`, a 10-second delay and 5 starts per 5 minutes bound restart churn. `KillMode=control-group` prevents orphan children; the API has 30 seconds and the worker 90 seconds to stop before a forced kill.

These initial values are containment policies, **not workload-derived capacity recommendations**. A legitimate large media job may exceed the worker's 4 GiB cgroup cap and be retried; record peak cgroup memory, threads and FD usage on an isolated production-class host before applying to the live service. Durable job leases/fencing allow recovery from a terminated worker without publishing stale results.

Run `bash scripts/v2-service-limits-test.sh` **only on a disposable/isolated CI VM**: it builds an ephemeral Docker systemd environment with privileged container capabilities, verifies unit syntax and real cgroup/FD properties, checks both service startup paths using small stand-in executables, and proves restart on a forced API failure. It does **not** validate production binary startup or representative peak usage; the existing PostgreSQL-backed application CI runs separately, and a dedicated isolated real-binary acceptance gate must precede deployment. No host systemd unit is installed, enabled, modified or reloaded by the fixture.


## Operational health/readiness boundary

The service manager remains the liveness authority for the native processes. The API separately exposes host-local `/healthz` (process liveness) and `/readyz` (PostgreSQL readiness) on its loopback listener. The Rust worker deliberately has no additional listening socket: invoke `ginbar-worker-v2 ready` with the same `DATABASE_URL` environment as the worker to perform a one-shot, one-connection, 1 second PostgreSQL readiness probe.

A PostgreSQL outage therefore does not require killing a live worker: the production runner keeps its existing bounded retry/recovery loop, while the readiness command reports failure until the dependency recovers. No probe writes durable application state, emits credentials, or introduces a monitoring daemon, metrics store, dashboard, or alerting service.

## Disposable deployment/update-and-rollback dry-run (not an installer)

Run `bash scripts/v2-deploy-dry-run-test.sh` only on an isolated Docker-capable CI VM. The fixture creates a temporary privileged Docker **systemd** container with its own cgroup namespace, without publishing ports, bind-mounting host paths or touching host units. It installs copies of the exact API/worker unit files and `nginx/v2/nginx.conf` **inside the container only**. The API/worker executables are small stand-ins: this is an orchestration and serving-boundary test, not a production Go/Rust or PostgreSQL test.

The exercised protocol uses an immutable per-release directory and a single atomically replaced `releases/current` symlink. API binary, worker binary and frontend symlink all refer through it; durable media and credentials stay outside release directories. Under a lock, the container-local updater rejects incomplete releases and unsafe identifiers, switches the symlink, restarts both systemd services, verifies and reloads nginx, and probes API direct readiness, proxied HTTPS API, worker's launched release and frontend. On failed activation it restores the previous symlink, restarts both services, reloads nginx, probes the previous release, and exits nonzero. Failed rollback exits nonzero for operator intervention.

The sequence verifies initial release A, successful update to B, intentionally unready candidate rejected and restored to B, path traversal/missing candidate rejection without changing B, and explicit rollback to A. Each stage checks that processed media remains stable and ingestion sources remain denied. On exit the fixture removes its container, image and temporary build context.

**Never copy this fixture updater to a live host.** It uses mock executables, a self-signed certificate and mocked readiness. Real deployment requires separately reviewed artifacts, database migration/backup compatibility, drain and traffic cutover, worker leases, secrets/TLS, host permissions, and operator recovery. The dry-run does not establish zero downtime, real binary health, migration reversibility or production suitability.


## Operator-invoked PostgreSQL logical backup (not production-authorized)

With PostgreSQL 17-compatible client tools and a securely provisioned libpq credential (prefer restricted `PGPASSFILE` over `PGPASSWORD`), set `PGHOST`, `PGPORT`, `PGUSER`, and the **plain-name** `PGDATABASE`, then invoke `bash scripts/v2-pg-backup.sh /backup-volume/unique-snapshot-name`. The destination parent must already exist and the destination directory must not. The command creates a private same-filesystem staging directory, uses `pg_dump -Fc`, validates `pg_restore --list`, and publishes a single directory containing `backup.dump` and `metadata.txt` with SHA-256, size, UTC creation time and database/tool versions. Both files are mode 0600 and the directory is 0700. Any failed command cleans incomplete output. Verify artifact metadata and restore a copy into a newly created disposable database before treating a backup as recoverable. Secure the credential file and any backup storage independently; neither logs nor metadata include connection secrets.

For the disposable PostgreSQL 17.11 cold-recovery drill run `GINBAR_PG_BACKUP_IMAGE=postgres:17.11-alpine bash scripts/v2-pg-recovery-test.sh` only on an isolated Docker-capable host. It exercises the operator command, checks source non-mutation and archive integrity, then destroys the disposable source and reconstructs into a fresh database. No real deployment or production data may be targeted by the fixture.

**Limits:** This is logical cold recovery, not WAL archiving, PITR, replication or a scheduling/retention/off-host-storage system. Effective RPO is the age of the latest independently durable backup; a local copy does not survive host loss. Large-database backup time, disk footprint and production IO impact remain unmeasured. Deployment rollback across schema migrations and any RTO commitment require separate review. The PostgreSQL backup role may be privileged; restrict its credentials, host access and artifact directory permissions.
