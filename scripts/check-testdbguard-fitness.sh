#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
fail(){ echo "testdbguard fitness: FAIL: $*" >&2; exit 1; }

# 2026-08-12 development-database incident: an integration test executed a
# real TRUNCATE against the shared dev Postgres instead of an isolated one.
# internal/testdbguard exists to make that structurally harder -- this check
# exists to make it structurally harder to introduce a NEW destructive
# integration test that forgets to call it. Every *_test.go file below is
# scanned for destructive SQL signatures; if one is found, the same file
# must also reference testdbguard, or be explicitly allowlisted below with a
# stated reason. An unexplained allowlist addition defeats this check.
ALLOWLIST=(
  # time.Time.Truncate(...) (a std-lib method), not SQL TRUNCATE.
  "internal/modelidentity/crypto_test.go"
  "internal/contextengine/assembler_test.go"
  "internal/modelruntime/provider_request_test.go"
  # response_truncated_empty error-code string literal, not SQL TRUNCATE.
  "internal/modelruntime/adapter/openaicompat/adapter_test.go"
  "internal/modelruntime/adapter/deepseek/adapter_test.go"
  "internal/modelruntime/adapter/gemini/adapter_test.go"
  "internal/modelruntime/adapter/mimo/adapter_test.go"
  # Test names/behavior about truncating long fields/prefixes in strings,
  # not SQL TRUNCATE.
  "internal/objectstorage/client_test.go"
  "internal/objectstorage/keys_test.go"
  # DROP TABLE appears only inside an in-memory fstest.MapFS fixture used to
  # exercise the migration-file LOADER (Load()) -- never executed against a
  # real Postgres connection.
  "internal/platform/migrations/runner_test.go"
  # Same package (postgres_test) as internal/rag/postgres/integration_test.go
  # and calls its guarded openRAGStore/resetRAGSchema helpers directly; has
  # no destructive call site of its own.
  "internal/rag/postgres/canary_test.go"
)

is_allowlisted() {
  local needle="$1"
  for entry in "${ALLOWLIST[@]}"; do
    [ "$needle" = "$entry" ] && return 0
  done
  return 1
}

# Real destructive SQL signatures used in this codebase's integration tests.
# Kept narrow (uppercase SQL keywords, exact identifiers) to avoid false
# positives on prose/comments/unrelated identifiers such as "Truncate" as an
# English word or a stdlib method name.
PATTERN='TRUNCATE [A-Za-z_]|DROP (TABLE|SCHEMA|DATABASE|INDEX|EXTENSION)|RESTART IDENTITY|DELETE FROM schema_migrations|\.DownSQL\b'

violations=0
while IFS= read -r -d '' file; do
  relative="${file#./}"
  if is_allowlisted "$relative"; then
    continue
  fi
  if ! grep -Eq "$PATTERN" "$file"; then
    continue
  fi
  if ! grep -q 'testdbguard\.' "$file"; then
    echo "testdbguard fitness: FAIL: $relative contains destructive SQL but never calls testdbguard.RequireDestructive/RequireTestDatabase" >&2
    violations=$((violations + 1))
  fi
done < <(find . -name '*_test.go' -print0)

if [ "$violations" -gt 0 ]; then
  fail "$violations integration test file(s) run destructive SQL without a testdbguard guard -- see internal/testdbguard"
fi

echo "testdbguard fitness: PASS"
