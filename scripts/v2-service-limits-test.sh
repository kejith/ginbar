#!/usr/bin/env bash
set -Eeuo pipefail

# Uses a disposable systemd PID 1 inside a dedicated CI Docker container.
# No systemd unit, service, or process on the host is modified.
ROOT="$(git rev-parse --show-toplevel)"
cd "$ROOT"
command -v docker >/dev/null || { echo "v2-service-limits-test: docker is required" >&2; exit 1; }
docker info >/dev/null || { echo "v2-service-limits-test: Docker is unavailable" >&2; exit 1; }
tmp="$(mktemp -d)"
container="ginbar-v2-limits-$$"
image=""
cleanup() {
  local status=$?
  trap - EXIT
  docker rm -f "$container" >/dev/null 2>&1 || true
  if [[ -n "$image" ]]; then docker image rm "$image" >/dev/null 2>&1 || true; fi
  rm -rf "$tmp"
  exit "$status"
}
trap cleanup EXIT
cp systemd/v2/ginbar-api-v2.service systemd/v2/ginbar-worker-v2.service "$tmp/"
cat >"$tmp/Dockerfile" <<'EOF'
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends systemd && rm -rf /var/lib/apt/lists/*
RUN useradd --system --no-create-home ginbar && mkdir -p /etc/ginbar/v2 /srv/ginbar/media
RUN printf 'DATABASE_URL=postgresql://unused/fixture\n' > /etc/ginbar/v2/api.env \
    && cp /etc/ginbar/v2/api.env /etc/ginbar/v2/worker.env
COPY ginbar-api-v2.service ginbar-worker-v2.service /etc/systemd/system/
COPY stub-api stub-worker /usr/local/bin/
RUN chmod 755 /usr/local/bin/stub-* \
    && ln -s stub-api /usr/local/bin/ginbar-api-v2 \
    && ln -s stub-worker /usr/local/bin/ginbar-worker-v2
STOPSIGNAL SIGRTMIN+3
CMD ["/lib/systemd/systemd"]
EOF
cat >"$tmp/stub-api" <<'EOF'
#!/bin/sh
exec sleep 3600
EOF
cat >"$tmp/stub-worker" <<'EOF'
#!/bin/sh
[ "$1" = run ] || exit 3
exec sleep 3600
EOF
image="$(docker build --quiet "$tmp")"
docker run --rm --entrypoint systemd-analyze "$image" verify \
  /etc/systemd/system/ginbar-api-v2.service \
  /etc/systemd/system/ginbar-worker-v2.service

# Docker is on the disposable CI VM. The private cgroup namespace keeps the
# fixture services/cgroup mutations out of the host systemd unit namespace.
docker run -d --name "$container" --privileged --cgroupns=private \
  -e SYSTEMD_LOG_TARGET=console -e SYSTEMD_LOG_LEVEL=debug \
  --tmpfs /run --tmpfs /run/lock \
  -v /sys/fs/cgroup:/sys/fs/cgroup:rw \
  "$image" >/dev/null

ready=0
for i in $(seq 1 40); do
  state="$(docker exec "$container" systemctl is-system-running 2>/dev/null || true)"
  if [[ "$state" == running || "$state" == degraded ]]; then ready=1; break; fi
  sleep 1
done
[[ "$ready" -eq 1 ]] || { docker inspect --format 'status={{.State.Status}} exit={{.State.ExitCode}} error={{.State.Error}}' "$container" >&2 || true; docker logs "$container" >&2 || true; echo "systemd did not boot" >&2; exit 1; }

docker exec "$container" systemctl start ginbar-api-v2.service ginbar-worker-v2.service

docker exec "$container" bash -euc '
check() {
  local unit="$1" fds="$2" tasks="$3" memory="$4" pid cg
  [[ "$(systemctl show -P ActiveState "$unit")" == active ]]
  [[ "$(systemctl show -P LimitNOFILE "$unit")" == "$fds" ]]
  [[ "$(systemctl show -P TasksMax "$unit")" == "$tasks" ]]
  [[ "$(systemctl show -P MemoryMax "$unit")" == "$memory" ]]
  [[ "$(systemctl show -P MemoryAccounting "$unit")" == yes ]]
  [[ "$(systemctl show -P Restart "$unit")" == on-failure ]]
  [[ "$(systemctl show -P OOMPolicy "$unit")" == kill ]]
  [[ "$(systemctl show -P KillMode "$unit")" == control-group ]]
  [[ "$(systemctl show -P StartLimitBurst "$unit")" == 5 ]]
  pid="$(systemctl show -P MainPID "$unit")"
  [[ "$pid" =~ ^[1-9][0-9]*$ ]]
  [[ "$(awk "/Max open files/ {print \$4}" "/proc/$pid/limits")" == "$fds" ]]
  [[ "$(awk "/Max open files/ {print \$5}" "/proc/$pid/limits")" == "$fds" ]]
  cg="$(systemctl show -P ControlGroup "$unit")"
  [[ "$(cat "/sys/fs/cgroup$cg/pids.max")" == "$tasks" ]]
  [[ "$(cat "/sys/fs/cgroup$cg/memory.max")" == "$memory" ]]
}
check ginbar-api-v2.service 4096 256 1073741824
check ginbar-worker-v2.service 1024 128 4294967296

# A killed API is restarted, including the configured backoff.
old_pid="$(systemctl show -P MainPID ginbar-api-v2.service)"
kill -KILL "$old_pid"
for i in $(seq 1 40); do
  new_pid="$(systemctl show -P MainPID ginbar-api-v2.service)"
  if [[ "$new_pid" =~ ^[1-9][0-9]*$ && "$new_pid" != "$old_pid" ]] \
     && [[ "$(systemctl show -P ActiveState ginbar-api-v2.service)" == active ]]; then
    check ginbar-api-v2.service 4096 256 1073741824
    echo "v2-service-limits-test: PASS fds=4096/1024 tasks=256/128 memory=1G/4G restart=verified"
    exit 0
  fi
  sleep 1
done
echo "API failed to restart on SIGKILL" >&2
exit 1
'
