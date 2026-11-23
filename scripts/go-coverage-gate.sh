#!/usr/bin/env bash
# Fails when a core package's statement coverage drops below its floor.
# Floors sit just under current coverage so a refactor that deletes tests,
# or a change that lands untested, fails the PR. Raise a floor when coverage
# rises; never lower one to get a PR through.
set -euo pipefail

floors=(
  ".:44"
  "./btree:89"
  "./inmemory:95"
  "./common:88"
  "./cache:64"
  "./logsafe:100"
  "./taint:90"
)

fail=0
for entry in "${floors[@]}"; do
  pkg="${entry%%:*}"
  floor="${entry##*:}"
  out=$(go test -count=1 -cover "$pkg")
  pct=$(printf '%s\n' "$out" | sed -n 's/.*coverage: \([0-9.]*\)%.*/\1/p' | tail -1)
  if [ -z "$pct" ]; then
    echo "FAIL $pkg: no coverage reported"
    printf '%s\n' "$out"
    fail=1
    continue
  fi
  if awk -v p="$pct" -v f="$floor" 'BEGIN { exit !(p + 0 < f + 0) }'; then
    echo "FAIL $pkg: ${pct}% < floor ${floor}%"
    fail=1
  else
    echo "ok   $pkg: ${pct}% >= floor ${floor}%"
  fi
done
exit "$fail"
