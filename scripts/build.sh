#!/usr/bin/env bash
# Builds tamp for GOOS and GOARCH, stamping the version, commit and build date
# that `tamp version` prints. The release workflow builds through it.
set -euo pipefail

OUT=${1:?usage: build.sh /path/to/tamp [version]}
cd "$(dirname "$0")/.."
VERSION=${2:-$(git describe --tags --always --dirty)}
COMMIT=$(git rev-parse HEAD)
BUILD_DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ)

CGO_ENABLED=0 go build -trimpath \
  -ldflags "-s -w -X main.version=$VERSION -X main.commit=$COMMIT -X main.buildDate=$BUILD_DATE" \
  -o "$OUT" ./cmd/tamp
printf 'version=%s\ncommit=%s\nbuild_date=%s\n' "$VERSION" "$COMMIT" "$BUILD_DATE"
