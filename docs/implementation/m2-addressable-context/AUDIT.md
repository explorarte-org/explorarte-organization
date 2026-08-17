# M2 — Addressable Context + Progressive Disclosure: AUDIT

Status: design-only audit, no productive code changed.
Base commit audited: `73561a8cdf0e1b2480a8b1341e672b5839faf114` (`origin/main`).
Migration tip at audit time: `000053_add_semantic_selector_facts` (CONTEXT-ASSEMBLY-M1.3).

Method note: this audit was produced by four parallel deep-reads of the real
source tree over SSH (contextengine/contextcompiler; executionharness/
modelruntime/modeldispatch; authorization/tasks; rag/memory/skillregistry/
staging/objectstorage) plus direct inspection of `migrations/`, `docs/adr/`,
`docs/canonical/`, and `docs/v2/`. Every claim below is OBSERVED (read
directly in code/docs/schema) unless marked INFERENCE (a reasonable
conclusion not directly stated in the source) or UNKNOWN (could not be
verified in the time available). No claim is invented.

## 1. Executive summary

The repository already contains almost the entire *skeleton* M2 needs:

- A durable, versioned, tiered, content-hashed, capped-size, append-only
  content model (`context_snapshots` / `context_segments`,
  `migrations/000006_create_context_engine.up.sql`) that is structurally
  closer to "addressable context resource" than any of the object/artifact
  stores in the repo.
- A durable, immutable, 1:1-with-snapshot `ExecutionContextView`
  (`internal/contextcompiler`, `migrations/000051_create_execution_context_views.up.sql`)
  that already separates *what a model is authorized to see* (the snapshot)
  from *what was actually rendered for one invocation* (the view).
- A working, provider-independent, mid-execution model-tool-call loop
  (`internal/executionharness/runtime.go`) with exactly the authorization
  discipline M2 needs (tool set frozen into a run-identity digest, replay
  guards, durable-append-before-execute), which is fully built but
  currently wired to **zero tools** in its only production consumer
  (`internal/executive/runtimeadapter/harness.go`).
- A capability-only authorization subsystem (`internal/authorization`) that
  has **no relationship to content/read visibility at all** — the real
  "an untrusted source can never grant authority" enforcement lives
  entirely inside `internal/contextengine` (`MayGrantCapabilities` +
  authority-tier gating), a fact the mission brief's assumed architecture
  does not anticipate.
