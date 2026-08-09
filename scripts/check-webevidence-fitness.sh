#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
fail(){ echo "webevidence fitness: FAIL: $*" >&2; exit 1; }
require(){ local pattern="$1" path="$2"; grep -Eq "$pattern" "$path" || fail "missing pattern '$pattern' in $path"; }

# R30.1-4: internal/webevidencefixtures's runner asserts
# "automatic_rag_memory_promotion_never_occurs" is true because neither
# internal/webevidence nor internal/webevidencefixtures imports
# internal/rag or internal/memory — there is no function call either
# package could even make to promote a piece of web evidence into either
# system. That claim is only as good as this check: it must fail the
# build the moment either import boundary is crossed, the same discipline
# check-rag-fitness.sh/check-memory-fitness.sh already apply to their own
# domains.
if grep -R -E '"github.com/Mireuz13/explorarte-organization/internal/rag|"github.com/Mireuz13/explorarte-organization/internal/memory' internal/webevidence internal/webevidencefixtures --include='*.go'; then
  fail 'internal/webevidence(fixtures) must never import internal/rag or internal/memory — automatic promotion to either would become possible'
fi

require 'ErrInvalidEvidence' internal/webevidence/types.go
require 'TaskID <= 0' internal/webevidence/types.go
require 'ExpiresAt.IsZero\(\) \|\| !e\.ExpiresAt\.After\(e\.CapturedAt\)' internal/webevidence/types.go
require 'injectionPatterns' internal/webevidence/sanitize.go
require 'SourceWebEvidence' internal/contextengine/domain.go
require 'SourceWebEvidence' internal/contextengine/assembler.go
require 'InstructionData' internal/webevidencefixtures/render.go
require 'TrustUntrusted' internal/webevidencefixtures/render.go
require 'MayGrantCapabilities:[[:space:]]*false' internal/webevidencefixtures/render.go
require 'ValidateSourceMetadata' internal/webevidencefixtures/render.go
require 'NewAssembler\(\)\.Assemble' internal/webevidencefixtures/runner.go
require 'NewRenderer\(\)\.Render' internal/webevidencefixtures/runner.go
require 'context_engine_rejects_web_evidence_relabeled_as_instruction' internal/webevidencefixtures/runner.go

if grep -R -E 'MayGrantCapabilities:[[:space:]]*true' internal/webevidencefixtures --include='*.go'; then fail 'web evidence may grant capabilities'; fi

go test ./internal/webevidence/... ./internal/webevidencefixtures/... ./internal/contextengine/...

echo 'webevidence fitness: PASS'
