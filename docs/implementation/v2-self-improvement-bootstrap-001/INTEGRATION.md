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
run after the seam was added. The full PostgreSQL end-to-end bootstrap smoke
(`internal/engineeringmission/postgres_integration_test.go`,
`-tags=integration`) now runs against disposable PostgreSQL guarded by
testdbguard, via `compose.integration.yaml`'s isolated `postgres`+
`integration-test` services -- never the shared development/production
instance. It proves the full path this document describes end to end:
durable `MissionPolicy` -> BaseSHA-bound isolated workspace ->
bounded/gated CodeRunner mutation -> sealed candidate commit ->
`RequestPromotion`/`RecordCheck` -> an independent reviewer role (never the
workspace author's own role) -> `SubmitReview(APPROVE)` ->
`PromotionApproved` -> target ref byte-identical to before the mission ran,
with `ApplyPromotion` never called (and never reachable from this
package's `PromotionPort`). Three additional PostgreSQL-backed negatives
in the same file prove fail-closed behavior for a mutation outside
`AllowedPaths` (denied before the patch ever reaches the repository), a
`BaseSHA` that has drifted from the real target-ref HEAD (denied at
workspace-creation time), and a `RequiredGates` entry the plan never
actually ran (denied before `RecordCheck`/`RequestPromotion`, zero
promotions created). Same-actor self-review, including the case where
author and reviewer share the same role ID, is covered by the package's
existing unit test and is enforced in `ReviewMission` itself -- before
ever delegating to `staging.SubmitReview` -- so it applies unconditionally,
independent of `staging`'s own `authority_class == "owner"` self-approval
allowance.

Fixed during this verification pass (real bugs, not scope creep):
`Service.Create` previously stored the literal string
`"code-runner-execution/v1"` as the task's `Instructions`, instead of an
actual `code-runner-execution/v1` JSON plan -- `coderunner`'s worker parses
`Instructions` directly as the plan (`ParsePlan([]byte(item.Task.
Instructions))`), so no mission created via the original `Create` could
ever have been executed. `Create` now takes the real plan JSON as an
explicit parameter, validated via `coderunner.ParsePlan` before the task is
even created. Separately, the `"engineering.required_gates"` requirement
key violated `tasks`' own identifier pattern (letters/digits/hyphen/
underscore only, no dots) and was renamed to
`"engineering-required-gates"` everywhere it appears.
