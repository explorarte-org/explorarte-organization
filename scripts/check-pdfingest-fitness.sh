#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
fail() { printf 'pdfingest fitness: %s\n' "$*" >&2; exit 1; }

[[ -d internal/pdfingest ]] || fail "internal/pdfingest is missing"
[[ -d internal/pdfingest/poppler ]] || fail "internal/pdfingest/poppler is missing"

# os/exec is confined to internal/pdfingest/poppler -- the whole point of
# the owner's decision that PDF parsing must not live in orgd/core: a
# malformed PDF can crash or hang a parser, and it must never be able to
# take orgd down with it. Every other package, in particular cmd/orgd and
# internal/app, must never import os/exec at all.
# Other pre-existing, already-approved os/exec carve-outs (git operations
# in staging, the retired Alibaba CLI adapter, test fixtures) are excluded
# here too -- this check only guards against a *new* subprocess surface
# appearing outside the ones already deliberately reviewed.
if rg -n --glob '*.go' \
  --glob '!internal/pdfingest/poppler/**' \
  --glob '!internal/staging/gitexec/**' \
  --glob '!internal/staging/postgres/**' \
  --glob '!internal/modelruntime/adapter/alibabaclaude/**' \
  --glob '!internal/codeexecutionfixtures/**' \
  '"os/exec"' . >/dev/null 2>&1; then
  fail "os/exec found outside the approved carve-out packages"
fi

# No shell strings, ever -- owner decision point 2 ("without sh -c").
if rg -n --glob '*.go' '(/bin/(ba)?sh|sh -c|exec\.Command\([^,]*"sh"|CommandContext\([^,]+,[^,]*"sh")' internal/pdfingest >/dev/null 2>&1; then
  fail "shell invocation found in internal/pdfingest"
fi

# orgd and the generic application process must remain unaware PDFs exist.
if rg -n 'internal/pdfingest' cmd/orgd internal/app >/dev/null 2>&1; then
  fail "orgd or app imports internal/pdfingest"
fi

# The three poppler-utils binaries are resolved by name via exec.LookPath,
# never hardcoded to a specific filesystem path -- keeps the package
# portable across the dev container and the dedicated pdfingest image
# without a rebuild.
grep -Fq 'exec.LookPath("pdfseparate")' internal/pdfingest/poppler/poppler.go || fail "pdfseparate is not resolved via LookPath"
grep -Fq 'exec.LookPath("pdftotext")' internal/pdfingest/poppler/poppler.go || fail "pdftotext is not resolved via LookPath"
grep -Fq 'exec.LookPath("pdfinfo")' internal/pdfingest/poppler/poppler.go || fail "pdfinfo is not resolved via LookPath"

# Quarantine is a small, closed set -- owner decision point 6: only
# malformed/encrypted/unsupported are permanent (never retry-worthy)
# verdicts. Timeout must stay a distinct, retryable error, never folded
# into QuarantineReason.
grep -Fq 'QuarantineMalformed   QuarantineReason = "malformed"' internal/pdfingest/processor.go || fail "QuarantineMalformed missing or renamed"
grep -Fq 'QuarantineEncrypted   QuarantineReason = "encrypted"' internal/pdfingest/processor.go || fail "QuarantineEncrypted missing or renamed"
grep -Fq 'QuarantineUnsupported QuarantineReason = "unsupported"' internal/pdfingest/processor.go || fail "QuarantineUnsupported missing or renamed"
if grep -Fq 'QuarantineTimeout' internal/pdfingest/processor.go; then
  fail "timeout must not be a QuarantineReason (owner decision: retryable, not a permanent verdict)"
fi
grep -Fq 'ErrTimeout = errors.New' internal/pdfingest/processor.go || fail "ErrTimeout missing"

# Empty extracted text is not an ingestion failure (owner decision point
# 7) -- must be a distinct, non-error status, not silently coerced.
grep -Fq 'TextExtractionEmpty' internal/pdfingest/processor.go || fail "TextExtractionEmpty status missing"

printf 'pdfingest fitness: OK\n'
