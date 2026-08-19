# V2 Grok Adversarial Review 001

Base: `62961162de44a86c0e504daa976b01269c6fd097` (tag `v2-program-base-6296116`)
Branch: `feat/v2-grok-adversarial-review-001`
Worktree: `/opt/explorarte/organization-v2-grok-review`

Introduces a second epistemic perspective between a candidate design and a
frozen one, and keeps the three positions apart:

    designer  (ingenieria_ia/orquestador, DeepSeek V4 Pro)
      != reviewer    (investigacion/revisor_adversarial, xAI Grok 4.6)
      != adjudicator (empresa/ceo, Luna)

This slice ends at design freeze. Nothing here authorizes implementation.

## Reviewer role

`investigacion/revisor_adversarial`, `authority_class: transversal_audit`,
`runtime_kind: auditor_agent`, `model_policy: research.adversarial_review`,
unit `investigacion`, reporting to `empresa/ceo` and `empresa/human`.

It is a new role rather than a reuse of `investigacion/auditor_cerebro_empresa`
so that "audit of the brain" and "adversarial review of a candidate design"
stay durably distinguishable without inferring the difference from the
provider.

Status is `proposed_profile_required`, so the parser derives `enabled=false`
and `executable=false` and its role binding is materialized inactive. That is
the correct pre-activation state, not an oversight — see Activation below.

## Authority boundary

Granted (via `transversal_audit`): `organization.read_registry`,
`audit.read_sanitized_evidence`, `audit.publish_finding`, plus the class's
existing `rag.read_department`, `rag.propose_candidate`, `memory.read_own`.

Hard-denied explicitly: `code.stage_write`, `code.commit`,
`deployment.request` (pre-existing) and now also `task.assign_worker`,
`task.review`, `memory.approve`, `model.dispatch_assignment.create`,
`model.dispatch_assignment.revoke`.

`task.review` is denied by explicit decision. The reviewer publishes findings;
it does not close the epistemic question. Adjudication belongs to
`empresa/ceo`.

`model.invoke` is untouched — canonical validation already restricts it to
`execution_service`, so granting it here would fail the registry outright.

The five newly denied capabilities were never granted. They were absent under
default-deny, which is not a testable assertion; a hard deny is, and it stops
the boundary from eroding if the grant list grows later.

## Routing

    investigacion/revisor_adversarial
      -> research.adversarial_review      (docs/canonical/model-routing.yaml)
      -> profile research.adversarial_review
      -> provider xai, transport http_adapter
      -> internal/modelruntime/adapter/xai

There is no `if purpose == review { use grok }` anywhere. Grok is a
consequence of the binding. Two tests enforce this: one walks the chain from
role to provider, another greps `internal/executive` for provider literals
and fails if any appear.

## Grok 4.6 target family and the unresolved model id

Target family: **Grok 4.6**. Provider: `xai`.

`provider_model_id` is **`UNRESOLVED_PENDING_XAI_PREFLIGHT`**.

xAI publishes `grok-4.6` as a family id with no dated snapshot, and the id
actually exposed to this account cannot be confirmed without a provider call,
which this slice does not make. The canonical document therefore carries a
value that cannot dispatch anywhere rather than a plausible guess that could.
`decision_status: owner_confirmation_required` records that it is unsettled.

Resolving it requires a separate preflight against `/v1/models`.

## Egress and the review bundle

`xai` is allowed `public` and `sanitized`. It is allowed nothing else:
`organizational` is absent from the canonical policy AND from both compiled
allowlists (`registry.productiveEgressAllowRules`,
`modelegress.ProductiveLoadOptions`), so an allow rule for xai/organizational
fails canonical validation rather than depending on anyone remembering the
rule. `clinical` and `secret` remain global hard denies.

`ScopeAdversarialReview` is derived only from durable context whose actor role
is `investigacion/revisor_adversarial`. `xai` requires that scope
unconditionally — including for public data — and `singleProviderTest` does
not widen it. The mirror also holds: `deepseek` and `openai_compatible` refuse
the reviewer's scope, so a silent fallback of the adversarial review onto the
design author's own model is denied at the egress boundary rather than by
convention.

The reviewer's input is a closed-field `designreview.Bundle`: owner
requirements, candidate design, architecture constraints, authority
constraints, unresolved decisions, authorized evidence refs, design identity.
Seven keys, nothing else. It is a closed list rather than a redaction pass so
it fails safe by construction.

## Contracts

`adversarial-review/v1` — verdict `accept|revise|block`, findings with
`id`/`severity`/`claim`/`affected_requirement`/`required_correction`/
`evidence_refs`, plus contradiction, assumption, security, authority,
recovery and memory-epistemic lists.

