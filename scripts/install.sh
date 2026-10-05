#!/bin/sh
# Installs sop-mcp-server, the Joltrin barrier for MCP agents, and registers it
# with the agent CLIs found on this machine (Claude Code, Codex, Gemini CLI).
#
#   curl -fsSL https://raw.githubusercontent.com/SharedCode/joltrin/master/scripts/install.sh | sh
#
# It downloads the latest release binary, checks its SHA-256 against the
# release's checksum file, and puts it in ~/.joltrin/bin. It does not use sudo.
#
#   JOLTRIN_VERIFY=1     also verify the binary's signed build provenance with
#                        the GitHub CLI (gh, signed in). It does not rely on the
#                        checksum file, needs a release from v5.11.0 on, and adds
#                        a few seconds, so it is off by default
#   JOLTRIN_VERSION=TAG  install that release instead of the latest, e.g. v5.11.0
#   JOLTRIN_NO_SETUP=1   install only, do not register with any agent
#   JOLTRIN_BIN_DIR=DIR  install somewhere other than ~/.joltrin/bin
#
# Trust: the script, the binary and the checksum file all come from the same
# GitHub release, so the checksum catches a damaged or swapped download but not
# a compromised release. If that matters to you, set JOLTRIN_VERIFY=1, read this
# script first, pin a version, or build from source.
set -eu

if [ -n "${JOLTRIN_VERSION:-}" ]; then
  default_base=https://github.com/SharedCode/joltrin/releases/download/$JOLTRIN_VERSION
else
  default_base=https://github.com/SharedCode/joltrin/releases/latest/download
fi
base=${JOLTRIN_BASE_URL:-$default_base}
dest=${JOLTRIN_BIN_DIR:-$HOME/.joltrin/bin}

fail() { echo "install: $*" >&2; exit 1; }

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case $os in
  darwin | linux) ;;
  *) fail "this script supports macOS and Linux. On Windows, download the .exe from https://github.com/SharedCode/joltrin/releases/latest" ;;
esac

arch=$(uname -m)
case $arch in
  arm64 | aarch64) arch=arm64 ;;
  x86_64 | amd64) arch=amd64 ;;
  *) fail "no prebuilt binary for $arch" ;;
esac
# A shell started under Rosetta reports x86_64 on an Apple Silicon Mac.
if [ "$os" = darwin ] && [ "$arch" = amd64 ] && [ "$(sysctl -in hw.optional.arm64 2>/dev/null || true)" = 1 ]; then
  arch=arm64
fi

name=sop-mcp-server-$os-$arch
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
cd "$tmp"

echo "Downloading $name"
curl -fsSL -O "$base/$name" -O "$base/sop-mcp-server-SHA256SUMS" || fail "download failed from $base"

if command -v shasum >/dev/null 2>&1; then sum="shasum -a 256"; else sum=sha256sum; fi
grep " $name\$" sop-mcp-server-SHA256SUMS > want || fail "$name is not in the checksum file"
$sum -c want >/dev/null 2>&1 || fail "checksum does not match, nothing was installed"

if [ "${JOLTRIN_VERIFY:-}" = 1 ]; then
  command -v gh >/dev/null 2>&1 || fail "JOLTRIN_VERIFY=1 needs the GitHub CLI (gh), signed in with gh auth login"
  echo "Verifying build provenance"
  gh attestation verify "$name" --repo SharedCode/joltrin >/dev/null 2>&1 ||
    fail "build provenance could not be verified, nothing was installed. It needs gh signed in and a release from v5.11.0 on"
fi

mkdir -p "$dest"
mv "$name" "$dest/sop-mcp-server"
chmod +x "$dest/sop-mcp-server"
echo "Installed $dest/sop-mcp-server"

if [ "${JOLTRIN_NO_SETUP:-}" = 1 ]; then
  echo "Register it with: $dest/sop-mcp-server setup --apply"
else
  "$dest/sop-mcp-server" setup --apply
fi
