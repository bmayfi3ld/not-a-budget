#!/usr/bin/env bash
# Build the not-a-budget server binary and pack the .mcpb bundle.
#
# Usage:
#   scripts/build.sh                # build for the host platform, then pack
#   scripts/build.sh all            # cross-compile all platforms, then pack (host binary is bundled)
#
# The MCPB `binary` server type bundles a single platform's binary per package.
# For a multi-platform release, build a separate .mcpb per target (the pack step
# uses whatever is at server/not-a-budget[.exe]).
set -euo pipefail

cd "$(dirname "$0")/.."

PKG=./cmd/server
OUT=server

mkdir -p "$OUT" dist

# Version is tracked only in CHANGELOG.md; extract the top released heading.
VERSION=$(grep -m1 -oE '## \[[0-9]+\.[0-9]+\.[0-9]+\]' CHANGELOG.md | grep -oE '[0-9]+\.[0-9]+\.[0-9]+')
LDFLAGS="-s -w -X main.version=${VERSION}"

# Sync the version into manifest.json so the bundle matches the changelog.
if command -v jq >/dev/null 2>&1; then
  tmp=$(mktemp); jq --arg v "$VERSION" '.version = $v' manifest.json > "$tmp" && mv "$tmp" manifest.json
else
  sed -i.bak -E 's/^([[:space:]]*)"version": "[^"]*"/\1"version": "'"$VERSION"'"/' manifest.json && rm -f manifest.json.bak
fi
echo "version ${VERSION} (from CHANGELOG.md) -> manifest.json"

build() {
  local goos="$1" goarch="$2" ext="${3:-}"
  echo "building ${goos}/${goarch}..."
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
    go build -trimpath -ldflags="$LDFLAGS" -o "dist/not-a-budget-${goos}-${goarch}${ext}" "$PKG"
}

if [[ "${1:-host}" == "all" ]]; then
  build linux amd64
  build linux arm64
  build darwin amd64
  build darwin arm64
  build windows amd64 .exe
fi

# Host binary goes into server/ for packing.
HOST_EXT=""
if [[ "$(go env GOOS)" == "windows" ]]; then HOST_EXT=".exe"; fi
echo "building host binary -> ${OUT}/not-a-budget${HOST_EXT}"
CGO_ENABLED=0 go build -trimpath -ldflags="$LDFLAGS" -o "${OUT}/not-a-budget${HOST_EXT}" "$PKG"

# Pack the bundle. Requires the mcpb CLI (npx @anthropic-ai/mcpb).
if command -v mcpb >/dev/null 2>&1; then
  mcpb pack . dist/not-a-budget.mcpb
elif command -v npx >/dev/null 2>&1; then
  npx --yes @anthropic-ai/mcpb pack . dist/not-a-budget.mcpb
else
  echo "mcpb CLI not found; skipping pack. Install with: npm i -g @anthropic-ai/mcpb" >&2
  exit 1
fi

echo "done -> dist/not-a-budget.mcpb"
