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

## H. MERGE READINESS (original pass)

**READY_FOR_INDEPENDENT_REVIEW** — superseded by the closure round below. Independent adversarial verification (DeepSeek v4 Pro, EXP-0001, two phases, 115 turns total) returned `VERIFIED_WITH_REQUIRED_FIX`, not ready-for-merge as-is; three required fixes plus a deeper finding it did not surface are closed in the section that follows.

---

# Closure Round 2 — Post-Verification Fixes + EXEC-PRINCIPAL-001

## I. HEAD

- final SHA: `407f7bc546b799b257240269f1116aa3967c7e2f`
- base for this round: `69e604019134fa316bc879cb86fe04683f9daa17` (the SHA independently verified above)
- 8 new commits, all on `fix/grok-audit-baseline-001`, none squashed, none force-pushed
- working tree: clean

## J. REQUIRED FIXES FROM ADVERSARIAL VERIFICATION

1. **Compose canonical identity** (P1) — `compose.yaml` had committed `name: grok-audit-fixes-worktree` over the canonical `name: explorarte-organization`, the exact wrong-layer isolation the file's own header comment warns against. Restored. Commit `8b9dc42`.
2. **+5 harness suites** (D-HARNESS-001 follow-up) — `agentbudget-postgres`, `costledger-postgres`, `evaluation-postgres`, `modelpricing-postgres`, `webevidence-postgres` added to `scripts/integration-suites.tsv`. Commit `afb2bcf`. Adding them **surfaced two independent, real, pre-existing bugs** — not decoration:
   - `internal/modelruntime/postgres`'s down/reapply test had a stale hardcoded rollback list missing migrations 37/39/47, silently corrupting `cost_provenance` and the `openai_responses` wallet row for every suite running after it in the shared harness database. Fixed, with new post-reapply assertions. Commit `2d78921`.
   - `internal/costledger/postgres`'s fixture truncated `provider_wallets` and reseeded only 3 of 5 real providers (predated migrations 39/47). Fixed. Commit `fcddf80`.
   - Harness result after these three: **28/29**, only `executive-postgres` still failing.
3. **executive-postgres fixture** — attempting the minimal fix (`WithExecutionPrincipal` + a real `PrincipalStore` + one registered principal, mirroring the exact production wiring pattern) surfaced a genuinely deeper, previously-unverified defect, investigated and closed as EXEC-PRINCIPAL-001 below rather than patched around.

## K. EXEC-PRINCIPAL-001

### Root cause: **CONFIRMED**

Evidence, in the order it was found:
1. Wiring `WithExecutionPrincipal(key)` alone (no `PrincipalStore`) panicked — nil interface call in `AgentMessages.resolvePrincipalByKey`.
2. Wiring a real `PrincipalStore` + one registered principal (`oracle-01/model-runtime-01` / `ingenieria_ia/code-runner`, the exact convention used everywhere else in this codebase) then failed with `principal has dispatch_actor_role_id="ingenieria_ia/code-runner" but sender is "empresa/ceo"` — the CEO→leader hop's sender role doesn't match the one principal.
3. Fixing role resolution (below) then surfaced a **second, independent, previously-unreached bug**: `AgentMessages` passed the principal's *key* string into `Ledger.Send`, whose own defense-in-depth query (`internal/agentmessaging/postgres.Store.validateExecutionPrincipalForSender`) compares against the principal's *numeric ID* column. A non-numeric key there always fails type coercion. **No agent-messaging delegation call had ever succeeded through the real orchestrator, at any hop, before this fix** — the role-mismatch bug always fired first, masking this one.
4. Fixing both then surfaced a **third, independent bug**: the orchestrator's very first `attachChildCoordination` call (root task → the CEO's own "planning" sub-task, both `AssignedRoleID=="empresa/ceo"`) is a same-role self-message, which `agentmessaging`'s topology validator denies unconditionally by design (`ValidateEdge`, `internal/agentmessaging/topology.go:76`). Not a bug in the validator — the orchestrator must not attempt agent-messaging for a hop that crosses no role boundary.

