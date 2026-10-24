#!/usr/bin/env bash
# Builds demo/tailwind.css, demo-agents/tailwind.css, demo-btree/tailwind.css and demo-intent/tailwind.css,
# the stylesheets the homepage, the agents page and the Joltrin Arena B-tree challenge link. They replace the
# Tailwind CDN script, which compiled the page's classes in the browser before
# first paint. Uses the Tailwind CLI that sop-arena already installs, so run
# `npm ci` in sop-arena first. Commit the result when index.html changes; the
# Site Check job fails if demo/tailwind.css is out of date.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TAILWIND="$REPO_ROOT/sop-arena/node_modules/.bin/tailwindcss"
[ -x "$TAILWIND" ] || { echo "run npm ci in sop-arena first" >&2; exit 1; }

printf '@tailwind base;\n@tailwind components;\n@tailwind utilities;\n' > "${TMPDIR:-/tmp}/joltrin-tailwind-in.css"
for dir in demo demo-agents demo-btree demo-intent; do
  (cd "$REPO_ROOT/$dir" && "$TAILWIND" -c tailwind.config.cjs -i "${TMPDIR:-/tmp}/joltrin-tailwind-in.css" -o tailwind.css --minify)
done
