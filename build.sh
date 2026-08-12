#!/usr/bin/env bash
set -euo pipefail

build_dir="$(mktemp -d "${TMPDIR:-/tmp}/buffgo-build.XXXXXX")"
trap 'rm -rf -- "${build_dir}"' EXIT

echo "go test ./..."
go test ./...
echo "go build ./cmd/buffgo"
go build -o "${build_dir}/buffgo" ./cmd/buffgo
echo "ok"
