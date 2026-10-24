#!/usr/bin/env bash
# Assembles the site exactly as the Pages deploy publishes it, then checks that
# every local file the pages point at is really in it. The deploy workflow and
# the pull request check both run this script, so what a PR proves is what ships.
# It expects demo-intent/intent.wasm, demo/sop.wasm, demo-agents/sop-agents.wasm, demo-btree/arena.wasm, the wasm_exec.js files
# and sop-arena/dist to be built already.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"
SITE="${1:-_site}"

rm -rf "$SITE"
mkdir -p "$SITE/arena" "$SITE/agents" "$SITE/btree" "$SITE/private-intent" "$SITE/docs" "$SITE/assets"

# Technical demo (WASM) lives at the site root: joltrinhq.com/
cp demo/index.html demo/tailwind.css demo/sop.wasm demo/wasm_exec.js demo/404.html demo/favicon.svg demo/favicon.ico demo/og-image.png demo/logo-mark.png demo/robots.txt demo/sitemap.xml "$SITE/"
# The documentation index lives at joltrinhq.com/docs/
cp docs/index.html "$SITE/docs/"
# The homepage loads its replay and videos from ./assets/. Every file there ships.
cp -r docs/assets/. "$SITE/assets/"
# SOP Arena lives at a subpath of the same site: joltrinhq.com/arena/
cp -r sop-arena/dist/. "$SITE/arena/"
# Agent verification barrier (verify in WASM) lives at joltrinhq.com/agents/
cp demo-agents/index.html demo-agents/tailwind.css demo-agents/sop-agents.wasm demo-agents/wasm_exec.js demo-agents/favicon.svg demo-agents/favicon.ico demo-agents/og-image.png demo-agents/logo-mark.png "$SITE/agents/"
# Joltrin Arena B-tree challenge (the in-memory B-tree in WASM) lives at joltrinhq.com/btree/
cp demo-btree/index.html demo-btree/tailwind.css demo-btree/arena.wasm demo-btree/wasm_exec.js demo-btree/favicon.svg demo-btree/favicon.ico demo-btree/og-image.png demo-btree/logo-mark.png "$SITE/btree/"
# Private Intent (the intent package in WASM) lives at joltrinhq.com/private-intent/
cp demo-intent/index.html demo-intent/tailwind.css demo-intent/intent.wasm demo-intent/wasm_exec.js demo-intent/favicon.svg demo-intent/favicon.ico demo-intent/og-image.png demo-intent/logo-mark.png "$SITE/private-intent/"
# Response headers for the Cloudflare deployment.
cp deploy/cloudflare/_headers deploy/cloudflare/_redirects "$SITE/"

fail=0

# Every ./ path a page names must exist. This covers src, href and poster
# attributes and ./assets/ paths written inside scripts, which is how the demo
# video is loaded.
for page in index.html agents/index.html btree/index.html private-intent/index.html docs/index.html; do
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
if grep -nE '<video|\.(mp4|webm|mov)' "$SITE/index.html" "$SITE/agents/index.html" "$SITE/btree/index.html" "$SITE/private-intent/index.html" "$SITE/docs/index.html"; then
  echo "Error: pages must embed videos from YouTube, not a local video file or <video> tag" >&2
  fail=1
fi
for video in "$SITE"/assets/*.mp4 "$SITE"/assets/*.webm "$SITE"/assets/*.mov; do
  if [ -f "$video" ]; then
    echo "Error: video files must not be published in the site, use a YouTube link: $video" >&2
    fail=1
  fi
done

# Every page in the sitemap must be a real page in the site.
sitemap_pages=0
while IFS= read -r url; do
  sitemap_pages=$((sitemap_pages + 1))
  case "$url" in
    https://joltrinhq.com|https://joltrinhq.com/) page="index.html" ;;
    https://joltrinhq.com/*)
      page="${url#https://joltrinhq.com/}"
      page="${page%%[?#]*}"
      case "$page" in
        "") page="index.html" ;;
        */) page="${page}index.html" ;;
        *.html) ;;
        *) page="$page/index.html" ;;
      esac
      ;;
    *)
      echo "Error: sitemap.xml lists an unexpected URL: $url" >&2
      fail=1
      continue
      ;;
  esac
  case "/$page/" in
    */../*|*/./*)
      echo "Error: sitemap.xml lists a noncanonical path: $url" >&2
      fail=1
      continue
      ;;
  esac
  if [ ! -f "$SITE/$page" ]; then
    echo "Error: sitemap.xml lists $url but $SITE/$page does not exist" >&2
    fail=1
  fi
done < <(grep -oE '<loc>[^<]+</loc>' "$SITE/sitemap.xml" | sed -E 's|</?loc>||g')
if [ "$sitemap_pages" = 0 ]; then
  echo "Error: sitemap.xml contains no page URLs" >&2
  fail=1
fi

[ "$fail" = 0 ] || exit 1
echo "Site assembled in $SITE and every referenced file is present."
