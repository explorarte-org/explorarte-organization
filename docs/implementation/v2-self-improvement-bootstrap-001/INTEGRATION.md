# Governed Engineering Mission Bootstrap

Base: `8f682de8bb5a0f302763c64dc1ee5b107711c573`  
Branch: `v2/self-improvement-bootstrap-001`

This slice adds `internal/engineeringmission` as a thin application layer. An
engineering mission is durable task state plus one `engineering-mission/v1`
policy evidence record. The resolver reconstructs the policy after restart
from `tasks.GetTask` and fails closed for missing or duplicate records.

The policy binds a full BaseSHA, bounded repository-relative mutation paths,
typed GO_BUILD/GO_VET/GO_TEST gates, and acceptance criteria. `coderunner`
continues to own execution, while `staging` owns isolated worktrees, sealing,
artifacts, checks, and promotion. `WorkspaceResolver` supplies the mission
BaseSHA to the existing staging service; the service's existing target-head
check rejects drift. `Guard` validates patch/gofmt mutations before execution
and validates actual changed files before sealing.

Promotion is intentionally outside this package's ports: the existing staging
promotion/review APIs remain the source of truth and `ApplyPromotion` is not
reachable from the engineering mission layer. Engineering review must be
independent of the workspace author; no owner exception is introduced here.

Rejected alternatives:

- a second task/workflow/candidate store — existing tasks and staging already
  own those durable facts;
- extending `internal/improvement` — that package owns artifact lifecycle,
  not repository self-modification;
- shell commands or model-selected authority — CodeRunner's typed operation
  contract and trusted runtime configuration remain authoritative.

Verification history: policy boundary tests and existing CodeRunner tests were
run after the seam was added. A full PostgreSQL end-to-end bootstrap smoke is
pending wiring to the repository's integration composition root; it must not
be represented as passed until run against disposable PostgreSQL guarded by
testdbguard.
