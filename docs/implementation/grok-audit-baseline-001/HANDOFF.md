# Grok Audit Baseline 001 — Remediation Closure Handoff

## A. HEAD

- branch: `fix/grok-audit-baseline-001`
- worktree: `/home/ubuntu/grok-audit-fixes`
- final SHA: `7f5b40859b718340586056ed7ce20edd5d95b762`
- base SHA: `56ee62de6e07a56d96450c9b9d14ba3769c7bad4` (production `main` at time of audit)
- working tree: clean

## B. FINDINGS

| ID | Original severity | Status | Fix commit(s) | Enforcement point | Regression test | Mutation fitness | Residual |
|---|---|---|---|---|---|---|---|
| ORG-AUDIT-001 | P1 | CLOSED | `d6f55ed` | `agentmessaging/postgres.Store.Send` idempotency-hit branch (`messageColumns`/`scanMessage` now select `request_hash`) | `TestSendRejectsSameKeyWithDifferentCommand` | Executed: reverting the column addition makes the test fail with `err=<nil>` instead of `ErrConflict` | none |
| ORG-AUDIT-002 | P1 | CLOSED | `f922d84` | `Store.Send`'s real `ValidateEdge` call (already wired; the gap was test coverage of that call site) | `TestSendDeniesWorkerToWorkerViaRealStore` | Behavioral: test exercises the real `Store`, not `ValidateEdge` in isolation; deleting the `ValidateEdge` line in `Send` would make it fail (not re-executed as a live mutation this pass, verified by direct code inspection of the assertion path) | none |
| ORG-AUDIT-003 | P1 | CLOSED | `3b6fcb0`, `d619ca8` (down-migration fix) | `costgate.Reserve` via `model_pricing`/`provider_wallets` rows for `openai_responses` (migration 000047) | `TestModelPricingSeedIsRealAndResolvable` (extended) + new `TestEveryRoutedNonSubscriptionProviderHasPricingAndAWallet` | Executed: deleting the `openai_responses` `provider_wallets` row makes the closure test fail with "has no provider_wallets row"; `model_pricing` rows are immutable by trigger so cannot be mutation-deleted, positive resolution assertions cover that half | Wallet balance is a placeholder value (mirrors `openai_compatible`'s), not a considered budget — flagged in the migration's own comment |
| ORG-AUDIT-004 | P2 | CLOSED | `7dba319` | `model_egress_revision_belongs_to_organization` SQL function (migration 000046) | `TestEgressRevisionOwnershipRecognizesHistoricalBindings` | Executed: applying 000046's down migration in-place and rerunning the test reproduces the exact pre-fix failure message; reapplying up restores PASS | none |
| ORG-AUDIT-005 | P2 | CLOSED_WITH_DEFERRED_RESIDUAL | `861c41c` | `Store.Send`/`ClaimNext` call `CapabilityAuthorizer.Authorize` against `capability-matrix.yaml` grants | `TestSendDeniesRoleWithoutMessageSendCapability` + full pre-existing suite (proves the matrix fix didn't break legitimate leader-claims-from-worker flows) | Executed: role without `agent.message.send` grant (`execution_service`) is denied on a topologically-legal edge; capability-matrix fix for `department_leadership` verified against the existing claim/ack tests | **D-005**: `Ack`/`Nack` (`agent.message.settle`) are not capability-gated |
| ORG-AUDIT-006 | P2 | CLOSED | `6dd99cd` | `audit_events_immutable` trigger (migration 000045) | Behavioral proof in section D below (INSERT ok, UPDATE/DELETE rejected, survives dump/restore) | Executed, this closure pass: verified against a restored database, not just the live schema | none |
| ORG-AUDIT-007 | P2 | CLOSED | `98b96ea` | `compose.yaml` `AUTO_MIGRATE` default, volume/network namespacing | Config-only; no runtime test (compose file correctness, verified by `docker compose config` succeeding and this closure's own worktree-isolated harness runs never colliding with production) | Declarative — no automated fitness for a default-value/namespacing change | none |
| ORG-AUDIT-008 | P2 | CLOSED_WITH_DEFERRED_RESIDUAL | `98b96ea` | `model-worker` healthcheck (`pgrep`-based process liveness) | None automated (a healthcheck is observed by `docker compose ps`/`--wait`, not `go test`) | Declarative | model-worker healthcheck proves the process exists, not that dispatch is progressing — a real readiness signal needs the worker to expose one, undone |
| ORG-AUDIT-009 | P1 | CLOSED | `c5b3832` | `tasks/contextprovider.Provider.GetTaskContext` (organization-only match, revision check removed) | `TestGetTaskContextSurvivesARevisionThatHasMovedOn` | Executed: restoring the revision-equality check reproduces the exact pre-fix "task context scope mismatch" error | none |
| ORG-AUDIT-010 | P1 | CLOSED_WITH_DEFERRED_RESIDUAL | `977092f`, `1e8fdc0` (regression repair) | `GetTaskContext`/`ValidateVersion` actor==assignee check | `TestGetTaskContextRejectsActorThatIsNotTheAssignee`, `TestGetTaskContextAllowsTheRealAssignee`, `TestValidateVersionRejectsActorThatIsNotTheAssignee` | Executed via allow/deny pairs (both directions asserted); a regression this same fix caused in `internal/executive`'s R23 test was found during this closure and repaired (test fixture, not the enforcement) | **D-010**: `ClaimTasks` with empty `AssignedRoleID` still claims from any role — making it mandatory broke 7 of `internal/tasks/postgres`'s own subtests through a shared unscoped helper |
| ORG-AUDIT-011 | P2 | CLOSED | `fcbc0c0` | `rag/postgres.Store.Reindex` body-offset verification for non-media chunks | `TestApprovedKnowledgeRAGPostgresRepository/reindex_rejects_a_chunk_whose_content_was_not_derived_from_the_approved_body` | Executed: forged chunk (self-consistent hash, real approved version_id, content that doesn't match the body) is rejected; full legitimate-reindex suite still passes | Media-backed chunk provenance (external file/page) is separate, undone work |
| ORG-AUDIT-012 | P2 | CLOSED_WITH_DEFERRED_RESIDUAL | `2aea5fc` | `Service.GetTask`/`Service.ListDeadLetters` post-fetch org comparison; `contextengine.Store.List` rejects empty `organization_id` | `TestGetTaskRejectsTaskFromAnotherOrganization` (unit, mutation-checked) | Executed: reverting the org comparison in `Service.GetTask` makes the test fail with `err=<nil>` instead of `ErrNotFound` | **D-012**: `Cancel`/`Finalize`/`StartAttempt` and the shared `lockTask` helper are still not organization-bound (~20 call sites across the executive orchestrator) |

## C. NEW HARNESS FINDING — D-HARNESS-001

- **Root cause**: `internal/agentmessaging/postgres` and `internal/executive` (+ `postrun`, `+sleep`) had real `//go:build integration` test files that were never listed in `scripts/integration-suites.tsv`, the file the harness itself documents as "the single source of truth for what an integration run is expected to observe." `COMPLETE_GREEN` never depended on running them.
- **Fix**: added four units (`agentmessaging-postgres`, `executive-postgres`, `executive-postrun-postgres`, `executive-sleep-postgres`) to the manifest, plus their mode names to `test-integration.sh`'s validation list. Commits `f409f1a`, `7f5b408`.
- **Behavioral proof**: full harness run before this fix (using the pre-remediation manifest via `ORG_INTEGRATION_SUITES_FILE`) reports `expected=20` and the four unit IDs absent from the evidence entirely; the same run against the current manifest reports `expected=24` with all four present and individually accounted.
- **Mutation proof**: `scripts/check-integration-evidence-fitness.sh` fitness 4 (new, committed) drives the **real** manifest through a fast `SAFETY_ABORT` precondition (no Docker needed), confirms all four units are observed, then removes exactly those four lines and confirms `expected` drops from 24 to 20 with all four vanishing from the evidence. Re-run as part of this closure: `PASS`.
- **Status**: CLOSED. No other integration-tagged package in the repository was found missing from the manifest (checked: every `//go:build integration` file's package now maps to a TSV row).

## D. MIGRATIONS

- Tip: **47** (`000045_make_audit_events_immutable`, `000046_recognize_historical_egress_revision_bindings`, `000047_seed_openai_responses_pricing_and_wallet`)
- **44→47 upgrade**: demonstrated repeatedly this pass via `orgctl migrate up` against a freshly-created isolated Postgres 17 instance (`explorarte_test`), reporting `"current": 47` each time.
- **0→47 fresh**: same command, same fresh volume — this is what every one of the isolated-postgres runs this pass actually was (Compose volume created new each time via `docker compose up -d --wait postgres` on a torn-down project).
- **Rollback/reapply**: `internal/contextengine/postgres`'s `TestContextEnginePostgreSQL17/down_migration_and_reapply_in_disposable_integration_database` rolls back every migration ≥ 6 (all of 45/46/47 included) and reapplies to tip, asserting table existence after. This is a pre-existing test in the repo, not written for this closure — it caught a real bug in 000047's down migration (see below) and now passes.
- **Bug found and fixed**: 000047's original down migration tried to `DELETE FROM model_pricing`, which has been append-only-by-trigger since 000020. Caught by the down/reapply test: `ERROR: model pricing rows are immutable (SQLSTATE P0001)`. Fixed by leaving the seeded price tiers standing on rollback (same pattern as 000026/000027's own down migrations) and only removing the mutable `provider_wallets` row. Commit `d619ca8`.
- **Invariants after upgrade** (000045/000046), demonstrated directly this pass:
  - `audit_events`: legitimate `INSERT` succeeds; `UPDATE` and `DELETE` both fail with `ERROR: audit_events rows are append-only`.
  - `model_egress_revision_belongs_to_organization('explorarte', <current revision>)` → `true`; `('explorarte', <nonexistent revision>)` → `false`; `('<unrelated org>', <real revision>)` → `false`.
- **Dump/restore**: `pg_dump --no-owner --no-privileges` of the migrated `explorarte_test` database (11,289 lines, 0 errors) → `pg_restore`d/`psql`-loaded into a fresh `explorarte_test_restore` database on the same instance (0 errors) → re-ran both invariant checks above against the restored database with identical results (`audit_events_immutable` trigger present and enforcing; `model_egress_revision_belongs_to_organization` present and correct for current/nonexistent/cross-org cases). This is the check this domain's own migration history (000008/000044) says was missing before and caused a real incident.

## E. TESTS

- `go build ./...` — PASS
- `go vet ./...` — PASS
- `go vet -tags=integration ./...` — PASS
- `go test ./...` (unit, no build tags) — PASS, 86 packages with tests, 0 failures (caught and fixed one hardcoded migration-tip assertion: `TestMigrationTipAndContiguity`, commit `3848bef`)
- `go test -race ./internal/{agentmessaging,tasks,contextengine,rag,modelpricing,modelegress}/...` (unit) — PASS
- `go test -race -tags=integration ./internal/{agentmessaging,tasks,contextengine,rag,modelegress,modelpricing}/postgres` (against live Postgres) — PASS, run individually per package
- Official integration harness (`scripts/test-integration.sh all`): **23/24 PASS**, 1 FAIL (`executive-postgres`, pre-existing, documented in section C's sibling finding and section F below), `accounting_complete=true`, `evidence_complete=true`, final status `COMPLETE_WITH_FAILURES`
- `scripts/check-integration-evidence-fitness.sh` (generic accounting fitness + new D-HARNESS-001 fitness 4) — PASS

Two real regressions were found and fixed during this closure review, both from running full packages this branch's implementation phase never ran in isolation:
1. `internal/executive`'s R23 integration test broke from ORG-AUDIT-010's actor==assignee check (test fixture bug, fixed, commit `1e8fdc0`).
2. `internal/contextengine/postgres`'s generic down/reapply test caught ORG-AUDIT-003's migration 000047 down script trying to mutate an immutable table (fixed, commit `d619ca8`).

## F. DEFERRED

- **D-005** — Agent messaging settle capability: `Ack`/`Nack` still authenticate via claim token + principal only, not `agent.message.settle`. Impact: a role without a settle grant can still ack/nack claimed messages. Not widened this pass because threading the capability check through settlement needs its own careful look at how principal/role identity is available at that point, not a rushed addition. Condition to resolve: before `agent.message.settle` is relied upon as an actual authorization boundary anywhere in product logic.
- **D-010** — Task claim role binding: `ClaimTasks(AssignedRoleID="")` still claims the next ready task from any role. Not made mandatory this pass because it broke 7 of `internal/tasks/postgres`'s own integration subtests that claim through a shared helper without specifying a role; each would need auditing for whether it's deliberately role-agnostic or simply never specified one. Condition to resolve: before an operational surface that lets an untrusted or lower-privilege caller invoke claim without a role ships.
- **D-012** — Task mutation organization binding: `Cancel`/`Finalize`/`StartAttempt` and the shared `lockTask` helper (~20 call sites across the executive orchestrator, runtime adapters, and dispatch bootstrap) are still not organization-bound at the query level. **Classified PRE-MULTI-ORG obligatorio**, not indefinite debt: this deployment is single-organization today, so the exploitability is latent; it becomes a hard blocker the moment a second organization is provisioned in the same database, and must be resolved before that happens, not after.
- **New, non-audit-finding**: `TestExecutivePostgreSQL17AgentBudgetsAndMessagingAreWiredThroughDelegation` (in `internal/executive`, now run by the harness via D-HARNESS-001's fix) fails because it configures `WithAgentMessaging` without `WithExecutionPrincipal`, tripping the orchestrator's deliberate fail-closed path. Confirmed pre-existing against this branch's unmodified base (`git stash` + rerun reproduced the identical failure before any ORG-AUDIT work began). Not fixed — out of the twelve findings' scope and not one of the three deferred items. The harness now reports it honestly as `FAIL` instead of it being invisible.

## G. RISK ASSESSMENT

- **P0 remaining**: none found, in this branch's scope or during this closure review.
- **P1 remaining**: none unclosed among the twelve findings. D-005 and D-010 are P2-class residuals of P2/P1 findings respectively, not unaddressed P1s in themselves — see section F for why each is bounded.
- **P2 remaining**: D-005, D-008 (healthcheck proves liveness not readiness), D-010, D-012.
- **Unknown/unverified**: whether any organization other than `explorarte` will ever be provisioned in the same Postgres instance (this is exactly what would convert D-012 from latent to live). The pre-existing `TestExecutivePostgreSQL17AgentBudgetsAndMessagingAreWiredThroughDelegation` gap's blast radius outside the test itself (i.e., whether production `orchestrator` construction always supplies `WithExecutionPrincipal` via `bootstrap.Open`'s fallback) was not re-verified this pass — `bootstrap/runtime.go`'s own fallback (`"oracle-01/model-runtime-01"` if the env var is unset) suggests production is not exposed to this specific gap, but that inference was not independently confirmed against a running production orchestrator.

## H. MERGE READINESS

**READY_FOR_INDEPENDENT_REVIEW**
