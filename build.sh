#!/usr/bin/env bash
set -euo pipefail

build_dir="$(mktemp -d "${TMPDIR:-/tmp}/buffgo-build.XXXXXX")"
trap 'rm -rf -- "${build_dir}"' EXIT

echo "npm --prefix web ci"
npm --prefix web ci
echo "npm --prefix web run build"
npm --prefix web run build
echo "go test ./..."
go test ./...
echo "go build ./cmd/buffgo"
go build -o "${build_dir}/buffgo" ./cmd/buffgo
echo "ok"