It has **no `proposed_followup_tasks`** and no approval field. The shared
decoder disallows unknown fields, so a result carrying them is refused rather
than ignored, and the existing forbidden-key scan already rejects a reviewer
that names a provider or asserts an approval. Verdict and findings must agree:
`revise`/`block` with no findings is refused, and `accept` alongside a
critical or high finding is refused.

`design-adjudication/v1` — verdict `freeze|revise|reject`, accepted/rejected
finding ids, required changes, unresolved owner decisions, and the design
identity echoed back. The host compares that echo against its own record; a
mismatch raises `ErrDesignIdentityMismatch`, a distinct sentinel, because it
is not a malformed contract but a correct-looking verdict about the wrong
artifact. A `freeze` carrying required changes or unresolved owner decisions
is refused.

The review contract has no identity echo on purpose: it is bound to its design
by the host through the durable task, never by what the model repeats back.

Both schemas stay inside the JSON-Schema subset Model Runtime accepts and that
xAI's structured outputs will not reject — no `pattern`, no `const`, no
`$ref`, and never `items` as an array (xAI returns 400 for that shape). A test
walks both schemas and enforces it.

## Freeze semantics and digest binding

`internal/designfreeze` is a pure decision — no ports, no storage, no clock —
so the same inputs always produce the same verdict and a stored freeze can be
recomputed from durable evidence later.

Requirement key: `design-freeze` (hyphens; `internal/tasks` rejects dots in
requirement keys). No new table: it is expressed as an existing task
requirement of type `approval` plus evidence.

Satisfied only when all five hold at once:

1. a valid design identity (`design_id`, `design_version`, lowercase sha256);
2. a durable adversarial review of **that** design;
3. a durable adjudication of **that** design;
4. adjudication verdict is exactly `freeze`;
5. all three identities agree.

Two properties are easy to get backwards and are pinned by tests:

- the reviewer holds **no approval** — an `accept` verdict freezes nothing;
- the reviewer holds **no veto** — a `block` verdict does not stop a freeze
  adjudication. The reviewer informs the decision; it does not rule.

The freeze binds to bytes, not to a label. `Record.Digest` hashes the whole
binding, so a record edited afterwards stops verifying, and a freeze earned by
digest A never authorizes digest B — including the case where the label is
unchanged and only the bytes moved.

## Provider unavailable behaviour

While `ORG_MODEL_PROVIDER_XAI_ENABLED` is false, `xai` is simply absent from
the adapter registry. The adversarial review fails closed with
`GROK_REVIEW_UNAVAILABLE=provider_not_configured`; the adjudication does not
run and no freeze is recorded. There is no code path that retries the review
against DeepSeek or anything else.

## Activation checklist (none of it done here)

1. Resolve the exact Grok 4.6 model id exposed to this account and replace
   `UNRESOLVED_PENDING_XAI_PREFLIGHT` in `model-routing.yaml`.
2. Provision the credential file (mode 0600, outside the repository) and set
   `ORG_MODEL_PROVIDER_XAI_*`. Preflight must check existence, ownership, mode
   and non-emptiness without printing, hashing or logging the contents.
3. Seed xAI pricing; without it, cost gating blocks activation by design.
4. Flip the reviewer role to an enabled status in `role-catalog.yaml` and
   adjust `counts.proposed_profiles`.
5. Attach the campaign budget ceiling (USD 5 total) — not attached here, no
   DB budget was touched.

## Deferred

- **Executive -> engineeringmission seam.** The Executive still does not
  import `internal/engineeringmission`; nothing converts a plan step into a
  `MissionPolicy` plus a validated `code-runner-execution/v1` plan.
- **Principal/lease identity for engineering missions.** The orchestrator
  claims with `WorkerID = "executive-orchestrator"` and no holder principal,
  correct only while it runs no Harness work.
- **`AuthorityUnavailable` autonomous retry.** The Harness reports
  `Retryable`; nothing schedules the re-entry.
- **Triggering `designreview.Coordinator.Run` from the Executive run state
  machine.** The coordinator is complete and tested; the orchestrator does not
  call it yet.
- **Exact Grok 4.6 model id, credential activation and a real provider smoke.**

## Known pre-existing defect, unrelated to this slice

`scripts/check-model-runtime-fitness.sh` already failed at the base commit.
`mimo` had been added without being listed in its network-client and
adapter-directory allowlists, so the script aborted before reaching its
compiled-availability assertion. That is repaired here, because adding `xai`
without the repair would have left the new entry unverified.

A second failure remains and is **not** fixed here: the script forbids
`cmd/orgd` from importing `internal/modelruntime`, and `cmd/orgd/main.go`
already does so at the base commit for `modelruntime.BuildSHA`. It is
untouched by this branch and out of scope.
