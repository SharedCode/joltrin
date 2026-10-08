#!/usr/bin/env bash
# Assembles the site exactly as the Pages deploy publishes it, then checks that
# every local file the pages point at is really in it. The deploy workflow and
# the pull request check both run this script, so what a PR proves is what ships.
# It expects demo/sop.wasm, demo-agents/sop-agents.wasm, both wasm_exec.js files
# and sop-arena/dist to be built already.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"
SITE="${1:-_site}"

rm -rf "$SITE"
mkdir -p "$SITE/arena" "$SITE/agents" "$SITE/docs" "$SITE/assets"

# Technical demo (WASM) lives at the site root: joltrinhq.com/
cp demo/index.html demo/tailwind.css demo/sop.wasm demo/wasm_exec.js demo/404.html demo/favicon.svg demo/favicon.ico demo/og-image.png demo/logo-mark.png "$SITE/"
# The documentation index lives at joltrinhq.com/docs/
cp docs/index.html "$SITE/docs/"
# The homepage loads its replay and videos from ./assets/. Every file there ships.
cp -r docs/assets/. "$SITE/assets/"
# SOP Arena lives at a subpath of the same site: joltrinhq.com/arena/
cp -r sop-arena/dist/. "$SITE/arena/"
# Agent verification barrier (verify in WASM) lives at joltrinhq.com/agents/
cp demo-agents/index.html demo-agents/tailwind.css demo-agents/sop-agents.wasm demo-agents/wasm_exec.js demo-agents/favicon.svg demo-agents/favicon.ico demo-agents/og-image.png demo-agents/logo-mark.png "$SITE/agents/"
# Preserve custom domain (e.g. joltrinhq.com) if CNAME exists
if [ -f CNAME ]; then
  cp CNAME "$SITE/CNAME"
elif [ -f demo/CNAME ]; then
  cp demo/CNAME "$SITE/CNAME"
fi

fail=0

# Every ./ path a page names must exist. This covers src, href and poster
# attributes and ./assets/ paths written inside scripts, which is how the demo
# video is loaded.
for page in index.html agents/index.html docs/index.html; do
  dir="$(dirname "$page")"
  refs=$(grep -oE "(src|href|poster)=[\"']\./[^\"'#?]+[\"']|['\"]\./assets/[^\"'#?]+['\"]" "$SITE/$page" \
    | grep -oE '\./[^"'"'"'#?]+' | sed -E 's/^\.\///' | sort -u || true)
  for ref in $refs; do
    path="$SITE/$dir/$ref"; [ "$dir" = "." ] && path="$SITE/$ref"
    if [ ! -e "$path" ]; then
      echo "Error: $page references ./$ref but it is not in the site" >&2
      fail=1
    fi
  done
done

# Videos are YouTube embeds only. A local video file or a <video> tag on a page
# once shipped without its files and showed a black box, so neither is allowed.
if grep -nE '<video|\.(mp4|webm|mov)' "$SITE/index.html" "$SITE/agents/index.html" "$SITE/docs/index.html"; then
  echo "Error: pages must embed videos from YouTube, not a local video file or <video> tag" >&2
  fail=1
fi
if ls "$SITE"/assets/*.mp4 "$SITE"/assets/*.webm "$SITE"/assets/*.mov >/dev/null 2>&1; then
  echo "Error: video files must not be published in the site, use a YouTube link" >&2
  fail=1
fi

[ "$fail" = 0 ] || exit 1
echo "Site assembled in $SITE and every referenced file is present."
