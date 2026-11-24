#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
jobs="${WORKSPACE_JOBS:-2}"
case "${1:-help}" in
 fmt) gofmt -w cmd internal ;;
 build)
  mkdir -p bin
  go build -p "$jobs" -o bin/gateway ./cmd/gateway
  go build -p "$jobs" -o bin/mock-upstream ./cmd/mock-upstream
  ;;
 test) go test -p "$jobs" ./... ;;
 vet) go vet -p "$jobs" ./... ;;
 race)
  compiler="${CC:-gcc}"
  if ! command -v "$compiler" >/dev/null 2>&1; then
   echo "race requires a C compiler in WSL (not installed); no system changes made." >&2
   exit 2
  fi
  CGO_ENABLED=1 go test -race -p "$jobs" ./...
  ;;
 smoke) python3 scripts/smoke_test.py ;;
 check)
  unformatted="$(gofmt -l cmd internal)"
  if [[ -n "$unformatted" ]]; then printf 'Run fmt first:\n%s\n' "$unformatted"; exit 1; fi
  go vet -p "$jobs" ./...
  go test -p "$jobs" ./...
  ;;
 *) echo "Usage: bash scripts/dev.sh {fmt|build|test|vet|race|smoke|check}"; exit 2 ;;
esac
