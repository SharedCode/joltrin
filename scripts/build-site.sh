#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

echo "==> Assembling Joltrin Combined Site for E2E Testing..."

# Ensure Arena is built
if [ ! -d "sop-arena/dist" ] || [ ! -f "sop-arena/dist/index.html" ]; then
  echo "Building sop-arena..."
  if [ ! -d "sop-arena/node_modules" ]; then
    (cd sop-arena && npm ci)
  fi
  (cd sop-arena && npm run build)
fi

# Ensure demo WASM exists
if [ ! -f "demo/sop.wasm" ]; then
  echo "Compiling demo/sop.wasm..."
  (cd demo && GOOS=js GOARCH=wasm go build -ldflags="-s -w" -o sop.wasm .)
fi

# Ensure agent barrier WASM exists
if [ ! -f "demo-agents/sop-agents.wasm" ]; then
  echo "Compiling demo-agents/sop-agents.wasm..."
  (cd demo-agents && GOOS=js GOARCH=wasm go build -ldflags="-s -w" -o sop-agents.wasm .)
fi

# Assemble _site with the same script the Pages deploy runs, so the tests look at
# what ships. A separate copy list here once let the tests pass on a site the
# deploy then published with files missing.
for f in demo/wasm_exec.js demo-agents/wasm_exec.js; do
  if [ ! -f "$f" ]; then
    GOROOT="$(go env GOROOT)"
    src="${GOROOT}/lib/wasm/wasm_exec.js"
    [ -f "$src" ] || src="${GOROOT}/misc/wasm/wasm_exec.js"
    cp "$src" "$f"
  fi
done
scripts/assemble-site.sh _site