- An explicit forward roadmap document, `docs/implementation/branch-31-token-context-governance/DESIGN.md`
  (R31, Spanish), whose **Phase 4** already scopes something very close to
  M2 — but *narrowly*, for RAG/memory evidence only ("send compact excerpts
  + recoverable IDs, add an authorized by-ID read to expand detail") — not
  yet implemented, and not labeled "M2" anywhere in the repo (grep for the
  literal string "M2" across `docs/` returns nothing — OBSERVED via the
  contextengine research agent).

No P0 was found that blocks M2 outright. Two P1-class structural risks are
flagged in §11/§12 below (the single canonical-provider-render fallback
path, and the currently-dormant tool-authorization machinery having exactly
one — deny-everything — production wiring, meaning M2 will be the *first*
real exercise of that code path).

## 2. Current architecture map

Real flow (OBSERVED, corrected against the mission brief's flat sketch):

```
Task (tasks.Task, durable Postgres row)
  -> Executive (internal/executive.Orchestrator.driveTypedTask)
       - resolves role-bound execution principal, claims task (lease)
       - resolves DispatcherAssignment (org-revision + subject-role bound;
         internal/modeldispatch) -- a THIRD org-boundary check the brief's
         sketch omits entirely
  -> ContextEngine.Build (internal/contextengine/service.go)
       -> contextengine.Snapshot ("ContextSnapshot"), context_segments
  -> ExecutionHarness (internal/executionharness) -- NOT a pass-through;
       structurally the Executive cannot reach Model Runtime except through
       here (internal/executive/harness_port.go: ModelInvocationReader has
       "no EnsureInvocation, no Create and no Dispatch" by design)
  -> Model Runtime (internal/modelruntime)
       -> contextcompiler.ResolveProviderContext (the ONLY provider-visible
          render algorithm) -> ExecutionContextView (durable, immutable)
       -> modelruntime.PrepareModelInput binds ExecutionContextView's
          rendered bytes as the invocation's stable-prefix message
       -> ProviderAdapter.Dispatch (6 adapters: alibabaclaude, deepseek,
          gemini, mimo, openaicompat, openairesponses)
  -> Provider
```

`InitialContext` as seen by `executionharness.RunSpec` (`internal/executionharness/types.go`)
is an *opaque, digest-verified blob* (`ID, Version, Digest, Content`) —
the Harness does not know it is a `ContextSnapshot`/`ExecutionContextView`
at all; that binding happens one layer down in
`internal/executionharness/modelruntimeadapter/adapter.go`, which parses
`InitialContext.ID` as a `modelruntime` context-snapshot integer ID and
creates a `modelruntime.Invocation` bound to it.

### Ownership per object

| Object | Package/type | Creates | Mutates | Durable? |
|---|---|---|---|---|
| Task | `internal/tasks.Task` | `tasks.Service.CreateTask` | lease/status transitions only; `TaskClass` immutable once created | yes |
| ContextSnapshot | `internal/contextengine.Snapshot` | `contextengine` service `Build` (`service.go:71-147`) | `Invalidate` (ready→invalidated, one-way) and one-time `BindSelectorFacts` COALESCE fill of `task_class`/`execution_purpose`/`actor_unit_id` when NULL | yes |
| context_segments | `internal/contextengine.Segment` | same `Build` call, via `Assembler.Assemble` | none — DB trigger `reject_context_segment_mutation` makes them append-only | yes |
| ExecutionContextView | `internal/contextcompiler.ExecutionContextView` | `ContextAssemblyService.ResolveAndPersist` | none — DB trigger `execution_context_views_immutable`; retry returns the identical row (`SameLogicalView`) or fails closed (`ErrExecutionContextViewDrift`) | yes |
| Model Invocation | `internal/modelruntime.Invocation` | `InvocationService.Create` | status transitions | yes |
| Model Invocation Input | `model_invocation_inputs` (migration 000049) | `PrepareModelInput` | none — append-only, `BEFORE UPDATE OR DELETE` trigger rejects mutation | yes |
| Harness run history | `executionharness.Event` (via `ExecutionHistoryStore`) | `Runtime.Execute` | none — append-only | durability backend UNKNOWN at the interface level (postgres/ subpackage exists but full schema not read in this pass) |

### Where organization_id is actually checked (OBSERVED, concrete comparison sites)

- `internal/authorization/authorizer.go:167` — `request.OrganizationID != a.organization` → deny.
- `internal/authorization/authorizer.go:179` — organization lookup + retired check.
- `internal/authorization/authorizer.go:209` — role's own `OrganizationID` cross-check.
- `internal/contextengine/service.go` `resolve()` — `revision.ID != request.OrganizationRevisionID` hard reject (`ReasonRevisionMismatch`).
- `internal/tasks/contextprovider/provider.go:48` — `detail.Task.OrganizationID != request.OrganizationID` → error.
- `internal/tasks/contextprovider/provider.go:57-58` (annotated `ORG-AUDIT-010`) — `detail.Task.AssignedRoleID != request.ActorRoleID` → error. Comment documents this as a **previously real bug**: "Nothing before this compared them — a caller could combine memory/RAG scoped to one role with instructions/evidence from a task assigned to a different one." Re-checked again in `ValidateVersion` to catch assignee drift after the fact.
- `internal/modeldispatch` assignment resolution — `assignment.OrganizationRevisionID != task.OrganizationRevisionID || assignment.SubjectRoleID != task.AssignedRoleID` (`internal/executive/orchestrator.go:713`).

INFERENCE: this multi-layer, independently-enforced pattern (not a single
choke point) is itself precedent that M2's disclosure path needs its own
explicit, independent org/snapshot-membership check at the point of fetch —
it cannot assume an earlier check in the pipeline already covers it, because
that is exactly the class of bug (`ORG-AUDIT-010`) this codebase has fixed
before.

### Where authorization lives vs where authority-to-read lives (important divergence from the brief)

`internal/authorization` is **pure capability/action authorization** —
`EvaluationRequest{OrganizationID, OrganizationRevisionID, ActorRoleID,
CapabilityID, ResourceType, ResourceID, ActionDigest, ApprovalRequestID}` →
`Evaluation{Effect, ReasonCode, ...}` (`internal/authorization/domain.go:40-60`,
`authorizer.go` `evaluate()` lines 165-250). Default-deny is enforced at
construction (`authorizer.go:101-103` rejects unless
`matrix.DefaultPolicy == "deny"`). `docs/implementation/branch-06-capability-policy-engine/INTEGRATION.md`
states explicitly it "does not accept grants from tasks, prompts, memories,
RAG content, agents, or skills." It never inspects context/content
visibility.

The actual "no source can escalate its own authority" enforcement lives in
`internal/contextengine`:
- `internal/contextengine/validation.go` `ValidateSourceMetadata` —
  `MayGrantCapabilities` can only be true for `AuthorityPriority` 1 or 2
  (owner_decisions / canonical_registry_and_policies); priority 0
  (immutable_safety) can never grant capabilities either.
- `internal/contextengine/assembler.go` — approved-memory, RAG-evidence, and
  web-evidence source kinds are hard-rejected unless
  `InstructionClass==InstructionData && TrustClass==TrustUntrusted &&
  !MayGrantCapabilities`.
- `internal/tasks/contextprovider/provider.go` `sourceRecord()` — task
  content is always constructed `InstructionClass: InstructionScoped,
  TrustClass: TrustUntrusted, DataClass: DataOrganizational,
  MayGrantCapabilities: false` regardless of what the task's own text says.
- DB-level backstop: `context_segments` CHECK constraints (migration
  000006) — `may_grant_capabilities` can only be TRUE at
  `authority_priority IN (1,2)`; `source_kind IN
  ('approved_memory','rag_evidence','task_context')` can never set it;
  `source_kind IN ('approved_memory','rag_evidence')` is forced to
  `instruction_class = 'data'` and `trust_class = 'untrusted'`.

**This is the mechanism M2's "progressive disclosure cannot increase
authority" principle must extend, not `internal/authorization`.**
`internal/authorization`'s decisions are irrelevant to whether a *fetched
piece of content* can act as an instruction — that's a contextengine-tier
concept.

### Where provenance lives

Per-segment: `AuthorityTier`, `AuthorityPriority`, `SourceKind`,
`SourceReference`, `SourceVersion`, `InstructionClass`, `TrustClass`,
`DataClass`, `MayGrantCapabilities`, `Included`/`OmissionReason`,
`ContentHash` (`context_segments`, migration 000006). Per-view: `SelectionKind`,
`SelectorAlgorithmVersion`, `FellBackToCanonical`/`FallbackReason`,
`SegmentDiffs` (migration 000051/000053). Selector provenance is
deliberately **not duplicated** — `ExecutionContextView` does not re-store
`TaskClass`/`ExecutionPurpose`/`ActorUnitID`; those live only on the
canonical `Snapshot` row (comment in `contextcompiler_view.go`: "Neither
duplicates the selector facts themselves... see M1.3 section 14's 'do not
duplicate large selector payloads' instruction").

### Where tokens are counted / provider usage recorded

Two genuinely distinct, explicitly-never-conflated concepts:
- `ContextTokenTelemetry` (`internal/contextcompiler/contextcompiler_telemetry.go`) —
  a **host-side, deterministic byte-count estimate**
  (`EstimatorID="context_utf8_bytes_ceiling"`, `ceil(bytes/3)`), explicitly
  documented as never to be conflated with a provider's own reported
  `InputTokens`. Persisted as additive nullable columns on the
  pre-existing `model_invocation_render_telemetry` table (migration
  000052), gated by an all-or-nothing CHECK constraint
  (`_token_estimator_complete`) so a partially-populated M1.2 row is
  impossible at the DB layer.
- Actual provider `Usage` is recorded separately inside `modelruntime`
  (per-invocation, via the dispatch/costgate path) — not read in full
  detail in this pass, but confirmed distinct in principle and in the
  telemetry doc comments (OBSERVED via research agent quoting the doc
  comment verbatim).

Stable/dynamic partition is a **closed, single-sourced set**:
`contextengine.IsDynamicProviderTier` = `{TierTask, TierProject,
TierRAGEvidence, TierApprovedMemory, TierApprovedSkill}`; everything else
is "stable" (actor/role/policy-scoped, byte-identical across invocations).
A real prior bug is documented and fixed here: `contextcompiler`'s
telemetry used to independently redeclare a narrower version of this same
rule (only `TierTask`), silently diverging from the render partition —
R31 hardening fixed it by making both call the one function. **This is
direct precedent for the M2 rule "never redeclare a classification that
already has one canonical source."**

## 3. Ownership map

| Concern | Real owner | Notes |
|---|---|---|
| Canonical context authorization universe | `internal/contextengine` (Snapshot + Segment) | organization_id, actor_role_id, org_revision_id, authority tiers |
| Provider-visible rendering | `internal/contextcompiler` (ExecutionContextView) + `internal/contextengine.BuildProviderRenderV2` | single algorithm (`ResolveProviderContext`), byte-identical across all callers |
| Action/capability authorization | `internal/authorization` | org+revision+role+capability+resource+action_digest; no content awareness |
| Task identity / TaskClass | `internal/tasks` | `TaskClass` immutable, classification only, never authority |
| ExecutionPurpose | `internal/executive` (`harness_port.go`), closed enum, NOT a Task field | distinct from Task's freeform TaskClass and from contextengine's legacy free-text Purpose — three "purpose" concepts deliberately kept separate |
| Model-invocation input framing | `internal/modelruntime` (`ModelInputEnvelope`) | binds stable-prefix message to `ExecutionContextView`'s exact rendered bytes/hash |
| Mid-execution tool-call loop | `internal/executionharness` (`Runtime.Execute`) | provider-independent, fully built, currently zero tools wired in production |
| Provider transport | `internal/modelruntime/adapter/*` (6 adapters) | must not be touched by M2 |
| Cost reservation | `internal/modelruntime/costgate` | must not be touched by M2; runs automatically for any new model turn a tool call triggers |
| Approved memory | `internal/memory` | evidence-backed lesson store, own admission/lifecycle semantics, feeds contextengine via `contextprovider` |
| Approved RAG knowledge | `internal/rag` | chunked/embedded knowledge documents, namespace = role/department resolved server-side, feeds contextengine via `contextprovider` |
| Skill lifecycle | `internal/skillregistry` | governs lifecycle/assignment only; content lives outside the DB (path+SHA256 reference to git) |
| Code-review artifact promotion | `internal/staging` + `artifactfs` | `Artifact` here means manifest/patch/check-report/review-report only |
| PDF-ingestion evidence blobs | `internal/objectstorage` | narrow OCI client for one pipeline, content-addressed keys, consumed only by `rag/bootstrap` |
| Batch engineering execution | `internal/coderunner` | closed operation enum, no dynamic tool registry, architecturally unrelated to the Harness tool loop |

## 4. Existing reusable primitives

- `contextengine.SourceRecord` — the common currency every context
  provider (`tasks/contextprovider`, `memory/contextprovider`,
  `rag/contextprovider`) already renders into, carrying authority tier,
  instruction class, trust class, data class, and
  `MayGrantCapabilities`. M2's `ContextResource`-equivalent should almost
  certainly be built as a natural extension of this shape, not a parallel
  one (see DESIGN.md §6/§14).
- `contextengine.IsDynamicProviderTier` — the single source of truth for
  stable/dynamic classification; M2 telemetry must call this, never
  re-derive it.
- `contextcompiler.ResolveProviderContext` — the single provider-render
  algorithm; any M2 "what did the model actually see initially" telemetry
  must derive from its output, not a second computation.
- The M1.3 legacy-marker migration pattern (migration 000053): nullable +
  NOT backfilled when NULL is an honest "predates this feature" fact;
  NOT NULL + backfilled with a distinct, separately-versioned legacy tag
  only when the backfill is a faithful reconstruction of already-observed
  behavior (`fell_back_to_canonical` → `selection_kind`). This is the
  exact template M2's own historical-compatibility migration should follow
  (see DESIGN.md §19/§20).
- `internal/executionharness`'s tool-call loop primitives: run-identity
  digest freezing the declared tool set, `ToolCatalog.Lookup` +
  `sameToolDefinition` catalog-drift check, replay guard on `ToolCallID`,
  `MaxToolCalls` budget, durable-append-before-execute ordering. This is
  the correct place to hang `context.fetch`/`context.search` as a
  first-class tool (see DESIGN.md §9/§11).
- `artifactfs`'s content-addressed local blob store mechanics (SHA-256
  sharded path, atomic hardlink publish, corruption/symlink checks) —
  reusable *code*, but currently only instantiated for `staging`'s four
  narrow artifact kinds; would need its own wiring, org scoping, and authz
  if reused for M2 (it has none of those today).

## 5. Existing stores and what they actually own

(Full detail in the research pass; summarized here.)

- **`internal/objectstorage`** — thin OCI Object Storage HTTP client for
  the PDF-ingestion evidence pipeline only. Content-addressed keys
  (`raw/<sha256>.pdf`, `manifests/parser-runs/...`, `pages/<sha256>/...`).
  Only callers: `internal/rag/bootstrap` and `cmd/orgctl/objectstorage.go`.
  No org/authz/versioning concept of its own.
- **`internal/staging` + `artifactfs`** — a git-workspace code-review
  promotion engine (`WorkspaceStatus`, `PromotionStatus`, `Check`,
  `Review`, `Promotion`, `GitBackend`). `Artifact` here is narrowly
  manifest/binary_patch/check_report/review_report, addressed by SHA-256
  digest. Not a general content store.
- **`internal/memory`** — evidence-backed organizational lesson store
  (`Entry{Category, Problem, Correction, SourceKind, EvidenceRefs, Status,
  AdmissionAttestation, SupersedesEntryID, Revision}`). Admission
  explicitly rejects `DataSecret`/`DataClinical` before an entry can even
  become a candidate. Feeds `contextengine` via `memory/contextprovider`,
  strictly scoped to `entry.OrganizationID == request.OrganizationID &&
  entry.RoleID == request.ActorRoleID && entry.Status == StatusApproved`,
  always rendered as `TierApprovedMemory` / `InstructionData` /
  `TrustUntrusted` / `MayGrantCapabilities:false`.
- **`internal/rag`** — approved-knowledge document store with real
  chunking/embedding (`KnowledgeDocument → KnowledgeVersion → Chunk`,
  optional `MediaSourceRef` pointing at an `objectstorage` key for PDF
  pages — the one genuine rag↔objectstorage coupling).
  `NamespaceKind` (`own`/`department`) resolved server-side only via
  `rag/roles/resolver.go`, never accepted as free text. `IndexGeneration`
  tracks atomic reindexing. `rag/contextprovider` **silently swallows**
  `ErrCapabilityDenied`/`ErrApprovalRequired` as "no results" rather than
  surfacing an error — a design choice M2 must not blindly copy for a
  disclosure-audit path (see AUDIT §11).
- **`internal/skillregistry`** — governs skill *lifecycle and assignment*
  only (`draft→human_approved→candidate→active→suspended→retired`).
  Content lives outside the DB — only a `SourceRecord{Path, SHA256,
  Origin}` reference is stored (git-backed, internal or GitHub).
- **`internal/contextengine`** itself — `context_snapshots` /
  `context_segments` are already the closest existing thing in the repo to
  "addressable context resources": versioned, tiered, content-hashed,
  size-capped (≤1 MiB per segment), append-only. This is the piece the
  mission brief's candidate list (`objectstorage`, `staging`, `artifacts`,
  `memory`, `rag`) did not name but that turned out to be the real
  semantic owner of addressable, disclosure-eligible content identity.

## 6. Current authorization boundaries

Two structurally separate boundaries exist today, and M2 must keep them
separate (see DESIGN.md §10):

1. **Action authorization** (`internal/authorization`) — "may this actor,
   in this org/revision, perform this capability against this resource."
   Never touches content.
2. **Context/content authority** (`internal/contextengine`) — "may this
   piece of content be included, and can it instruct or merely inform."
   Enforced via `AuthorityTier`/`AuthorityPriority`/`MayGrantCapabilities`
   at both Go-validation and DB-CHECK layers.

M2 introduces a third, narrower boundary: "may this specific
already-admitted-into-the-snapshot resource be *disclosed* to this
specific invocation, right now." DESIGN.md argues this should be a
capability-scoped *view* over boundary #2's existing membership, not a
new independent authorization surface, and definitely not routed through
boundary #1 (which has no content concept to check against).

## 7. Current telemetry boundaries

- `ContextTokenTelemetry` (M1.2) — one row per `(model_invocation,
  execution_context_view)`, host-side byte-based estimate of what the
  *provider actually received as the initial prompt*. Never claims to be
  provider-reported usage.
- `ProviderRenderTelemetry` (`internal/modelruntime/interfaces.go`) — a
  narrower, per-dispatch struct (`Version, FallbackToLegacy,
  FallbackReason, StablePrefixHash/Bytes, DynamicSuffixHash/Bytes,
  ProviderRenderHash, ProviderVisibleBytes`), explicitly best-effort to
  write ("a cache/render-telemetry concern is never a correctness gate").
- Actual provider `Usage` (input/output token counts as reported by the
  provider) is recorded elsewhere in `modelruntime`/`costgate` — distinct
  again, and never to be confused with either of the above.

There is currently **no concept of "dynamic context read after initial
prompt"** anywhere in telemetry — M1.2 only measures what went into the
*first* rendered view. M2 must add a parallel, clearly-labeled dimension
(DESIGN.md §15), not fold disclosure reads into the existing
`ContextTokenTelemetry` row (which is 1:1 with one `ExecutionContextView`,
itself 1:1 with one `ContextSnapshot` — a snapshot is created once per
task-context-build, not once per disclosure read).

## 8. Current artifact/object-storage architecture

Per §5: three narrow, purpose-built stores (`objectstorage`,
`staging`/`artifactfs`, and skill content living in git, not Postgres) —
none is a general-purpose "artifact/blob" platform service. `docs/v2/architecture-delta-v1-to-v2.md`
explicitly states object storage is "supporting infrastructure, not an
autonomous capability" and demotes it deliberately in the V1→V2 design.
The only genuine `type Artifact struct` in the whole repo is
`internal/staging/domain.go` (code-review promotion byproducts).
`internal/improvement/types.go`'s `ArtifactRef` is a separate,
narrower concept — an opaque content-addressed *pointer* the
self-improvement subsystem uses, explicitly not owning storage itself.

**Conclusion relevant to DESIGN.md §N (Artifact store)**: there is no
existing "Context Artifact Store" and none of the three existing stores is
a good fit by domain semantics (code-review promotion, single-pipeline PDF
evidence, or governance-lifecycle-with-external-content). The closest
existing durable, versioned, capped, content-hashed store *is*
`context_segments` itself.

## 9. Current Harness/model-tool execution path

Fully described in §2 and §4. Key facts to repeat here because they are
the crux of the M2 feasibility case:

- The mechanism already exists end-to-end and is provider-independent:
  `executionharness.Runtime.Execute` → tool request → catalog/authorize/
  replay-guard/budget checks → durable append → `ToolExecutor.Execute` →
  durable append of result → next turn's `Project()` surfaces the result
  in `VisibleHistory` → `modelruntime.ModelInputMessage{Role:
  ModelInputRoleTool}` → provider.
- Tool declarations already flow end-to-end through all six provider
  adapters (`ToolDefinitions` in, `RawToolIntent` out) — confirmed
  concretely in `openaicompat/adapter.go`.
- **It is currently wired to zero tools in the only production consumer.**
  `internal/executive/runtimeadapter/harness.go`'s
  `executiveToolCatalog{}`/`executiveToolExecutor{}` always deny/error,
  `RunPolicy{MaxTurns:1, MaxToolCalls:0}`, `Tools: nil`. Comment: "Executive
  typed tasks have never allowed a model-selected tool... If it is ever
  entered, something upstream stopped denying tool intents."
- `docs/implementation/v2-harness-model-runtime-integration-001/INTEGRATION.md`
  explicitly lists "Context Assembly V2, Memory OS, compaction,
  **programmatic tool execution**, and Workflow completion bridging" as
  known-out-of-scope gaps for that slice — i.e. the codebase's own authors
  already flagged this as the next piece of work, and it maps directly
  onto M2.
- `InitialContext` in `RunSpec` is frozen at run start (digest-verified);
  it does not get replaced or extended mid-run. Any M2 disclosure content
  can therefore only arrive as **tool results appended to VisibleHistory**
  — this is an important design constraint, not a limitation to work
  around (see DESIGN.md §9).
- `CodeRunner` (`internal/coderunner`) is a superficially similar but
  architecturally unrelated system: a closed-enum, no-dynamic-registry
  batch task executor, zero coupling to `executionharness`. Not the
  extension point for M2.

## 10. Historical compatibility constraints inherited from M1.x

- `context_snapshots.task_class/execution_purpose/actor_unit_id`
  (migration 000053) are nullable and **deliberately not backfilled** —
  NULL is treated as an honest fact ("this snapshot predates semantic
  selector facts"), never reinterpreted.
- `execution_context_views.selection_kind/selector_algorithm_version` ARE
  backfilled, but only because the backfill is a *faithful reconstruction*
  of already-observed behavior (`fell_back_to_canonical` boolean directly
  implies whether the historical resolution behaved like a `task_class`
  match or a `canonical` fallback), and the backfilled rows are tagged
  with a distinct, separately-versioned marker (`legacy_task_class_of/v0`)
  so they can never be confused with a genuine M1.3-era selection.
- `tasks.task_class` similarly got a one-time, narrowly-scoped backfill
  (`legacy.unspecified` → `research.corpus_curate` for two specific known
  roles) explicitly documented as "a one-time data migration, not a
  runtime classifier."
- `model_invocation_render_telemetry`'s M1.2 columns are nullable and
  all-or-nothing (CHECK constraint), so historical R10.4-only rows remain
  valid with the new columns simply NULL.
- `ExecutionContextView` immutability + `SameLogicalView`/`ErrExecutionContextViewDrift`
  guarantees a retried resolution of the same snapshot never silently
  diverges — this must extend to M2: a retried disclosure of the same
  handle must return byte-identical content or fail closed, never drift.

**This is the load-bearing precedent M2's own migration design must
follow exactly** — see DESIGN.md §19/§20.

## 11. Technical risks

- **R-1 (P1).** The provider-render path has exactly one deterministic
  algorithm today (`ResolveProviderContext`), but it has an explicit
  fallback branch to a legacy `PortableRenderer` on any compile failure or
  profile-registry miss. If M2's disclosure mechanism ever needs to be
  aware of "what was actually rendered," it must key off
  `ExecutionContextView.FellBackToCanonical`/`ProviderRenderVersion`
  rather than assume the V2 renderer path was used — a historical or even
  contemporaneous view could be a fallback render with different framing
  guarantees (e.g. the untrusted-data wrapping markers).
- **R-2 (P1).** The mid-execution tool-call loop is fully built but has
  **never been exercised in production with a non-empty tool set**. M2
  will be the first real load-bearing use of `ToolCatalog`/`ToolExecutor`/
  the replay-guard/budget/durable-append-before-execute path. This is not
  a defect, but it means M2 cannot assume that path is battle-tested —
  TEST_PLAN.md must cover it as if it were new code, because in the
  load-bearing sense, it is.
- **R-3 (P2).** `rag/contextprovider` silently swallows
  `ErrCapabilityDenied`/`ErrApprovalRequired` as "no results" during
  *initial* context assembly. If M2's `context.search` reuses this
  pattern for a disclosure-time query, a denied capability would look
  identical to "nothing matched" in the model's eyes — acceptable for
  initial assembly (fail-quiet is arguably intentional there) but a
  DESIGN.md decision is needed on whether disclosure-time search should
  behave the same way or should surface FORBIDDEN distinctly for audit
  purposes (see DESIGN.md §17, TEST_PLAN.md category B/J).
- **R-4 (P2).** Three separate "purpose"/identity-like concepts already
  coexist by design (Task.TaskClass, Executive's ExecutionPurpose,
  ExecutionPrincipal in modeldispatch vs RunIdentity.ExecutionPrincipalID
  in the harness vs the lease-holder canonical execution principal). A
  fifth "who is asking" concept for M2 disclosure calls must reuse one of
  these existing identities via FK, never mint a sixth.
- **R-5 (P2).** `context_segments.content` is capped at 1 MiB and
  `execution_context_views.provider_visible_bytes` at 8 MiB (DB CHECK
  constraints, migration 000006/000051). Any M2 addressable resource
  larger than these caps (e.g. a large RAG document body, before
  chunking) needs its own size discipline defined explicitly — it cannot
  silently reuse `context_segments`' row shape unchanged.

## 12. Architectural blockers

None found that block M2 design work outright (no P0). The nearest thing
to a blocker is R-2 above: M2 is not extending a proven capability, it is
completing one that was deliberately built ahead of any real consumer.
This changes risk posture (favor conservative, narrowly-scoped
implementation slices — see DESIGN.md §27) but does not block design.

## 13. Things that looked reusable but should NOT be reused

- **`internal/objectstorage`** — despite the name, this is not a general
  blob store; it is hard-coded to one pipeline's key scheme
  (`raw/<sha256>.pdf`, `pages/<sha256>/...`) and has no org/authz concept.
  Reusing it as "the" M2 resource store would silently inherit a PDF-
  ingestion-shaped API and zero access control.
- **`internal/staging`'s `Artifact` type** — semantically means
  "code-review promotion byproduct." Reusing the same name/type for M2
  disclosure resources would create exactly the "ContextStore/
  ArtifactStore/ObjectStore/MemoryStore/RAGStore all storing similar blobs
  with no clear ownership" anti-pattern the mission brief warns against.
- **`internal/coderunner`'s operation model** — superficially similar
  ("closed set of operations a model-driven process executes") but a
  batch, non-interactive execution model with no dynamic registry by
  design; not a template for `context.fetch`/`context.search`.
- **`rag/contextprovider`'s silent-denial pattern** — reasonable for
  *initial* snapshot assembly (a role simply doesn't see department RAG it
  can't access, no error needed) but risky to copy verbatim into a
  disclosure-time audit path where FORBIDDEN needs to be distinguishable
  from NOT_FOUND for the failure-semantics requirement (mission brief
  §L).
- **`ExecutionPrincipal` (modeldispatch)** — do not treat this as "the"
  principal to attribute a disclosure call to; it is a *technical
  dispatcher* identity, explicitly documented as carrying no context or
  memory. The correct principal to bind a disclosure event to is whatever
  identity already gates tool-call authorization in
  `executionharness` (`RunIdentity`/lease holder) — reuse via FK, do not
  reintroduce `ExecutionPrincipal` at the disclosure layer.

## 14. Recommended M2 boundary

M2 should be a new, narrowly-scoped domain package
(`internal/contextdisclosure` or similar — naming is DESIGN.md's call, not
this audit's) that:

- **Owns**: addressable-resource identity, handle format, disclosure
  events, disclosure-scoped telemetry, and the `ToolCatalog`/
  `ToolExecutor` implementation that plugs into the *already-built*
  `executionharness` tool-call loop.
- **Reads, but does not fork**: `context_snapshots`/`context_segments` as
  the authorized universe (extending it additively — see DESIGN.md §6),
  `contextengine.IsDynamicProviderTier` and
  `contextcompiler.ResolveProviderContext` for classification/render
  facts, `internal/authorization` only if a genuinely new *capability*
  (e.g. "may perform context.search at all") is warranted — not for
  content-membership checks, which belong to contextengine's own model.
- **Does not touch**: `internal/modelruntime/adapter/*`,
  `internal/modelruntime/costgate`, `internal/modeldispatch`,
  `internal/authorization`'s decision engine, `internal/coderunner`,
  `internal/staging`, `internal/objectstorage`'s key scheme.

Full detail in `DESIGN.md`.
