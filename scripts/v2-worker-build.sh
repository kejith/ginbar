#!/usr/bin/env bash
set -Eeuo pipefail

ROOT="$(git rev-parse --show-toplevel)"
cd "$ROOT"

BASE_IMAGE="${GINBAR_V2_WORKER_BUILD_BASE_IMAGE:-rust:1.99.0-bookworm}"

fail() {
  printf 'v2-worker-build: %s\n' "$*" >&2
  exit 1
}

usage() {
  cat >&2 <<'EOF'
usage:
  bash scripts/v2-worker-build.sh OUTPUT
  bash scripts/v2-worker-build.sh --if-needed BASE_SHA OUTPUT

Builds the v2 worker release binary with Rust 1.99 and NASM inside an
ephemeral Docker container. No Rust/NASM installation on the host is required.
OUTPUT must be outside the repository checkout.
EOF
  exit 2
}

should_build=1
if [[ "${1:-}" == "--if-needed" ]]; then
  [[ $# -eq 3 ]] || usage
  base_sha="$2"
  output="$3"
  if [[ -n "$base_sha" && ! "$base_sha" =~ ^0+$ ]] && git cat-file -e "${base_sha}^{commit}" 2>/dev/null; then
    should_build=0
    while IFS= read -r path; do
      case "$path" in
        src/worker/v2/*|scripts/v2-worker-build.sh|.github/workflows/v2-ci.yml)
          should_build=1
          break
          ;;
      esac
    done < <(git diff --name-only "$base_sha" HEAD)
  fi
else
  [[ $# -eq 1 ]] || usage
  output="$1"
fi

if (( ! should_build )); then
  printf 'v2-worker-build: skipped; worker build inputs unchanged\n'
  exit 0
fi

command -v docker >/dev/null 2>&1 || fail "docker is required"
docker info >/dev/null 2>&1 || fail "docker daemon is not accessible to user $(id -un)"

output_dir="$(dirname "$output")"
mkdir -p "$output_dir"
output_dir="$(cd "$output_dir" && pwd -P)"
output_name="$(basename "$output")"
[[ "$output_name" != "." && "$output_name" != ".." && -n "$output_name" ]] || fail "invalid output path"
output="$output_dir/$output_name"

case "$output" in
  "$ROOT"|"$ROOT"/*)
    fail "output must be outside the repository checkout"
    ;;
esac

tmp_name=".${output_name}.tmp.$$"
tmp_output="$output_dir/$tmp_name"
rm -f "$tmp_output"
cleanup() {
  rm -f "$tmp_output"
}
trap cleanup EXIT

printf 'v2-worker-build: sha=%s base_image=%s output=%s\n' \
  "$(git rev-parse HEAD)" "$BASE_IMAGE" "$output"

docker run --rm \
  -e DEBIAN_FRONTEND=noninteractive \
  -e HOST_UID="$(id -u)" \
  -e HOST_GID="$(id -g)" \
  -e OUTPUT_NAME="$tmp_name" \
  -v "$ROOT:/repo:ro" \
  -v "$output_dir:/out" \
  -w /repo \
  "$BASE_IMAGE" \
  bash -euc '
    apt-get update >/dev/null
    apt-get install -y --no-install-recommends nasm >/dev/null
    rustc --version >&2
    cargo --version >&2
    nasm -v >&2
    export CARGO_HOME=/tmp/cargo-home
    export CARGO_TARGET_DIR=/tmp/cargo-target
    cargo build --release --locked --manifest-path src/worker/v2/Cargo.toml
    install -m 0755 /tmp/cargo-target/release/ginbar-worker-v2 "/out/$OUTPUT_NAME"
    chown "$HOST_UID:$HOST_GID" "/out/$OUTPUT_NAME"
  '

[[ -x "$tmp_output" ]] || fail "release binary missing after container build"
mv -f "$tmp_output" "$output"
trap - EXIT

printf 'v2-worker-build: bytes=%s sha256=%s\n' \
  "$(stat -c %s "$output")" "$(sha256sum "$output" | awk '{print $1}')"
