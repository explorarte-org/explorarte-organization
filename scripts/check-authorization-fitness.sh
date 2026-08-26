#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
BASE_SHA="$(bash "$ROOT/scripts/resolve-task-base.sh")"

command -v rg >/dev/null 2>&1 || {
  echo "ERROR: ripgrep (rg) is required" >&2
  exit 1
}

fail() {
  echo "ERROR: authorization fitness: $*" >&2
  exit 1
}

if rg -n 'internal/(staging|tasks)' internal/authorization --glob '*.go'; then
  fail "internal/authorization imports a forbidden module"
fi

for table in authorization_requests authorization_decisions authorization_uses; do
  rg -q "CREATE TABLE ${table}" migrations/000005_create_capability_policy_engine.up.sql || fail "missing durable table ${table}"
done
rg -q 'UNIQUE[[:space:]]*\(request_id\)' migrations/000005_create_capability_policy_engine.up.sql || fail "authorization_uses must enforce UNIQUE(request_id)"
rg -q 'authorization_requests_requester_role_fk' migrations/000005_create_capability_policy_engine.up.sql || fail "missing requester role foreign key"
rg -q 'REFERENCES organization_roles\(organization_id, id\)' migrations/000005_create_capability_policy_engine.up.sql || fail "missing composite role foreign key"

rg -q 'type CapabilityAuthorizer interface' internal/authorization/interfaces.go || fail "legacy CapabilityAuthorizer disappeared"
rg -q 'Authorize\(context\.Context, string, int64, string, string\) error' internal/authorization/interfaces.go || fail "legacy Authorize signature changed"

for event in \
  authorization.request_created \
  authorization.request_reused \
  authorization.decision_approved \
  authorization.decision_rejected \
  authorization.request_cancelled \
  authorization.request_expired \
  authorization.approval_consumed \
  authorization.scope_mismatch \
  authorization.policy_drift_denied; do
  rg -q "$event" internal/authorization/postgres || fail "missing transition event ${event}"
done
rg -q 'INSERT INTO outbox_events' internal/authorization/postgres/helpers.go || fail "authorization transitions do not write the shared outbox"
rg -q 'INSERT INTO audit_events' internal/authorization/postgres/helpers.go || fail "authorization transitions do not write shared audit events"

python3 - <<'PY'
from pathlib import Path

authorizer = Path("internal/authorization/authorizer.go").read_text()
start = authorizer.index("func (a *Authorizer) evaluate")
body = authorizer[start:authorizer.index("func (a *Authorizer) Authorize", start)]
for deny in ("a.globalHardDenied", "a.authorityHardDenied"):
    if body.index(deny) > body.index("capability.Approval != \"\""):
        raise SystemExit(f"ERROR: {deny} is evaluated after approval")

service = Path("internal/authorization/service.go").read_text()
start = service.index("func (s *Service) validateConsumption")
body = service[start:service.index("func (s *Service) CancelRequest", start)]
if body.index("s.policy.hardDenied") > body.index("capability.Approval == \"\""):
    raise SystemExit("ERROR: approval can be evaluated before hard deny during consumption")
PY

rg -q 'concurrent consumption creates exactly one use' internal/authorization/postgres/integration_test.go || fail "missing concurrent consumption integration test"
rg -q 'revision and matrix policy drift' internal/authorization/postgres/integration_test.go || fail "missing policy drift integration test"
rg -q 'rollback.*audit|audit.*roll back' internal/authorization/postgres/integration_test.go || fail "missing transactional rollback integration test"

# Canonical immutability is defined ONCE (delta vs the real change base).
bash "$ROOT/scripts/check-canonical-immutability.sh" "$BASE_SHA"

echo "authorization fitness checks passed"
