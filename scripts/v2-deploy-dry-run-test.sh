#!/usr/bin/env bash
set -Eeuo pipefail

# Runs only in an isolated Docker CI VM. No host systemd or nginx changes.
ROOT="$(git rev-parse --show-toplevel)"
cd "$ROOT"
fail() { printf 'v2-deploy-dry-run-test: %s\n' "$*" >&2; exit 1; }
command -v docker >/dev/null 2>&1 || fail "docker required"
docker info >/dev/null 2>&1 || fail "Docker daemon unavailable"

tmp="$(mktemp -d)"
container="ginbar-v2-deploy-$$"
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
trap 'exit 130' INT
trap 'exit 143' TERM

cp systemd/v2/ginbar-api-v2.service systemd/v2/ginbar-worker-v2.service "$tmp/"
cp nginx/v2/nginx.conf "$tmp/nginx.conf"
mkdir -p "$tmp/releases"
for release in a b bad; do
  mkdir -p "$tmp/releases/$release/frontend"
  printf '<!doctype html><title>frontend-%s</title>\n' "$release" >"$tmp/releases/$release/frontend/index.html"
  cat >"$tmp/releases/$release/api" <<'EOF_API'
#!/usr/bin/python3
import json
import os
from http.server import BaseHTTPRequestHandler, HTTPServer

release = os.path.basename(os.path.dirname(os.path.realpath(__file__)))

class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        status = 200
        if self.path == "/healthz":
            response = {"status": "ok"}
        elif self.path == "/readyz":
            status = 503 if release == "bad" else 200
            response = {"status": "not_ready" if status == 503 else "ready", "release": release}
        elif self.path == "/api/v2/posts":
            response = {"release": release, "host": self.headers.get("Host", ""),
                        "forwarded_proto": self.headers.get("X-Forwarded-Proto", "")}
        else:
            status = 404
            response = {"status": "not_found"}
        body = json.dumps(response, separators=(",", ":")).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Cache-Control", "no-store")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *_args):
        pass

HTTPServer(("127.0.0.1", 8080), Handler).serve_forever()
EOF_API
  cat >"$tmp/releases/$release/worker" <<'EOF_WORKER'
#!/usr/bin/env bash
set -euo pipefail
[[ "$#" -eq 1 && "$1" == run ]] || exit 2
release="$(basename "$(dirname "$(readlink -f "$0")")")"
printf '%s\n' "$release" > /srv/ginbar/worker-runtime/release
exec sleep infinity
EOF_WORKER
  chmod 755 "$tmp/releases/$release/api" "$tmp/releases/$release/worker"
done

# Container-only transaction: atomically switch the release pointer, restart
# original service units, reload original nginx, probe, or restore old release.
cat >"$tmp/apply-release" <<'EOF_SWITCH'
#!/usr/bin/env bash
set -Eeuo pipefail
[[ "$#" -eq 1 ]] || { echo "usage: apply-release RELEASE" >&2; exit 2; }
target="$1"
[[ "$target" =~ ^[a-z][a-z0-9-]*$ ]] || { echo "invalid release name" >&2; exit 2; }
base=/srv/ginbar/releases
[[ -x "$base/$target/api" && -x "$base/$target/worker" && -f "$base/$target/frontend/index.html" ]] \
  || { echo "incomplete candidate" >&2; exit 2; }
exec 9>/run/ginbar-v2-release-switch.lock
flock -x 9
previous="$(readlink "$base/current")"
[[ "$previous" =~ ^[a-z][a-z0-9-]*$ && -d "$base/$previous" ]] \
  || { echo "invalid previous release pointer" >&2; exit 2; }
[[ "$target" != "$previous" ]] || exit 0

probe() {
  local release="$1" ready body frontend
  for _ in $(seq 1 30); do
    if systemctl is-active --quiet ginbar-api-v2.service \
      && systemctl is-active --quiet ginbar-worker-v2.service \
      && [[ "$(cat /srv/ginbar/worker-runtime/release 2>/dev/null || true)" == "$release" ]]; then
      ready="$(curl -fsS --max-time 1 http://127.0.0.1:8080/readyz 2>/dev/null || true)"
      body="$(curl -kfsS --max-time 1 https://127.0.0.1/api/v2/posts 2>/dev/null || true)"
      frontend="$(curl -kfsS --max-time 1 https://127.0.0.1/ 2>/dev/null || true)"
      if grep -Fq "\"status\":\"ready\"" <<<"$ready" \
        && grep -Fq "\"release\":\"$release\"" <<<"$ready" \
        && grep -Fq "\"release\":\"$release\"" <<<"$body" \
        && grep -Fq '"forwarded_proto":"https"' <<<"$body" \
        && grep -Fq "frontend-$release" <<<"$frontend"; then return 0; fi
    fi
    sleep 0.2
  done
  return 1
}
activate() {
  local release="$1" staged="$base/.current.$$"
  ln -s "$release" "$staged" || return 1
  mv -Tf "$staged" "$base/current" || return 1
  systemctl restart ginbar-api-v2.service || return 1
  systemctl restart ginbar-worker-v2.service || return 1
  nginx -t >/dev/null 2>&1 || return 1
  systemctl reload nginx.service || return 1
  probe "$release"
}
if ! activate "$target"; then
  printf 'activation failed: %s; rolling back to %s\n' "$target" "$previous" >&2
  if ! activate "$previous"; then
    printf 'FATAL: rollback to %s failed\n' "$previous" >&2
    exit 1
  fi
  printf 'rollback recovered: %s\n' "$previous" >&2
  exit 1