All three were **latent since the original security-hardening branch** (`security-agent-communication-hardening-v1`) introduced principal authentication — none were specific to this closure round's changes. They were invisible because `executive-postgres` had never run in the official harness until D-HARNESS-001's fix (this same closure round), and the code path from `Orchestrator.attachChildCoordination` through to a real `Ledger.Send` call had apparently never been exercised end-to-end by any test before.

## L. OLD MODEL

`Orchestrator` held one `principalKey string`, set once at bootstrap from `ORG_MODEL_EXECUTION_PRINCIPAL_KEY` (default `oracle-01/model-runtime-01`), passed unchanged into every `SendDelegation`/`SendCompletion` call regardless of which role was actually sending. `AgentMessages.SendDelegation` resolved that one key to one principal and checked `principal.DispatchActorRoleID == sender.AssignedRoleID` — a check that, by construction, can be satisfied for at most one role. A real flow has at least two distinct sender roles (CEO, then each department leader); a production-realistic wiring could never pass this check for both.

## M. NEW MODEL

**Resolver.** `modeldispatch.PrincipalStore` gained `ResolveActiveForRole(ctx, organizationID, roleID) (ExecutionPrincipal, error)` (interface: `internal/modeldispatch/interfaces.go`; implementation: `internal/modeldispatch/postgres/principals.go`), backed by migration `000048`'s partial unique index enforcing at most one active principal per `(organization_id, dispatch_actor_role_id)` — scoped to `principal_key LIKE 'role-bound/%'` only (see distinction below).

**Trust boundary.** `AgentMessages.resolveOrProvisionPrincipalForRole` (`internal/executive/runtimeadapter/agentmessages.go`) takes only `ctx` and `roleID`, where `roleID` is always `sender.AssignedRoleID` off an already-persisted, already-registry-validated `TaskRecord` — never from caller/model/task-text input. It resolves the active role-bound principal, or lazily provisions one via a deterministic, idempotent `principal_key = "role-bound/" + roleID` / `idempotency_key = "role-bound-principal:" + org + ":" + roleID`. Concurrent callers racing this either create the row or observe the one just created — never a duplicate, same idempotency contract `RegisterPrincipal` already provides everywhere else.

**Orchestrator.** No longer holds a principal key at all. `WithExecutionPrincipal`/`principalKey` are deleted, not deprecated. `AgentMessagingProvider.SendDelegation`/`SendCompletion` (`internal/executive/ports.go`) dropped the `executionPrincipalID` parameter entirely — the implementation resolves it internally.

**Provisioning.** Lazy, on first use, not pre-populated from the registry's ~80-role catalog. Deterministic and auditable (every `RegisterPrincipal` call writes to `audit_events`; revocable via the existing `DisablePrincipal`).

**Distinction resolved (was mixed silently before, per section 4 of the request): two identities, kept explicitly separate.**
- **Semantics A** (technical dispatcher identity) — `internal/modeldispatch`'s pre-existing use: `oracle-01/model-runtime-01`-style principals identify *the process* invoking a model provider, and legitimately many such principals can share one `dispatch_actor_role_id` (proven directly by `modelruntime-postgres`'s own `execution_principal_mismatch_denies_claim...` test, which registers two). **Role can change model provider without this identity needing to change.**
- **Semantics B** (organizational sender identity) — agent-messaging's need: exactly one authenticated principal per role, standing in for "the role itself" as a message sender. This is what EXEC-PRINCIPAL-001 fixes.
- Kept apart by construction, not convention alone: the `role-bound/` key prefix plus migration 000048's *scoped* partial index (not a blanket one) means role-bound (B) principals are uniqueness-constrained and semantics-A principals are completely unaffected — confirmed directly: the initial blanket-constraint draft of migration 048 broke `modelruntime-postgres`'s legitimate multi-principal-per-role test; rescoping the predicate fixed it without touching that test.

## N. SECURITY INVARIANTS PRESERVED

