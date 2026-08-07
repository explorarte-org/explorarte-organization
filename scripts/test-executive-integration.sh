#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

compose=(docker compose -f compose.yaml -f compose.integration.yaml --profile integration)
cleanup() {
  "${compose[@]}" down -v --remove-orphans >/dev/null 2>&1 || true
}
trap cleanup EXIT

cleanup
"${compose[@]}" run --rm integration-test \
  go test -count=1 -tags=integration ./internal/executive/...
