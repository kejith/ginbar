#!/usr/bin/env bash
# Generate synthetic JPEG test inputs for the worker benchmarks.
#
# Usage:
#   ./scripts/gen_bench_inputs.sh
#
# Creates three JPEG files in /tmp with different resolutions.
# Requires ffmpeg on PATH.
set -euo pipefail

sizes=(
    "640:427"
    "1280:853"
    "1920:1279"
)

for size in "${sizes[@]}"; do
    w="${size%%:*}"
    h="${size##*:}"
    out="/tmp/bench_${w}x${h}.jpg"

    if [[ -f "$out" ]]; then
        echo "exists: $out"
        continue
    fi

    echo "creating: $out (${w}x${h})"
    ffmpeg -y -f lavfi -i "testsrc2=size=${w}x${h}:rate=1:duration=1" \
        -frames:v 1 -q:v 2 "$out" \
        -hide_banner -loglevel error
done

echo "done — benchmark inputs ready in /tmp"
