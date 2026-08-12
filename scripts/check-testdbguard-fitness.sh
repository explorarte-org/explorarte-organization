#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

# 2026-08-12 branch audit: the previous version of this check only asked
# "does this *_test.go file contain the substring testdbguard. anywhere?"
# That allows two real false PASSes: (a) RequireTestDatabase called in one
# function while a TRUNCATE in a *different* function of the same file has
# no RequireDestructive at all; (b) RequireDestructive called, but *after*
# the destructive statement in source order, in the same function. Neither
# is actually guarded, and the old check said PASS for both.
#
# This version checks, per TOP-LEVEL func block (func ... { ... up to the
# next top-level `func `), that any destructive SQL line in that block has
# a testdbguard.RequireDestructive call *earlier in the same block* (by
# line number). This is a real, if not perfect, guarantee: it is scoped to
# top-level functions, not to individual t.Run(...) subtest closures
# nested inside one -- a destructive statement in subtest B of a function
# is satisfied by a guard call anywhere earlier in the SAME top-level
# function, including inside an earlier subtest A, which would be a false
# PASS this check does not catch. Every destructive site wired in this
# branch places its guard as the first statement of the same subtest
# closure it protects, so this gap is not currently exploited -- but it is
# a real, named limit of this check, not a solved problem. Do not describe
# this check as verifying per-statement or per-subtest guarding: it
# verifies per-top-level-function guard-before-first-destructive-line
# ordering.
python3 - "$@" <<'PYEOF'
import re
import subprocess
import sys

ALLOWLIST = {
    # time.Time.Truncate(...) (a std-lib method), not SQL TRUNCATE.
    "internal/modelidentity/crypto_test.go",
    "internal/contextengine/assembler_test.go",
    "internal/modelruntime/provider_request_test.go",
    # response_truncated_empty error-code string literal, not SQL TRUNCATE.
    "internal/modelruntime/adapter/openaicompat/adapter_test.go",
    "internal/modelruntime/adapter/deepseek/adapter_test.go",
    "internal/modelruntime/adapter/gemini/adapter_test.go",
    "internal/modelruntime/adapter/mimo/adapter_test.go",
    # Test names/behavior about truncating long fields/prefixes in strings,
    # not SQL TRUNCATE.
    "internal/objectstorage/client_test.go",
    "internal/objectstorage/keys_test.go",
    # DROP TABLE appears only inside an in-memory fstest.MapFS fixture used to
    # exercise the migration-file LOADER (Load()) -- never executed against a
    # real Postgres connection.
    "internal/platform/migrations/runner_test.go",
    # Same package (postgres_test) as internal/rag/postgres/integration_test.go
    # and calls its guarded openRAGStore/resetRAGSchema helpers directly; has
    # no destructive call site of its own.
    "internal/rag/postgres/canary_test.go",
}

DESTRUCTIVE_RE = re.compile(
    r'TRUNCATE [A-Za-z_]|DROP \((TABLE|SCHEMA|DATABASE|INDEX|EXTENSION)\)|DROP (TABLE|SCHEMA|DATABASE|INDEX|EXTENSION)|RESTART IDENTITY|DELETE FROM schema_migrations|\.DownSQL\b'
)
GUARD_RE = re.compile(r'testdbguard\.RequireDestructive')
PRESENCE_RE = re.compile(r'testdbguard\.')


def list_test_files():
    out = subprocess.run(["find", ".", "-name", "*_test.go"], capture_output=True, text=True, check=True)
    return sorted(p[2:] for p in out.stdout.splitlines() if p)


def check_file(path):
    with open(path, encoding="utf-8", errors="replace") as f:
        lines = f.readlines()

    if not any(DESTRUCTIVE_RE.search(l) for l in lines):
        return []
    if path in ALLOWLIST:
        return []
    if not any(PRESENCE_RE.search(l) for l in lines):
        return [f"{path}: contains destructive SQL but never references testdbguard at all"]

    func_starts = [i for i, l in enumerate(lines) if l.startswith("func ")]
    func_starts.append(len(lines))

    violations = []
    for idx in range(len(func_starts) - 1):
        start, end = func_starts[idx], func_starts[idx + 1]
        chunk = lines[start:end]
        destructive_offsets = [i for i, l in enumerate(chunk) if DESTRUCTIVE_RE.search(l)]
        if not destructive_offsets:
            continue
        guard_offsets = [i for i, l in enumerate(chunk) if GUARD_RE.search(l)]
        first_destructive = min(destructive_offsets)
        if not guard_offsets:
            violations.append(
                f"{path}:{start + first_destructive + 1}: destructive SQL in this function, "
                f"but no testdbguard.RequireDestructive call anywhere in the same top-level function"
            )
            continue
        first_guard = min(guard_offsets)
        if first_guard > first_destructive:
            violations.append(
                f"{path}:{start + first_destructive + 1}: destructive SQL occurs BEFORE the "
                f"testdbguard.RequireDestructive call in this function (guard at line {start + first_guard + 1})"
            )
    return violations


violations = []
for path in list_test_files():
    violations.extend(check_file(path))

if violations:
    for v in violations:
        print(f"testdbguard fitness: FAIL: {v}", file=sys.stderr)
    print(f"testdbguard fitness: FAIL: {len(violations)} violation(s)", file=sys.stderr)
    sys.exit(1)

print("testdbguard fitness: PASS (per-top-level-function guard-before-first-destructive-line check)")
PYEOF
