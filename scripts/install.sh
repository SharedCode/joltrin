#!/bin/sh
# Installs sop-mcp-server, the Joltrin barrier for MCP agents, and registers it
# with the agent CLIs found on this machine (Claude Code, Codex, Gemini CLI).
#
#   curl -fsSL https://raw.githubusercontent.com/SharedCode/joltrin/master/scripts/install.sh | sh
#
# It downloads the latest release binary, checks its SHA-256 against the
# release's checksum file, and puts it in ~/.joltrin/bin. It does not use sudo.
#
#   JOLTRIN_NO_SETUP=1   install only, do not register with any agent
#   JOLTRIN_BIN_DIR=DIR  install somewhere other than ~/.joltrin/bin
set -eu

base=${JOLTRIN_BASE_URL:-https://github.com/SharedCode/joltrin/releases/latest/download}
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

mkdir -p "$dest"
mv "$name" "$dest/sop-mcp-server"
chmod +x "$dest/sop-mcp-server"
echo "Installed $dest/sop-mcp-server"

if [ "${JOLTRIN_NO_SETUP:-}" = 1 ]; then
  echo "Register it with: $dest/sop-mcp-server setup --apply"
else
  "$dest/sop-mcp-server" setup --apply
fi