fi
printf 'activated: %s\n' "$target"
EOF_SWITCH
chmod 755 "$tmp/apply-release"

cat >"$tmp/Dockerfile" <<'EOF_DOCKER'
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
      systemd nginx openssl curl python3 util-linux ca-certificates \
    && rm -rf /var/lib/apt/lists/*
RUN useradd --system --no-create-home ginbar \
    && mkdir -p /etc/ginbar/v2 /etc/nginx/tls /srv/ginbar/media/media /srv/ginbar/media/sources \
      /srv/ginbar/worker-runtime /srv/ginbar/releases \
    && chown ginbar:ginbar /srv/ginbar/worker-runtime \
    && printf 'DATABASE_URL=postgresql://unused/fixture\n' > /etc/ginbar/v2/api.env \
    && cp /etc/ginbar/v2/api.env /etc/ginbar/v2/worker.env \
    && printf 'media-stable\n' > /srv/ginbar/media/media/probe.txt \
    && printf 'private-source\n' > /srv/ginbar/media/sources/secret.txt \
    && openssl req -x509 -newkey rsa:2048 -nodes -days 1 \
      -subj '/CN=ginbar.test' -keyout /etc/nginx/tls/tls.key \
      -out /etc/nginx/tls/tls.crt >/dev/null 2>&1
COPY ginbar-api-v2.service ginbar-worker-v2.service /etc/systemd/system/
COPY nginx.conf /etc/nginx/nginx.conf
COPY releases /srv/ginbar/releases/
COPY apply-release /usr/local/sbin/apply-release
RUN ln -s a /srv/ginbar/releases/current \
    && ln -s /srv/ginbar/releases/current/api /usr/local/bin/ginbar-api-v2 \
    && ln -s /srv/ginbar/releases/current/worker /usr/local/bin/ginbar-worker-v2 \
    && ln -s /srv/ginbar/releases/current/frontend /srv/ginbar/frontend \
    && chmod 755 /usr/local/sbin/apply-release
STOPSIGNAL SIGRTMIN+3
CMD ["/lib/systemd/systemd"]
EOF_DOCKER

image="$(docker build --quiet "$tmp")"
docker run --rm --entrypoint systemd-analyze "$image" verify \
  /etc/systemd/system/ginbar-api-v2.service /etc/systemd/system/ginbar-worker-v2.service

# No published ports or host mounts. Container-private systemd and cgroups.
docker run -d -t --name "$container" --privileged --cgroupns=private \
  -e container=docker --tmpfs /run --tmpfs /run/lock "$image" >/dev/null
ready=0
for _ in $(seq 1 60); do
  state="$(docker exec "$container" systemctl is-system-running 2>/dev/null || true)"
  if [[ "$state" == running || "$state" == degraded ]]; then ready=1; break; fi
  if [[ "$(docker inspect --format '{{.State.Running}}' "$container")" != true ]]; then break; fi
  sleep 1
done
[[ "$ready" -eq 1 ]] || { docker logs "$container" >&2 || true; fail "isolated systemd failed"; }
docker exec "$container" systemctl start ginbar-api-v2.service ginbar-worker-v2.service nginx.service

check_release() {
  local expected="$1"
  docker exec "$container" bash -euc '
    expected="$1"
    [[ "$(readlink /srv/ginbar/releases/current)" == "$expected" ]]
    [[ "$(cat /srv/ginbar/worker-runtime/release)" == "$expected" ]]
    systemctl is-active --quiet ginbar-api-v2.service
    systemctl is-active --quiet ginbar-worker-v2.service
    systemctl is-active --quiet nginx.service
    ready="$(curl -fsS --max-time 2 http://127.0.0.1:8080/readyz)"
    proxied="$(curl -kfsS --max-time 2 https://127.0.0.1/api/v2/posts)"
    page="$(curl -kfsS --max-time 2 https://127.0.0.1/)"
    grep -Fq "\"release\":\"$expected\"" <<<"$ready"
    grep -Fq "\"release\":\"$expected\"" <<<"$proxied"
    grep -Fq "\"forwarded_proto\":\"https\"" <<<"$proxied"
    grep -Fq "frontend-$expected" <<<"$page"
    [[ "$(curl -kfsS --max-time 2 https://127.0.0.1/media/probe.txt)" == media-stable ]]
    [[ "$(curl -ksS -o /dev/null -w "%{http_code}" https://127.0.0.1/sources/secret.txt)" == 404 ]]
  ' -- "$expected" || fail "release $expected boundary check failed"
}
for _ in $(seq 1 30); do
  if docker exec "$container" test -f /srv/ginbar/worker-runtime/release \
    && docker exec "$container" curl -fsS --max-time 1 http://127.0.0.1:8080/readyz >/dev/null 2>&1; then
    break
  fi
  sleep 0.2
done
check_release a
docker exec "$container" /usr/local/sbin/apply-release b
check_release b
if docker exec "$container" /usr/local/sbin/apply-release bad; then
  fail "unready candidate accepted"
fi
check_release b
if docker exec "$container" /usr/local/sbin/apply-release ../a; then
  fail "unsafe release path accepted"
fi
check_release b
if docker exec "$container" /usr/local/sbin/apply-release missing; then
  fail "missing candidate accepted"
fi
check_release b
docker exec "$container" /usr/local/sbin/apply-release a
check_release a
printf 'v2-deploy-dry-run-test: PASS initial=a update=b failed=bad recovered=b rollback=a nginx=verified media=stable\n'
