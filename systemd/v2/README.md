# Ginbar v2 native service limits

These are standalone **systemd service templates** for the v2 native Go API and Rust media worker. They are not installed by any tracked deployment script. The live legacy `wallium.service` Docker Compose deployment remains untouched. This slice assumes a Linux host with systemd and cgroup v2.

Prerequisites for a future reviewed deployment:

- dedicated `ginbar` user/group with read/write access to the shared `/srv/ginbar/media` source and processed-media tree;
- built Go API installed at `/usr/local/bin/ginbar-api-v2` (from `src/backend/v2/cmd/api`), and Rust worker at `/usr/local/bin/ginbar-worker-v2` (from `src/worker/v2`); the worker runtime must also have its existing FFmpeg/FFprobe dependencies;
- separate `/etc/ginbar/v2/api.env` and `worker.env` containing the required `DATABASE_URL` credentials; restrict file permissions and do not commit credentials;
- nginx v2 serves processed media from `/srv/ginbar/media/media` and proxies API to localhost port 8080; do not expose the ingestion `sources` directory.

| Service | Open files per process | Tasks per service cgroup | Cgroup memory maximum |
| --- | ---: | ---: | ---: |
| Go API | 4,096 | 256 | 1 GiB |
| Rust media worker | 1,024 | 128 | 4 GiB |

`TasksMax` includes threads and subprocesses, including the worker's FFmpeg/FFprobe children. `MemoryMax` is a hard **cgroup** bound, not just a Rust/Go heap setting; worker child processes count toward it. `OOMPolicy=kill` tears down the group on OOM; `Restart=on-failure`, a 10-second delay and 5 starts per 5 minutes bound restart churn. `KillMode=control-group` prevents orphan children; the API has 30 seconds and the worker 90 seconds to stop before a forced kill.

These initial values are containment policies, **not workload-derived capacity recommendations**. A legitimate large media job may exceed the worker's 4 GiB cgroup cap and be retried; record peak cgroup memory, threads and FD usage on an isolated production-class host before applying to the live service. Durable job leases/fencing allow recovery from a terminated worker without publishing stale results.

Run `bash scripts/v2-service-limits-test.sh` **only on a disposable/isolated CI VM**: it builds an ephemeral Docker systemd environment with privileged container capabilities, verifies unit syntax and real cgroup/FD properties, checks both service startup paths using small stand-in executables, and proves restart on a forced API failure. It does **not** validate production binary startup or representative peak usage; the existing PostgreSQL-backed application CI runs separately, and a dedicated isolated real-binary acceptance gate must precede deployment. No host systemd unit is installed, enabled, modified or reloaded by the fixture.
