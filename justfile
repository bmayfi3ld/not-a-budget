# mcpb-budget tasks. Run `just` to list targets.
#
# The version is tracked in ONE place — the top released heading in CHANGELOG.md.
# `just build` extracts it and injects it into the binary (via -ldflags -X) and
# into manifest.json (via `sync-version`). Never hand-edit the version elsewhere.

set shell := ["bash", "-euo", "pipefail", "-c"]

pkg := "./cmd/server"
bin := "server/not-a-budget"
bundle := "dist/not-a-budget.mcpb"

# Version = first "## [x.y.z]" heading in CHANGELOG.md (skips [Unreleased]).
version := `grep -m1 -oE '## \[[0-9]+\.[0-9]+\.[0-9]+\]' CHANGELOG.md | grep -oE '[0-9]+\.[0-9]+\.[0-9]+'`

# List available targets.
default:
    @just --list

# Print the version from CHANGELOG.md.
print-version:
    @echo {{version}}

# Write the CHANGELOG version into manifest.json (the only other place it lives).
sync-version:
    @tmp=$(mktemp); jq --arg v "{{version}}" '.version = $v' manifest.json > "$tmp" && mv "$tmp" manifest.json
    @echo "manifest.json version -> {{version}}"

# Format sources.
fmt:
    gofmt -w .

# Fail if any file is not gofmt-clean.
fmt-check:
    @unformatted=$(gofmt -l .); if [ -n "$unformatted" ]; then echo "not gofmt-clean:"; echo "$unformatted"; exit 1; fi

# Vet.
vet:
    go vet ./...

# Run tests.
test:
    go test ./...

# fmt-check + vet + test.
check: fmt-check vet test

# Build the host binary with the version injected (no bundle).
compile: sync-version
    @mkdir -p server
    ext=""; [ "$(go env GOOS)" = "windows" ] && ext=".exe"; \
    CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version={{version}}" -o "{{bin}}$ext" {{pkg}}
    @echo "built {{bin}} ({{version}})"

# Build the host binary and pack the .mcpb bundle.
build: compile
    @mkdir -p dist
    if command -v mcpb >/dev/null 2>&1; then \
      mcpb pack . {{bundle}}; \
    elif command -v npx >/dev/null 2>&1; then \
      npx --yes @anthropic-ai/mcpb pack . {{bundle}}; \
    else \
      echo "mcpb CLI not found; install with: npm i -g @anthropic-ai/mcpb" >&2; exit 1; \
    fi
    @echo "packed {{bundle}} ({{version}})"

# Cross-compile all platforms into dist/ (each with the version injected).
build-all: sync-version
    #!/usr/bin/env bash
    set -euo pipefail
    mkdir -p dist
    ldflags="-s -w -X main.version={{version}}"
    build() { echo "building $1/$2..."; CGO_ENABLED=0 GOOS="$1" GOARCH="$2" go build -trimpath -ldflags "$ldflags" -o "dist/not-a-budget-$1-$2$3" {{pkg}}; }
    build linux amd64
    build linux arm64
    build darwin amd64
    build darwin arm64
    build windows amd64 .exe
    echo "cross-compiled all platforms ({{version}})"

# Remove build artifacts.
clean:
    rm -rf dist {{bin}} {{bin}}.exe
