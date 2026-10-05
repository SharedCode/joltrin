#!/usr/bin/env bash
# Builds the homepage demo video from the real binary.
#
#   scripts/promo/build.sh [out-dir]      (default: docs/assets)
#
# It builds sop-mcp-server, records what it prints (capture.mjs), draws the
# scene at 30 fps (render.mjs), and encodes an MP4 at 1080 px and 720 px and two poster sizes. Needs
# Go, Node with @playwright/test and its Chromium, and ffmpeg.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
out="${1:-$root/docs/assets}"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
mkdir -p "$out"

echo "==> build the real binary"
(cd "$root" && go build -o "$work/sop-mcp-server" ./cmd/sop-mcp-server)

echo "==> capture its real output"
node "$root/scripts/promo/capture.mjs" "$work/sop-mcp-server" "$work/scene-data.json"

echo "==> draw the frames"
node "$root/scripts/promo/render.mjs" "$work/scene-data.json" "$work"

echo "==> encode"
frames="$work/frames/f%05d.jpg"
# H.264 for every browser and for LinkedIn. yuv420p and faststart so it plays
# at once on a phone.
ffmpeg -y -loglevel error -framerate 30 -i "$frames" \
  -c:v libx264 -preset slow -crf 25 -pix_fmt yuv420p -movflags +faststart -an \
  "$out/joltrin-barrier.mp4"
# A 720 px copy for phones, which is about half the size.
ffmpeg -y -loglevel error -framerate 30 -i "$frames" -vf scale=720:720:flags=lanczos \
  -c:v libx264 -preset slow -crf 26 -pix_fmt yuv420p -movflags +faststart -an \
  "$out/joltrin-barrier-720.mp4"
# Poster: the moment the barrier blocks the action.
ffmpeg -y -loglevel error -i "$work/frames/f00525.jpg" -vf scale=1080:1080 -q:v 4 "$out/joltrin-barrier-poster.jpg"
ffmpeg -y -loglevel error -i "$work/frames/f00525.jpg" -vf scale=540:540 -q:v 5 "$out/joltrin-barrier-poster-540.jpg"

for f in joltrin-barrier.mp4 joltrin-barrier-720.mp4 joltrin-barrier-poster.jpg joltrin-barrier-poster-540.jpg; do
  printf '%-30s %s\n' "$f" "$(du -h "$out/$f" | cut -f1)"
done
printf 'duration: %s s\n' "$(ffprobe -v error -show_entries format=duration -of csv=p=0 "$out/joltrin-barrier.mp4")"
