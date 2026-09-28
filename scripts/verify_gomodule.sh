#!/usr/bin/env bash
# Verifies that the root Go module is actually consumable the way an
# external project would consume it: `go get` with no version pinned
# resolves cleanly and lands on the latest tagged release.
#
# This exists because v2.0.0 through v5.6.0 were not go-gettable at all.
# Go requires a v2+ tag to have a matching /vN path suffix when a go.mod is
# present, and root's go.mod had none, so every tagged release silently
# failed for any external consumer until the module path itself was fixed.
# Nothing in the existing test suite caught that, it only surfaces when
# something outside this repo actually tries to depend on a tagged release.
# This script is that check, run automatically instead of by hand.
#
# Two things get verified:
#   1. Static: the module path declared in go.mod matches the major version
#      of the latest real release tag (v2+ needs a /vN suffix, v0/v1 must
#      not have one).
#   2. Live: `go get` with no version pinned, from a scratch module outside
#      this repo, actually resolves to that same latest tag.
#
# Usage: ./scripts/verify_gomodule.sh

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

RED='\033[0;31m'
GREEN='\033[0;32m'
BLUE='\033[0;34m'
NC='\033[0m'

fail() {
  echo -e "${RED}[FAIL]${NC} $1"
  exit 1
}

pass() {
  echo -e "${GREEN}[PASS]${NC} $1"
}

echo -e "${BLUE}==> Go module go-gettability check${NC}"

MODULE_LINE="$(grep -m1 '^module ' go.mod)"
MODULE_PATH="${MODULE_LINE#module }"
echo "Declared module path: ${MODULE_PATH}"

# Latest real release tag: strict vN.N.N only. This repo also carries tags
# for unrelated ecosystems (Sop4CS-*, sop4py-*) and one-off backups, none of
# those are Go module versions and must not be considered here.
LATEST_TAG="$(git tag --list | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | sort -V | tail -1)"
if [ -z "$LATEST_TAG" ]; then
  fail "no vN.N.N release tag found, nothing to verify against"
fi
echo "Latest release tag: ${LATEST_TAG}"

LATEST_MAJOR="${LATEST_TAG%%.*}"          # e.g. v5.7.0 -> v5
LATEST_MAJOR_NUM="${LATEST_MAJOR#v}"      # -> 5

# --- 1. Static: module path suffix must match the latest tag's major version ---
echo -e "\n${BLUE}[1/2] Module path vs. latest tag major version${NC}"

PATH_SUFFIX=""
if [[ "$MODULE_PATH" =~ /v([0-9]+)$ ]]; then
  PATH_SUFFIX="${BASH_REMATCH[1]}"
fi

if [ "$LATEST_MAJOR_NUM" -ge 2 ]; then
  if [ "$PATH_SUFFIX" != "$LATEST_MAJOR_NUM" ]; then
    fail "latest tag ${LATEST_TAG} is major version ${LATEST_MAJOR_NUM}, but go.mod's module path is '${MODULE_PATH}' (suffix: '${PATH_SUFFIX:-none}'). Go requires a matching /v${LATEST_MAJOR_NUM} suffix for any v2+ tag when a go.mod is present, without it no external consumer can go get this release."
  fi
  pass "module path suffix /v${PATH_SUFFIX} matches latest tag major version ${LATEST_MAJOR_NUM}"
else
  if [ -n "$PATH_SUFFIX" ]; then
    fail "latest tag ${LATEST_TAG} is major version 0 or 1, which must not carry a /vN path suffix, but go.mod declares '${MODULE_PATH}'"
  fi
  pass "latest tag ${LATEST_TAG} is v0/v1, module path correctly has no suffix"
fi

# --- 2. Live: go get with no version pinned resolves to the latest tag ---
echo -e "\n${BLUE}[2/2] Live go get resolution (no version pinned)${NC}"

BASE_PATH="${MODULE_PATH%/v[0-9]*}"
WANT="${BASE_PATH}$( [ -n "$PATH_SUFFIX" ] && echo "/v${PATH_SUFFIX}" )"

SCRATCH_DIR="$(mktemp -d)"
trap 'rm -rf "$SCRATCH_DIR"' EXIT

(
  cd "$SCRATCH_DIR"
  go mod init gomodule-verify >/dev/null 2>&1
  go get "${WANT}" 2>&1
) || fail "go get ${WANT} (no version pinned) did not resolve"

RESOLVED="$(cd "$SCRATCH_DIR" && go list -m "${WANT}" 2>/dev/null | awk '{print $2}')"
echo "Resolved version: ${RESOLVED}"

if [ "$RESOLVED" != "$LATEST_TAG" ]; then
  fail "go get with no version resolved to ${RESOLVED}, expected the latest tag ${LATEST_TAG}"
fi
pass "go get ${WANT} with no version pinned resolves to ${LATEST_TAG}, the actual latest release"

echo -e "\n${GREEN}Go module is correctly go-gettable at the latest release.${NC}"
