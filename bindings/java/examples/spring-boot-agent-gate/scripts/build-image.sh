#!/usr/bin/env bash
# Build the agent image with whichever engine is installed: docker, podman, or buildah.
# Usage: scripts/build-image.sh [tag]
set -euo pipefail
cd "$(dirname "$0")/.."
tag="${1:-agent-gate:dev}"

if command -v docker >/dev/null 2>&1; then
  docker build -t "$tag" .
elif command -v podman >/dev/null 2>&1; then
  podman build -t "$tag" .
elif command -v buildah >/dev/null 2>&1; then
  buildah bud -t "$tag" .
else
  echo "need docker, podman, or buildah on PATH" >&2
  exit 1
fi
echo "built $tag"