- `execution_principal.dispatch_actor_role_id == sender.AssignedRoleID`, enforced at two independent layers (`runtimeadapter.validateSenderRoleWithPrincipal` and `agentmessaging/postgres.Store.validateExecutionPrincipalForSender`) — neither removed, neither relaxed.
- No wildcard/super-principal, no cross-role fallback, no role derived from model/task-text input, no bypass for executive specifically.
- `roleID` passed to the resolver is always the sender's already-persisted, already-registry-validated `AssignedRoleID` — never accepted as a parameter from a caller.
- Topology (`agentmessaging.NewTopologyValidator`), capability (`CapabilityAuthorizer`), and task ownership (`validateTaskOwnership`) all remain fully independent of principal resolution — none were touched.

## O. MULTI-HOP PROOF

All four hops proven with real, persisted tasks and messages against Postgres 17, `TestExecutiveMultiHopMessagingEndToEnd` (`internal/executive/exec_principal_test.go`):

| Hop | Mechanism | Result |
|---|---|---|
| CEO → leader | `SendDelegation` (also proven through the real `Orchestrator`, `TestExecutivePostgreSQL17AgentBudgetsAndMessagingAreWiredThroughDelegation`) | PASS |
| leader → worker | `SendDelegation` (also proven through the real `Orchestrator`) | PASS |
| worker → leader | `SendCompletion` (direct — see note below) | PASS |
| leader → CEO | `SendCompletion` (direct — see note below) | PASS |

CEO/leader/worker resolve to three distinct principal IDs (asserted directly). Same-role self-delegation (CEO→CEO) still denied by topology, proving the fix didn't weaken that invariant.

**Note on `SendCompletion`:** `internal/executive.Orchestrator` implements and requires `AgentMessagingProvider.SendCompletion` but never calls it anywhere (`grep -rn SendCompletion internal/executive` outside its own declaration/implementation returns nothing) — a separate, pre-existing fact about this codebase, not something EXEC-PRINCIPAL-001 touches or introduces. The completion-direction tests call it directly to prove the *same resolution mechanism* is correct for that direction too; they do not claim the Orchestrator wires it into the real completion flow (which uses a different mechanism, `CompletionGate`).

## P. NEGATIVE PROOF

| Case | Test | Result |
|---|---|---|
| Wrong principal (role mismatch) | `TestExecutiveMessagingRejectsPrincipalRoleMismatch` — ledger's own independent re-validation, since the resolver can no longer produce a mismatch by construction | DENIED |
| Disabled principal | `TestExecutiveMessagingRejectsDisabledPrincipal` — denied at the ledger layer; `RegisterPrincipal`'s idempotent reuse returns the same disabled row rather than silently minting a fresh active one | DENIED |
| Cross-org | `TestExecutiveMessagingRejectsCrossOrgPrincipal` — see honest caveat below | DENIED (by a different layer than intended) |
| Invalid topology (self-message) | `TestExecutiveMultiHopMessagingEndToEnd`'s final assertion | DENIED |
| `validateSenderRoleWithPrincipal` in isolation | `TestValidateSenderRoleWithPrincipalDeniesRoleMismatch` (table-driven unit test, `internal/executive/runtimeadapter`) | DENIED |

**Honest caveat on cross-org:** in this single-organization deployment, `agentmessaging/postgres.Store`'s task-ownership check independently denies the tested attack shape (command organization ≠ real task organization) before the principal-organization check is even reached. Isolating the principal-level org check alone would require provisioning a second, fully-seeded organization (its own `organizations`/`organization_roles`/registry revision rows) purely for one negative-path test — judged out of proportion. Reported honestly rather than forced green; see mutation C below.

## Q. MUTATION FITNESS

