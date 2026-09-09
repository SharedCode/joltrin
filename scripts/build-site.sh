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

# Assemble _site
rm -rf _site
mkdir -p _site/arena _site/agents _site/docs _site/assets

echo "Copying Technical Demo..."
cp -r demo/. _site/

echo "Copying Joltrin Arena..."
cp -r sop-arena/dist/. _site/arena/

echo "Copying Agent Verification Barrier..."
cp -r demo-agents/. _site/agents/

echo "Copying Documentation and Assets..."
cp -r docs/. _site/docs/
cp -r docs/assets/. _site/assets/

# Preserve custom domain (e.g. joltrin.com) if CNAME exists
if [ -f "CNAME" ]; then
  cp CNAME _site/CNAME
elif [ -f "demo/CNAME" ]; then
  cp demo/CNAME _site/CNAME
fi

echo "Site assembled successfully in _site/"
