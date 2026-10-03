#!/usr/bin/env bash
set -Eeuo pipefail

ROOT="$(git rev-parse --show-toplevel)"
cd "$ROOT"

requested_scope="${1:-all}"
base_sha="${2:-${GINBAR_CI_BASE_SHA:-}}"

fail() {
  printf 'v2-ci: %s\n' "$*" >&2
  exit 1
}

release_host_gate() {
  if [[ -n "${HOST_GATE_SSH_PID:-}" ]]; then
    kill "$HOST_GATE_SSH_PID" 2>/dev/null || true
    wait "$HOST_GATE_SSH_PID" 2>/dev/null || true
    HOST_GATE_SSH_PID=""
  fi
}

resolve_auto_scope() {
  local base="$1"
  local worker=0 backend=0 frontend=0 shared=0 path

  if [[ -z "$base" || "$base" =~ ^0+$ ]] || ! git cat-file -e "${base}^{commit}" 2>/dev/null; then
    printf 'all\n'
    return
  fi

  while IFS= read -r path; do
    case "$path" in
      src/worker/v2/*)
        worker=1
        ;;
      src/backend/v2/internal/schema/migrations/*)
        worker=1
        backend=1
        ;;
      src/backend/v2/*)
        backend=1
        ;;
      src/frontend/*)
        frontend=1
        ;;
      go.work|scripts/v2-ci.sh|.github/workflows/v2-ci.yml|.gitignore)
        shared=1
        ;;
    esac
  done < <(git diff --name-only "$base" HEAD)

  if ((shared)); then
    printf 'all\n'
  elif ((worker && backend && frontend)); then
    printf 'all\n'
  elif ((worker && backend)); then
    printf 'server\n'
  elif ((frontend && (worker || backend))); then
    printf 'all\n'
  elif ((worker)); then
    printf 'worker\n'
  elif ((backend)); then
    printf 'backend\n'
  elif ((frontend)); then
    printf 'frontend\n'
  else
    printf 'all\n'
  fi
}

if [[ "$requested_scope" == "auto" ]]; then
  requested_scope="$(resolve_auto_scope "$base_sha")"
fi

run_worker=0
run_backend=0
run_frontend=0
case "$requested_scope" in
  all)
    run_worker=1
    run_backend=1
    run_frontend=1
    ;;
  server)
    run_worker=1
    run_backend=1
    ;;
  worker)
    run_worker=1
    ;;
  backend)
    run_backend=1
    ;;
  frontend)
    run_frontend=1
    ;;
  *)
    fail "unknown scope '$requested_scope' (expected auto, all, server, worker, backend, or frontend)"
    ;;
esac

printf 'v2-ci: sha=%s scope=%s base=%s\n' "$(git rev-parse HEAD)" "$requested_scope" "${base_sha:-none}"

if [[ -n "${GINBAR_HOST_GATE_SSH:-}" ]] && ! command -v flock >/dev/null 2>&1; then
  fail "flock is required for the remote host gate"
fi

if command -v flock >/dev/null 2>&1; then
  host_gate_lock="${GINBAR_HOST_GATE_LOCK:-/tmp/ginbar-v2-host-gate.lock}"
  if [[ -n "${GINBAR_HOST_GATE_SSH:-}" ]]; then
    host_gate_identity="${GINBAR_HOST_GATE_IDENTITY:-${HOME}/.ssh/ginbar-host-gate}"
    [[ -r "$host_gate_identity" ]] || fail "host gate SSH identity is unreadable ($host_gate_identity)"
    command -v ssh >/dev/null 2>&1 || fail "ssh is required for the remote host gate"
    coproc HOST_GATE_SSH {
      ssh -T -i "$host_gate_identity" \
        -o BatchMode=yes \
        -o ConnectTimeout=15 \
        -o StrictHostKeyChecking=yes \
        "$GINBAR_HOST_GATE_SSH"
    }
    if ! read -r -t 15 -u "${HOST_GATE_SSH[0]}" host_gate_status; then
      release_host_gate
      fail "could not acquire remote host gate ($GINBAR_HOST_GATE_SSH)"
    fi
    if [[ "$host_gate_status" != "LOCKED" ]]; then
      release_host_gate
      fail "remote host gate returned an unexpected response"
    fi
    trap 'release_host_gate' EXIT
  else
    exec 9>"$host_gate_lock"
    flock -n 9 || fail "host gate is busy ($host_gate_lock); do not overlap CI and target benchmarks"
  fi
fi

command -v docker >/dev/null 2>&1 || fail "docker is required"
DOCKER=(docker)
if ! docker info >/dev/null 2>&1; then
  if command -v sudo >/dev/null 2>&1 && sudo -n docker info >/dev/null 2>&1; then
    DOCKER=(sudo -n docker)
  else
    printf 'v2-ci: docker access diagnostics:\n' >&2
    id >&2 || true
    ls -l /var/run/docker.sock >&2 || true
    fail "docker daemon is not accessible to user $(id -un); grant the dedicated runner account Docker access"
  fi
fi

dkr() {
  "${DOCKER[@]}" "$@"
}

RUST_BASE_IMAGE="rust:1.99.0-bookworm"
RUST_CI_IMAGE="ginbar-v2-ci-rust:1.99.0-nasm"
POSTGRES_IMAGE="postgres:17.11-alpine"
GO_IMAGE="golang:1.25.0-bookworm"
NODE_IMAGE="node:22.20.0-bookworm"

CACHE_ROOT="${GINBAR_CI_CACHE_DIR:-${HOME:-/tmp}/.cache/ginbar-v2-ci}"
mkdir -p \
  "$CACHE_ROOT/cargo-home" \
  "$CACHE_ROOT/cargo-target" \
  "$CACHE_ROOT/go-mod" \
  "$CACHE_ROOT/go-build" \
  "$CACHE_ROOT/npm"

TMP_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/ginbar-v2-ci.XXXXXX")"
RUN_KEY="${GITHUB_RUN_ID:-local}-${GITHUB_RUN_ATTEMPT:-1}-$$"
NETWORK="ginbar-v2-ci-${RUN_KEY}"
PG_CONTAINER="ginbar-v2-ci-pg-${RUN_KEY}"
PG_STARTED=0

cleanup() {
  local status=$?
  trap - EXIT
  if ((PG_STARTED)); then
    dkr rm -f "$PG_CONTAINER" >/dev/null 2>&1 || true
    dkr network rm "$NETWORK" >/dev/null 2>&1 || true
  fi
  rm -rf "$TMP_ROOT" >/dev/null 2>&1 || true
  release_host_gate
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

ensure_image() {
  local image="$1"
  if ! dkr image inspect "$image" >/dev/null 2>&1; then
    dkr pull "$image"
  fi
}

ensure_rust_image() {
  if dkr image inspect "$RUST_CI_IMAGE" >/dev/null 2>&1; then
    return
  fi
  ensure_image "$RUST_BASE_IMAGE"
  dkr build --tag "$RUST_CI_IMAGE" - <<EOF_RUST_IMAGE
FROM $RUST_BASE_IMAGE
RUN apt-get update \
  && apt-get install -y --no-install-recommends nasm \
  && rm -rf /var/lib/apt/lists/*
RUN rustup component add rustfmt clippy
EOF_RUST_IMAGE
}

start_postgres() {
  ensure_image "$POSTGRES_IMAGE"
  dkr network create --label ginbar.v2.ci=true "$NETWORK" >/dev/null
  dkr run -d --rm \
    --name "$PG_CONTAINER" \
    --label ginbar.v2.ci=true \
    --network "$NETWORK" \
    --network-alias pg \
    -e POSTGRES_USER=ginbar_test \
    -e POSTGRES_PASSWORD=ginbar_test_ci_only \
    -e POSTGRES_DB=ginbar_test \
    "$POSTGRES_IMAGE" >/dev/null
  PG_STARTED=1

  local attempt
  for attempt in $(seq 1 60); do
    if dkr exec "$PG_CONTAINER" pg_isready -U ginbar_test -d ginbar_test >/dev/null 2>&1; then
      return
    fi
    sleep 1
  done
  dkr logs "$PG_CONTAINER" >&2 || true
  fail "PostgreSQL did not become ready"
}

DB_URL='postgres://ginbar_test:ginbar_test_ci_only@pg:5432/ginbar_test?sslmode=disable'

rust_run() {
  dkr run --rm \
    --network "$NETWORK" \
    -e CARGO_HOME=/cargo-home \
    -e CARGO_TARGET_DIR=/cargo-target \
    -e GINBAR_TEST_DATABASE_URL="$DB_URL" \
    -v "$ROOT:/repo:ro" \
    -v "$CACHE_ROOT/cargo-home:/cargo-home" \
    -v "$CACHE_ROOT/cargo-target:/cargo-target" \
    -w /repo \
    "$RUST_CI_IMAGE" "$@"
}

run_worker_gate() {
  printf '\n== worker: Rust 1.99 + PostgreSQL correctness gate ==\n'
  ensure_rust_image

  local lock_before lock_after test_log
  lock_before="$(sha256sum src/worker/v2/Cargo.lock | awk '{print $1}')"
  test_log="$TMP_ROOT/worker-tests.log"

  rust_run cargo fmt --manifest-path src/worker/v2/Cargo.toml -- --check
  rust_run cargo check --locked --manifest-path src/worker/v2/Cargo.toml
  rust_run cargo test --locked --manifest-path src/worker/v2/Cargo.toml -- --nocapture --test-threads=1 2>&1 | tee "$test_log"
  if grep -Fq 'skipping: GINBAR_TEST_DATABASE_URL is not set' "$test_log"; then
    fail "worker PostgreSQL-backed tests were skipped"
  fi
  rust_run cargo clippy --locked --manifest-path src/worker/v2/Cargo.toml --all-targets -- -D warnings

  lock_after="$(sha256sum src/worker/v2/Cargo.lock | awk '{print $1}')"
  [[ "$lock_before" == "$lock_after" ]] || fail "src/worker/v2/Cargo.lock changed during worker validation"
  git diff --exit-code -- src/worker/v2/Cargo.lock
}

go_run() {
  dkr run --rm \
    --network "$NETWORK" \
    -e GINBAR_TEST_DATABASE_URL="$DB_URL" \
    -e GOFLAGS=-mod=readonly \
    -e GOTOOLCHAIN=local \
    -e GOMODCACHE=/go-mod \
    -e GOCACHE=/go-build \
    -v "$ROOT:/repo:ro" \
    -v "$CACHE_ROOT/go-mod:/go-mod" \
    -v "$CACHE_ROOT/go-build:/go-build" \
    -w /repo/src/backend/v2 \
    "$GO_IMAGE" "$@"
}

run_backend_gate() {
  printf '\n== backend: Go 1.25 + PostgreSQL correctness gate ==\n'
  ensure_image "$GO_IMAGE"

  local unformatted test_log
  unformatted="$(go_run bash -lc "find . -type f -name '*.go' -print0 | xargs -0 -r gofmt -l")"
  if [[ -n "$unformatted" ]]; then
    printf '%s\n' "$unformatted" >&2
    fail "backend Go files are not gofmt-formatted"
  fi

  go_run go vet ./...
  test_log="$TMP_ROOT/backend-tests.log"
  go_run go test -v -count=1 ./... 2>&1 | tee "$test_log"
  if grep -Fq 'GINBAR_TEST_DATABASE_URL is not set' "$test_log"; then
    fail "backend PostgreSQL-backed tests were skipped"
  fi
}

run_frontend_gate() {
  printf '\n== frontend: Node 22 correctness gate ==\n'
  ensure_image "$NODE_IMAGE"
  dkr run --rm \
    -v "$ROOT:/repo:ro" \
    -v "$CACHE_ROOT/npm:/npm-cache" \
    "$NODE_IMAGE" \
    bash -lc 'cp -a /repo/src/frontend /tmp/frontend && cd /tmp/frontend && npm ci --cache /npm-cache --no-audit --no-fund && npm run validate'
}

if ((run_worker || run_backend)); then
  start_postgres
fi
if ((run_worker)); then
  run_worker_gate
fi
if ((run_backend)); then
  run_backend_gate
fi
if ((run_frontend)); then
  run_frontend_gate
fi

printf '\nv2-ci: PASS sha=%s scope=%s\n' "$(git rev-parse HEAD)" "$requested_scope"