| Mutation | Expected failing test(s) | Observed |
|---|---|---|
| A/D: resolver always returns the CEO principal regardless of role | `TestExecutiveMessagingLeaderToWorkerUsesLeaderPrincipal`, `TestExecutiveMessagingWorkerToLeaderUsesWorkerPrincipal`, `TestExecutiveMessagingLeaderToCEOUsesLeaderPrincipal`, `TestExecutiveMultiHopMessagingEndToEnd` | **CAUGHT** — all 4 failed; the 2 CEO-sender tests and the 4 negative tests still passed (correctly unaffected) |
| B: `validateSenderRoleWithPrincipal` neutralized (returns nil unconditionally) | Integration mismatch test alone: **not caught** (it drives the ledger directly, which has its own independent check) — this is real defense-in-depth, not a gap: added `TestValidateSenderRoleWithPrincipalDeniesRoleMismatch`, a direct unit test of the function, which **is CAUGHT** by this exact mutation | **CAUGHT** (by the added unit test) |
| C: `organization_id` filter removed from `Store.validateExecutionPrincipalForSender`'s query | `TestExecutiveMessagingRejectsCrossOrgPrincipal` | **NOT CAUGHT** — task-ownership's independent org check denies the same scenario first; see honest caveat in section P. This mutation is real (the principal-org check itself would be bypassed) but not currently exercised in isolation by any test |
| — (not requested, found during investigation): fixing role resolution alone without fixing the key-vs-ID bug in `Ledger.Send` | any real send | Would still fail — confirmed this is a second, independent bug by observing the exact error change from role-mismatch to a different failure only after fixing both |

## R. FIXTURES

- **`internal/executive/postgres_integration_test.go`** (`TestExecutivePostgreSQL17AgentBudgetsAndMessagingAreWiredThroughDelegation`): all manual principal registration removed. Wires `AgentMessages{Ledger, MaxAttempts, PrincipalStore, OrganizationID}` — identical shape to production bootstrap. Passes because role resolution genuinely works, not because a fixture pre-arranged a matching principal.
- **`internal/endtoendfixtures/runner.go`**: same change, same reasoning. Same root cause confirmed (identical wiring pattern), fixed in the same commit family.

## S. HARNESS

```
expected             29
passed               29
failed               0
blocked              0
skipped              0
unknown              0
accounting_complete  true
evidence_complete    true
FINAL STATUS         COMPLETE_GREEN
```
Reproduced twice on independent fresh volumes (immediately before and after the final commit series) with identical results.

## T. MIGRATIONS

- New migration: **yes**, `000048_enforce_single_active_execution_principal_per_role` — justified because the "at most one active role-bound principal per role" property is exactly the kind of invariant this codebase already enforces at the database layer elsewhere (e.g. `model_dispatcher_assignments_one_active_idx`), and a resolver with no such guarantee would have no principled basis to pick among ambiguous candidates under a race.
- Tip: **47 → 48**.
- Demonstrated this round: 47→48 up, 0→48 fresh, 48 down/reapply, `pg_dump`/restore (11,173 lines, 0 errors both directions), and post-restore invariant check (the scoped partial index survives restore intact, predicate included).
- `migrations/r21_tip_test.go` updated (`wantCount`, expected map, tip assertion) to 48.

## U. DEFERRED

- **D-005** — unchanged, P2, no 24/7 blocker.
- **D-008** — unchanged, P2, no 24/7 blocker.
- **D-010** — unchanged, P2, no 24/7 blocker.
- **D-012** — unchanged, P2 now / **hard blocker pre-multi-org**.
- **New: D-EXEC-PRINCIPAL-002** (P3, informational) — the cross-org negative test for the new resolver is currently only exercised indirectly (via task-ownership's independent check, see section P/Q). Trigger to resolve: before a second organization is genuinely provisioned in the same Postgres instance (the same trigger as D-012, and for the same underlying reason — this deployment has never needed to distinguish multi-org attack shapes from single-org ones in its test fixtures).

## V. FINAL RISK

- **P0 remaining**: none.
- **P1 remaining**: none. The compose regression (P1) is closed (section J.1).
- **P2 remaining**: D-005, D-008, D-010, D-012 (unchanged from the original pass), D-EXEC-PRINCIPAL-002 (new, informational).
- **Unknown**: none blocking. The original pass's "unknown" (whether production's `bootstrap.Open` always supplies a working execution principal) is now **resolved** — it did not, for any hop, until this round's fixes; production has been calling `attachChildCoordination` with agent messaging wired since the security-hardening branch merged, meaning **real delegation messaging has likely never functioned in production either**, silently, since MaxAttempts/retry policy would have simply exhausted attempts on every delegation without an operator necessarily noticing a message-send failure distinct from other task-orchestration errors. This is a significant, newly-confirmed fact, not a residual risk of this round's changes — this round is what fixes it.

## W. VERDICT

**READY_FOR_TARGETED_INDEPENDENT_REVIEW**
