# M2 — Addressable Context + Progressive Disclosure: DESIGN

This is a design proposal, not an implementation. No code, schema, or
migration in this repository is changed by this document. All claims about
current behavior are as audited in `AUDIT.md`; this document is DECISION +
RATIONALE for what M2 should become, built strictly on top of the M1/M1.1/
M1.2/M1.3 guarantees audited there.

## 1. Problem statement

Today, everything the Context Engine decides to include in a
`ContextSnapshot` is either inlined into the rendered `ExecutionContextView`
or silently omitted (`context_segments.included=false`,
`omission_reason` set). There is no way for an execution to see a *reference*
to omitted or not-yet-fetched content and pull it in only if actually
needed. This means every task-relevant piece of content pays its full byte/
token cost up front, whether or not the model ever uses it — and it means
the *only* lever the system has for "don't blow the budget" is "leave it
out entirely," not "make it optionally reachable."

## 2. Goals

- Let an execution receive a compact initial context and discover/retrieve
  additional, already-authorized content mid-execution via explicit,
  bounded, audited host-mediated calls.
- Preserve every M1/M1.1/M1.2/M1.3 guarantee: canonical/durable
  `ContextSnapshot`, derived/durable/immutable `ExecutionContextView`,
  deterministic selector-precedence resolution, selection provenance,
  token telemetry (including the never-conflate-estimate-with-provider-
  usage rule), stable/dynamic prefix telemetry, provider-visible view
  construction, and historical idempotency compatibility.
- Structurally prevent progressive disclosure from becoming a bypass of
  Context Assembly's rules: **a disclosure call can never return content
  that the originating ContextSnapshot did not already authorize.**
- Measure dynamic-context usage as a new, clearly-separated telemetry
  dimension.
- Reuse the already-built `executionharness` tool-call loop as the
  transport, rather than inventing a parallel host-capability mechanism.

## 3. Non-goals (explicit)

Model weight changes; autonomous self-modification; full Memory OS
(consolidation/sleep/reinforcement/semantic promotion/agent
self-modification); new RAG embedding architecture or BGE/Gemini strategy
changes; provider billing/cost-reservation/admission-control redesign;
task scheduler redesign; `internal/authorization` redesign; principal
redesign; `internal/coderunner` redesign; arbitrary filesystem/network
access for a model; MCP redesign; skill-registry redesign; deployment
changes; reserving or writing an actual migration file; running any
migration against any database (including disposable ones); mid-execution
*semantic re-querying* of RAG (see §22 — deferred, not designed here beyond
naming the seam).

## 4. Invariants

These MUST hold for any M2 implementation:

- **I-1 (authority ceiling).** No `context.fetch`/`context.search`-
  equivalent call may return content whose `(organization_id,
  context_snapshot_id, source membership)` was not already established at
  `ContextSnapshot` build time. A handle is a reference into an
  already-decided authorization universe, never a new grant.
- **I-2 (server-side validation only).** Authority for a disclosure call is
  always re-derived server-side from storage (snapshot membership, org,
  resource version/digest) — never trusted from fields the handle itself
  claims, even if those fields are cryptographically opaque to the model.
- **I-3 (immutability of what's read).** A given resource identity
  (handle + version) always returns byte-identical content, or the read
  fails closed. No "the document changed under you" silent drift.
- **I-4 (no instruction escalation, scoped to evidence-only content).**
  M2's addressable universe in this milestone is restricted to
  evidence/data-kind sources only (§4B) — the same three kinds
  `contextengine.Assemble` already forces to
  `InstructionClass==InstructionData`/`TrustClass==TrustUntrusted`/
  `MayGrantCapabilities==false` when inlined. For exactly that restricted
  set, "a dynamically-fetched resource carries the same
  trust/instruction/data-classification metadata it would have carried had
  it been inlined" and "every M2-reachable resource is untrusted/data/
  no-capabilities" are the same statement, not two that need reconciling.
  Instruction-bearing content (role profile, skill, AGENT, owner
  constraints, canonical policy, project/task instructions) is not made
  addressable by M2 at all — see §4B — so I-4 makes no claim about it. A
  dynamically-fetched resource can never become `may_grant_capabilities=
  true` or an `instruction_class` above `data`/`scoped` by virtue of being
  fetched later, with no exception, because no source admitted into M2's
  addressable set ever had a higher class to begin with.
- **I-5 (single render authority).** M2 never introduces a second
  provider-visible-render algorithm; disclosure content is appended as
  Harness tool-result messages, never spliced into the stable prefix that
  `ResolveProviderContext` already owns.
- **I-6 (historical rows never recompiled).** Pre-M2 `ContextSnapshot`s and
  `ExecutionContextView`s remain exactly as they are; they are never
  retroactively granted a manifest, never reinterpreted as having had
  addressable resources they didn't durably record.
- **I-7 (bounded everything).** Every dynamic operation has an explicit
  host-side limit (bytes, count, query length, timeout) — a model can never
  request "the whole corpus."
- **I-8 (single source of classification).** M2 reuses
  `contextengine.IsDynamicProviderTier` and the existing authority-tier/
  trust-class taxonomy; it does not redeclare a parallel classification
  (this repo has a documented history of exactly that bug — R31 hardening,
  see AUDIT.md §2).

## 4B. Addressable content classification (mission brief §B)

> Added in independent-review round 2. The original draft referenced
> "§B below" twice (old §6.1, old §14/§24) without ever writing this
> section — a dangling reference. Writing it out is also what resolves the
> I-4 contradiction the reviewer found (see below), so it is placed here,
> immediately after the invariant it disambiguates.

OBSERVED: `contextengine.DeterministicAssembler.Assemble`
(`internal/contextengine/assembler.go`) already enforces, in code, that
**only three source kinds** — `SourceApprovedMemory`, `SourceRAGEvidence`,
`SourceWebEvidence` — may ever be admitted with
`InstructionClass==InstructionData`, `TrustClass==TrustUntrusted`, and
`MayGrantCapabilities==false`; any source of one of those three kinds that
does *not* meet that triple is rejected outright
(`ReasonUnsafeInstructionSource`). Every other kind — role profile, skill
content, organization/department AGENT, owner constraints, canonical
policy, project/task instructional context — is admitted with whatever
higher authority tier its `SourceRecord` actually carries; the assembler
does not force those down to untrusted/data.

INFERENCE: the codebase has already drawn exactly the line M2 needs — a
closed set of "evidence-shaped" source kinds that are *structurally
incapable* of carrying instruction authority, versus everything else,
which legitimately can. Any design that tries to make instruction-bearing
content (role profile, skill, AGENT, owner constraints, canonical policy,
project/task instructions) *dynamically fetchable* in the same way as RAG
evidence would need a materially different authority model than "wrap it
as untrusted data" — because it is not data, and pretending otherwise
either strips real instructions of their authority when read inline
(breaking today's behavior) or grants disclosed content authority it
should never have (breaking I-4). That is precisely the contradiction
independent review round 2 identified between old I-4 ("preserves the
same trust/instruction metadata it would have had inline") and the old M2
CONTRACT's blanket "every dynamically-reachable resource is untrusted/
data" — those two statements cannot both hold for instruction-bearing
content, only for evidence-shaped content.

DECISION: M2's addressable universe, in this milestone, is restricted to
exactly the three kinds `contextengine.Assemble` itself already treats as
evidence-only:

| Content kind | Classification | Why |
|---|---|---|
| `SourceApprovedMemory` (approved memory segments) | **ADDRESSABLE** | Already forced untrusted/data/no-capabilities by `Assemble` today; disclosing it dynamically changes nothing about its authority. |
| `SourceRAGEvidence` (RAG evidence) | **ADDRESSABLE** | Same as above. This is also the kind whose identity model (§7) is best-founded — RAG already persists immutable, version/digest-pinned chunks (AUDIT.md — RAG versioning). |
| `SourceWebEvidence` (web evidence) | **ADDRESSABLE, deferred until its own persistence is closed** | Same authority argument applies, but AUDIT.md's findings on web-evidence persistence/versioning stability should be re-checked by the M2.1 implementer before relying on it for pinned identity (I-3) — do not block the *design* on this, but do not assume it is ready without that check. |
| Role profile | **MUST NEVER BE DYNAMICALLY FETCHABLE** (in this milestone) | Instruction-bearing; `Assemble` does not (and must not) force it to untrusted/data. Making it addressable would require either weakening its authority when fetched (dangerous) or a new "addressable instructions" authority model M2 does not define. |
| Approved skill content | **MUST NEVER BE DYNAMICALLY FETCHABLE** (in this milestone) | Same reasoning as role profile. |
| Organization AGENT / Department AGENT | **MUST NEVER BE DYNAMICALLY FETCHABLE** (in this milestone) | Same reasoning; also the highest-authority-tier sources in the system (AUDIT.md). |
| Owner constraints | **MUST NEVER BE DYNAMICALLY FETCHABLE** (in this milestone) | Same reasoning. |
| Canonical policy | **MUST NEVER BE DYNAMICALLY FETCHABLE** (in this milestone) | Same reasoning. |
| Project/task instructional context | **MUST NEVER BE DYNAMICALLY FETCHABLE** (in this milestone) | Same reasoning — this is instruction-bearing context, not evidence, even though it is task-specific rather than organization-wide. |
| Potentially-large artifacts (generic) | **MAY INLINE / ADDRESSABLE, case-by-case** | Only if the concrete artifact is itself evidence-shaped (e.g. a large RAG document); M2 does not create a new artifact category — see AUDIT.md §8/§13 and DESIGN.md §26 (rejected: new Context Artifact Store). |
| Canonical context segments already inlined today | **MUST INLINE** (unchanged) | Everything the assembler already includes verbatim stays included verbatim; M2 only adds a disclosure path for what is *omitted* or *excerpted*, never removes existing inline content. |

RATIONALE: this makes the mission brief's central principle
("dynamic disclosure = evidence/data only, never authority, never policy,
never role instructions, never capabilities") a structural consequence of
an existing code boundary (`contextengine.Assemble`'s own kind-gated
`InstructionData`/`TrustUntrusted`/`MayGrantCapabilities` enforcement),
not a new promise layered on top that some other code path could
accidentally violate. It also means I-4 and the M2 CONTRACT's
`may_grant_capabilities=false`/`trust_class=untrusted` guarantees are the
*same statement* for the restricted set M2 actually addresses, not two
statements that need reconciling. A future "addressable instructions"
milestone — explicitly out of scope here — would need its own, materially
different authority model (e.g. a real capability grant, audited and
narrow) before role profile/skill/AGENT content could ever become
dynamically fetchable; it is not a small extension of this design.

## 5. Domain ownership

New domain package: `internal/contextdisclosure` (name illustrative,
follows the repo's existing lowercase-no-separator package convention like
`contextengine`/`contextcompiler`/`executionharness`).

It owns:
- `ToolExecutionContext` (§9A) — the trusted binding a disclosure call is
  evaluated against.
- Addressable-resource identity and handle format (§7/§8).
- The disclosure event log (audit + telemetry source of truth for dynamic
  reads).
- The `executionharness.ToolCatalog`/`ToolExecutor` implementation that
  exposes `context.inspect`/`context.fetch`/`context.slice`/
  `context.search`/`context.aggregate` as Harness tools.
- Dynamic-context telemetry aggregation feeding into the existing
  `model_invocation_render_telemetry` family (extended, not duplicated —
  see §15).

It explicitly does NOT own:
- Segment/authority-tier/trust-class semantics (`contextengine` owns
  these; `contextdisclosure` reads them).
- Provider-visible rendering (`contextcompiler` owns this exclusively).
- Action/capability authorization decisions (`internal/authorization` owns
  these; `contextdisclosure` may consume a capability decision for "is
  context.search enabled for this role at all" but never for "is this
  specific content visible" — that's a contextengine-tier question).
- Tool-call transport, replay guarding, run-identity digesting
  (`executionharness` already owns and enforces these; `contextdisclosure`
  registers into it, does not reimplement it).

## 6. Proposed data model

Two new durable concepts, deliberately built as an *additive layer* over
`context_segments`, not a parallel store:

### 6.1 `context_addressable_resources`

One row per resource that a `ContextSnapshot` makes addressable — either
because a `context_segments` row was *omitted* (today: dropped silently;
under M2: kept referenceable) or because it represents a larger unit a
segment was excerpted from (e.g. a full RAG document a segment only
excerpted).

```
id                  BIGINT PK
organization_id     TEXT NOT NULL
context_snapshot_id BIGINT NOT NULL  -- FK (id, organization_id) -> context_snapshots
segment_id          BIGINT NULL      -- FK -> context_segments(id), if this
                                     -- resource corresponds 1:1 to a segment
resource_kind       TEXT NOT NULL    -- same closed set as context_segments.source_kind
source_reference    TEXT NOT NULL    -- same identity contract as segments
source_version      TEXT NOT NULL
authority_tier      TEXT NOT NULL    -- copied verbatim from the owning segment/source
authority_priority  INTEGER NOT NULL
instruction_class   TEXT NOT NULL
trust_class         TEXT NOT NULL
data_class          TEXT NOT NULL
may_grant_capabilities BOOLEAN NOT NULL DEFAULT FALSE  -- CHECK: always FALSE for
                                     -- anything reachable via M2 (see I-4; even
                                     -- priority-1/2 sources are not made dynamically
                                     -- fetchable in M2 -- see B below)
content_digest      TEXT NOT NULL    -- sha256 of the full underlying content
byte_count          BIGINT NOT NULL
inline              BOOLEAN NOT NULL -- true if already present verbatim in a
                                     -- context_segments row (fetch = local read);
                                     -- false if it must be retrieved from its
                                     -- owning subsystem (rag/memory) at read time
created_at          TIMESTAMPTZ NOT NULL
UNIQUE (context_snapshot_id, source_reference, source_version)
-- append-only, same trigger pattern as context_segments
```

DECISION: this table is scoped **per snapshot**, not global. A resource
that happens to be addressable in two different snapshots gets two rows
(with possibly different `authority_*`/`trust_*` fields, because those are
properties of *this snapshot's inclusion decision*, not of the underlying
content). RATIONALE: reproducibility (§C/§M) requires that "what could this
execution have read" be answerable purely from one snapshot's own rows,
without joining across snapshots or trusting that a resource's
authorization was stable over time.

### 6.2 `context_disclosure_events`

One append-only row per successful or failed disclosure operation.

```
id                       BIGINT PK
organization_id          TEXT NOT NULL
context_snapshot_id      BIGINT NOT NULL
execution_context_view_id BIGINT NOT NULL  -- FK, the specific durable view this
                                            -- invocation was dispatched against
requesting_model_invocation_id BIGINT NOT NULL -- FK -> modelruntime invocation.
                                            -- Named "requesting_", not bare
                                            -- "model_invocation_id" (round 2
                                            -- naming correction): this is the
                                            -- invocation that ASKED for the
                                            -- resource, turn N. The content
                                            -- only actually enters a provider
                                            -- prompt on turn N+1 (as a tool
                                            -- result) and may remain in
                                            -- VisibleHistory on later turns
                                            -- too -- this column must never be
                                            -- read as "tokens consumed by
                                            -- invocation N".
operation                TEXT NOT NULL     -- inspect|fetch|slice|search|aggregate
resource_id              BIGINT NULL       -- FK -> context_addressable_resources,
                                            -- NULL for a search call with no
                                            -- single-resource target
requested_handle         TEXT NOT NULL     -- the raw handle string presented,
                                            -- kept even on failure, for audit
outcome                  TEXT NOT NULL     -- ok|invalid_request|not_found|
                                            -- forbidden|stale_drift|operational_failure
                                            -- (forbidden further qualified
                                            -- internally per §17's cross-org
                                            -- existence-oracle correction)
disclosure_bytes_returned BIGINT NOT NULL DEFAULT 0 -- named "disclosure_",
                                            -- not bare "bytes_returned"
                                            -- (round 2): telemetry of THIS
                                            -- read, not of what any specific
                                            -- model invocation's prompt
                                            -- actually contained.
disclosure_estimated_tokens BIGINT NOT NULL DEFAULT 0 -- same estimator family
                                            -- as ContextTokenTelemetry; never
                                            -- a provider-reported figure;
                                            -- same "disclosure_" naming
                                            -- rationale as above.
query_text               TEXT NULL         -- for search/aggregate, bounded length
result_count              INTEGER NULL     -- for search
created_at                TIMESTAMPTZ NOT NULL
-- append-only, same trigger pattern as context_segments / execution_context_views
```

Attribution fields chosen per mission brief §I: `organization_id,
context_snapshot_id, execution_context_view_id,
requesting_model_invocation_id, operation, resource_id,
disclosure_bytes_returned, disclosure_estimated_tokens, timestamp` are
kept as **direct columns** (not derived) because they are the fields
TEST_PLAN.md category H needs to assert against directly without a join
chain, and because `execution_context_view_id`/
`requesting_model_invocation_id` together are exactly the compound key the
existing `model_invocation_render_telemetry` table already uses for
M1.2 — this keeps the two telemetry families joinable the same way.
Execution principal / role are **not** duplicated here — they are
derivable via `requesting_model_invocation_id` FK to whatever principal
that invocation already recorded (DECISION: avoid duplication per mission
brief §I explicit instruction).

**Naming note (independent review round 2).** The flow is: invocation N
requests `context.fetch` → disclosure happens → a tool result is appended
→ invocation N+1 is the one that actually sends that content to a
provider (and it may still be present in `VisibleHistory` on invocations
after N+1, too). `requesting_model_invocation_id`/
`disclosure_bytes_returned`/`disclosure_estimated_tokens` are telemetry of
*the read itself* — they must never be interpreted as "tokens invocation N
consumed" or "tokens invocation N+1 consumed"; a future, separate
telemetry dimension for "bytes actually present in a given invocation's
rendered prompt" (if ever built) is a different measurement and must use
its own, differently-named fields, not these.

## 7. Handle identity model

DECISION: **structured, opaque-to-the-model, server-validated handle.**

```go
type ContextHandle struct {
    OrganizationID  string // redundant with snapshot but validated independently
    SnapshotID      int64
    ResourceID      int64
    ResourceVersion string // source_version, not a row-mutation version
    ContentDigest   string // sha256, defense-in-depth against silent DB drift
    Kind            string // resource_kind, for cheap client-side dispatch only
}
```

The model sees only an opaque short string — e.g.
`ctx://snapshot/482/resource/91?v=3&d=1a2b...` (illustrative) or a signed/
HMAC'd token. RATIONALE for keeping the encoding human-legible rather than
a bare opaque token: `context.inspect` output needs to show handles the
model can carry across turns and the host needs to be debuggable in
`orgctl` tooling — but **every field is re-derived from
`context_addressable_resources`/`context_snapshots` at validation time**
(I-2). A forged or hand-crafted handle with a correct-looking
`OrganizationID`/`SnapshotID` but no matching DB row fails NOT_FOUND; one
with a mismatched digest fails STALE-DRIFT; one for a different
org/snapshot than the current invocation also fails NOT_FOUND, not
FORBIDDEN (§17's cross-org existence-oracle correction — a model-visible
FORBIDDEN would let an actor distinguish "exists in another org" from
"doesn't exist," which NOT_FOUND does not) — the handle's own claims are
never trusted, only used as a lookup key, exactly mirroring how
`ExecutionContextView`'s own digest fields are always re-verified rather
than trusted (`ErrExecutionContextViewIntegrity`).

Answering the mission brief's explicit "what happens if..." questions:

- **Document changes after snapshot creation**: irrelevant — the handle is
  bound to `ResourceVersion`/`ContentDigest` captured at snapshot build
  time; a fetch always returns that frozen version, never the source's
  current state. This is consistent with `context_segments` already being
  append-only.
- **RAG corpus changes**: same — the snapshot's `context_addressable_resources`
  row pins the RAG chunk's version/digest as of assembly time (§O, model 1).
- **Memory entry consolidated/retired**: same pinning; a later
  consolidation does not retroactively change what an existing snapshot
  can disclose. (Whether *new* snapshots can still address a retired
  entry is a `contextengine`/`memory` admission-time decision, out of
  scope for M2.)
- **Skill gets a new version**: pinned by `source_version`; the snapshot
  addresses the exact skill version it resolved against, never "latest."
- **Artifact replaced**: not applicable in M2 scope — M2 does not create a
  new artifact store (AUDIT.md §8/§13); any artifact-shaped resource made
  addressable under M2 is addressed via the same content-digest pinning.
- **Organization revision changes**: irrelevant to already-issued handles
  — a handle's authority was fixed at snapshot build time
  (`organization_revision_id` on the snapshot), not re-evaluated against
  the current revision on each fetch. This mirrors how
  `ExecutionContextView` never re-resolves against a newer revision.

## 8. Snapshot/resource binding

DECISION: **yes — resources are sealed inside (i.e. exhaustively
enumerated by) the ContextSnapshot's own `context_addressable_resources`
rows.** Snapshot S authorizes exactly the handles that exist as rows with
`context_snapshot_id = S.id`; the model may choose among them but cannot
invent a new one that resolves.

Alternatives considered and rejected:

- **Global corpus with post-hoc authorization at fetch time** (re-run
  authorization checks against the live RAG/memory corpus on every fetch,
  rather than a frozen per-snapshot list). Rejected: this reintroduces
  exactly the risk mission brief §E warns about — the authorized universe
  could grow between snapshot creation and a later fetch (e.g. a role
  gains a new capability mid-task), silently expanding what one execution
  can see beyond what was authorized when it started. Violates I-1.
- **Snapshot-scoped index with live re-ranking on each search** (freeze
  the *candidate set* but let relevance scoring be recomputed live).
  Partially adopted — see §12 — but resource *membership* (can this be
  returned at all) is still frozen; only *ranking within the frozen set*
  may be recomputed.

This is a stricter model than "sealed at snapshot creation but reads
happen against live storage" — DECISION explicitly favors sealing content
identity (digest) as well as membership, per I-3, because the mission
brief's central principle is about authority, and a resource whose bytes
can silently change without a version bump is an authority leak by another
name (a poisoned-after-the-fact document).

## 9. Progressive disclosure lifecycle

1. `ContextEngine.Build` runs as it does today, producing `Snapshot` +
   `context_segments` (unchanged).
2. **New step, corrected in independent review round 2 (see §9A's
   predecessor discussion for why the original version was wrong):**
   `contextengine.DeterministicAssembler.Assemble` — which OBSERVED is a
   pure function today (`internal/contextengine/assembler.go`: it takes
   `(ctx, AssemblyInput)`, returns `(Assembly, error)`, and never opens a
   `Store`, transaction, or DB connection) — is extended to additionally
   *compute* (still purely, still no I/O) an `AddressableResources []Resource`
   slice as part of its returned `Assembly` struct, one entry for every
   segment/source candidate it decided to omit or include only as an
   excerpt. This is still a pure, unit-testable computation — no
   transaction concern here at all.
   The actual *write* of `context_addressable_resources` rows happens in
   `internal/contextengine/postgres.Store.Create`
   (`internal/contextengine/postgres/store.go`), which OBSERVED already
   owns the single transaction boundary for a snapshot build today
   (`s.pool.BeginTx` → `insertSnapshot` → `insertSegment` (looped) →
   `appendAuditAndOutbox`/`appendAudit` → `tx.Commit`). `Store.Create` is
   extended to also loop over `command.Snapshot.AddressableResources` and
   `insertAddressableResource` for each, inside that same transaction,
   before `Commit`. DECISION: `contextengine` remains the only writer of
   anything derived from its own admission decisions (ownership boundary
   from AUDIT.md §14) — this is unchanged from the original draft; what
   changed is *which function inside contextengine* does the writing.
   RATIONALE: this closes the exact gap independent review round 2 found —
   under the original wording ("additive to `Assemble`"), a literal
   implementation would have given the pure `Assemble` function a `Store`/
   transaction it does not have today, or (worse) written
   `context_addressable_resources` in a *separate* transaction from
   `context_segments`/`context_snapshot`, creating exactly the window §9A
   and §18 must rule out: a snapshot durably visible to a reader with
   segments but no addressable-resource rows (or vice versa). Routing the
   write through `Store.Create`'s existing single transaction closes that
   window by construction, and preserves the property the reviewer
   correctly flagged as valuable: `Assemble` stays pure/deterministic/
   unit-testable without a database in the loop.
3. `contextcompiler.ResolveProviderContext` renders the initial
   provider-visible view exactly as today (I-5) — optionally including a
   compact, bounded manifest of available handles as part of the stable
   prefix (see §M — this is the one place M2 legitimately touches
   rendered bytes, and it must go through the existing single-render-
   algorithm path, never a side channel).
4. `executionharness.RunSpec.Tools` is populated (by whatever caller wires
   up the run — Executive today forbids all tools; a future M2 consumer
   opts in explicitly) with the `context.*` tool definitions.
5. Model requests a tool call; `Runtime.Execute`'s existing loop applies
   unchanged: catalog lookup, `sameToolDefinition` drift check, replay
   guard, `MaxToolCalls` budget, re-authorize, durable-append-before-
   execute.
6. `contextdisclosure.ToolExecutor.Execute` receives a `ToolExecutionContext`
   (§9A) — never a bare `RunIdentity` — validates the handle server-side
   (§7/§10) against the `ContextSnapshotID` and
   `RequestingModelInvocationID` that context carries, looks up
   `context_addressable_resources`, checks snapshot/org/version/digest
   membership, applies limits (§16), retrieves content (from
   `context_segments.content` if `inline=true`, or from the owning
   subsystem — `rag`/`memory` — via their existing read paths if
   `inline=false`), wraps it using `contextengine.RenderUntrustedContextResource`
   (§9B — the exported form of the same marker logic `BuildProviderRenderV2`
   already uses for non-stable content, I-4/§24), records a
   `context_disclosure_events` row, and returns a bounded `ContextResource`
   (§R).
7. `Runtime.Execute` appends the tool result to history; next turn's
   `Project()` surfaces it in `VisibleHistory`.
8. Model continues, possibly issuing further `context.*` calls, bounded by
   `RunPolicy.MaxToolCalls` and M2's own per-operation limits (§16).

## 9A. `ToolExecutionContext` — the trusted snapshot/invocation binding

> Added in independent-review round 2 (P1 finding: "the ToolExecutor
> lacks sufficient identity to satisfy its own contract").

OBSERVED, precisely, from `internal/executionharness/`:

- `executionharness.ToolExecutor` (`ports.go`) is `Execute(context.Context,
  RunIdentity, ToolRequest) (ToolExecutionResult, error)`. `RunIdentity`
  (`types.go`) carries `RunID, OrganizationID, TaskID, AttemptID, RoleID,
  ExecutionPrincipalID, CorrelationID, CausationID` — no
  `ContextSnapshotID`, no `ExecutionContextViewID`, no
  `ModelInvocationID`.
- `Runtime.Execute` (`runtime.go`) is the only caller of
  `r.tools.Execute(...)`, at the single call site
  `toolResult, toolErr := r.tools.Execute(ctx, spec.Identity, toolRequest)`.
  At that exact call site, two more values are already in scope, already
  durable, and never supplied by the model:
  - `spec.Context.ID` — the `RunSpec.Context` (`InitialContext`) set by
    whichever caller constructs the `RunSpec` (Executive/a future M2
    consumer), not by the model. `internal/executionharness/
    modelruntimeadapter/adapter.go:161` already parses this exact same
    field the exact same way for a different purpose:
    `contextSnapshotID, err := strconv.ParseInt(projection.Prefix.Context.ID, 10, 64)`.
  - `modelResult.InvocationRef` — set on the `ModelResult` returned by
    `r.models.Invoke(...)` for the *current* turn, before the tool-request
    loop runs. `modelruntimeadapter/adapter.go:388` already produces this
    exact value as `strconv.FormatInt(created.ID, 10)`, i.e. it is the
    formatted `modelruntime.Invocation.ID` — a durable ID that existed
    before the model ever saw a string form of it, not something the model
    invents or supplies.

INFERENCE: `Runtime.Execute` already possesses everything
`contextdisclosure` needs to prove a disclosure call's snapshot/invocation
binding, at the exact moment it dispatches to the tool executor — it is
simply not threaded through the current `ToolExecutor` port signature.
Resolving this by having `contextdisclosure` independently "look up the
current snapshot for this TaskID" would reintroduce an *implicit*
association exactly like the one M1.3's `TaskClassOf(actorRoleID)` proxy
was removed for — a derived guess standing in for a value the caller
already had authoritatively.

DECISION: extend the `executionharness.ToolExecutor` port (a small,
justified seam into `executionharness` — the mission brief explicitly
allows documenting a seam a subsystem needs; this is that seam, and
`executionharness` is not otherwise redesigned) to:

```go
// ToolExecutionContext is the trusted binding every ToolExecutor
// implementation is evaluated against. Every field is derived by
// Runtime.Execute from state it already holds -- RunSpec construction
// (never the model) and the current turn's already-completed ModelResult
// -- never from data the model supplies in a ToolRequest's arguments.
type ToolExecutionContext struct {
    Identity                    RunIdentity
    ContextSnapshotID           int64 // parsed from RunSpec.Context.ID,
                                       // the same parse
                                       // modelruntimeadapter.Adapter.Invoke
                                       // already performs for a different
                                       // purpose -- not re-derived logic,
                                       // the same durable value read twice.
    RequestingModelInvocationID int64 // parsed from the current turn's
                                       // ModelResult.InvocationRef, already
                                       // in scope at Runtime.Execute's
                                       // r.tools.Execute(...) call site,
                                       // before any tool dispatches.
}

type ToolExecutor interface {
    Execute(context.Context, ToolExecutionContext, ToolRequest) (ToolExecutionResult, error)
}
```

`Runtime.Execute` constructs `ToolExecutionContext{Identity: spec.Identity,
ContextSnapshotID: <parsed from spec.Context.ID>,
RequestingModelInvocationID: <parsed from modelResult.InvocationRef>}`
once per turn (not per tool call — all tool calls within one turn share
the same requesting invocation) and passes it to every `r.tools.Execute`
call in that turn's tool-request loop.

RATIONALE: this satisfies I-1/I-2 for the disclosure boundary specifically
— a `contextdisclosure.ToolExecutor` can now prove
`(organization_id, context_snapshot_id, requesting_model_invocation_id)`
from a value the Harness itself derived from durable state, never from
anything the model's `ToolRequest.Arguments` could claim. Because
`executionharness.ToolExecutor` today has zero production consumers
(AUDIT.md R-2), this signature change breaks no existing production
behavior — M2 would be the port's first real implementation either way.

## 9B. Exported wrapping seam: `contextengine.RenderUntrustedContextResource`

> Added in independent-review round 2 (P2 finding: "no canonical exported
> dynamic-content wrapping seam").

OBSERVED: the untrusted-data structural-marker framing/escaping M2 needs
to reuse (§24) is implemented today as
`func wrapUntrustedData(content string, segment Segment) []byte` in
`internal/contextengine/providerrender.go` — package-private, and its
second parameter is a `contextengine.Segment`, not anything
`contextdisclosure` naturally has for a dynamically-read resource (it has
a `context_addressable_resources` row and freshly-fetched bytes, not a
`Segment`).

DECISION: `contextdisclosure` MUST NOT copy `wrapUntrustedData`'s logic
into a second implementation, and MUST NOT fabricate a fake `Segment`/
`Snapshot` purely to call `BuildProviderRenderV2` and discard the rest of
its output just to get the wrapping. Instead, `contextengine` exports a
narrow function built on the exact same internal logic
`BuildProviderRenderV2` already uses:

```go
// RenderUntrustedContextResource applies the same collision-safe
// structural-marker framing/escaping BuildProviderRenderV2 already applies
// to untrusted/dynamic segments, to arbitrary untrusted content that did
// not come from a Segment. This is the single implementation of that
// framing; BuildProviderRenderV2 and contextdisclosure both call it.
func RenderUntrustedContextResource(content string, meta UntrustedResourceMeta) []byte
```

where `UntrustedResourceMeta` carries whatever subset of `Segment`'s
fields the marker actually needs (authority tier, trust class, type
label) — the exact field set is an implementation detail for the M2.3
slice, not fixed here; the constraint this design freezes is *that there
is exactly one implementation*, shared, not two. `BuildProviderRenderV2`
itself is refactored (mechanically, no behavior change) to call the new
exported function internally instead of the private one, so there is
never a risk of the two framings drifting apart.

## 10. Authorization model

Three questions, three different authorities, never conflated:

- **"May this actor/role invoke context.search at all?"** — an ordinary
  `internal/authorization` capability check (e.g. `context.search.invoke`),
  evaluated once per operation the same way any other capability is
  (org+revision+role+capability+resource+action_digest). This is boundary
  #1 from AUDIT.md §6.
- **"Is this specific resource disclosable to this specific
  snapshot/invocation?"** — never `internal/authorization`. Always: does a
  `context_addressable_resources` row exist with
  `(organization_id, context_snapshot_id) = (invocation's org, invocation's
  snapshot)` and matching `resource_id`/`resource_version`/
  `content_digest`? This is boundary #2, extended.
- **"Is this read within today's operational limits?"** — a third,
  non-authorization concern (§16), evaluated after both of the above pass.

DECISION (mission brief §H, explicit question): **yes, `ContextSnapshot`
(via its `context_addressable_resources` rows) is the authority boundary
for *which contextual resources exist to be read*; `internal/authorization`
remains the authority boundary for *actions* (including "is context.search
enabled").** These are not conflated: a role could be allowed to invoke
`context.search` (action-authorized) and still get zero results because
its snapshot has no matching addressable resources (content-unauthorized)
— that is the expected, correct outcome, not an error.

A retrieval must prove, at minimum (mission brief §H): organization match,
snapshot match, execution/invocation match, resource membership, resource
version, and — where the action itself is capability-gated — principal/
role via the existing `internal/authorization` check. Concretely, the
"execution/invocation match" check compares the handle's claimed
`SnapshotID` against `ToolExecutionContext.ContextSnapshotID` (§9A) — the
value `Runtime.Execute` itself derived from `RunSpec.Context.ID`, never
from the handle's own claims or from a second, independently-trusted
lookup by `TaskID`. All are re-derived server-side per I-2; none are read
from client-claimed handle fields without a DB lookup confirming them.

## 11. Proposed host capability API

All operations execute inside `contextdisclosure.ToolExecutor`, registered
as `executionharness` tools (never inside a `ProviderAdapter` — see §23),
and every operation below receives a `ToolExecutionContext` (§9A) as its
trusted binding — not a bare `RunIdentity`. Names illustrative per mission
brief.

### `context.inspect(snapshot_id implicit, handle?) -> ResourceDescriptor[]`
- INPUT: optional handle filter; if omitted, lists all addressable
  resources for the current snapshot (bounded, §16).
- OUTPUT: `{handle, kind, source_reference (redacted to a stable label,
  not a raw path), byte_count, trust_class, data_class}` — metadata only,
  no content.
- AUTHORIZATION: action-capability check + implicit snapshot scoping (no
  content-membership check needed beyond "belongs to this snapshot," since
  no content is returned).
- BOUNDARIES: current snapshot only.
- SIZE LIMIT: max N resources listed per call (§16).
- TIMEOUT: short (metadata-only, DB read).
- AUDIT EVENT: `context_disclosure_events{operation:"inspect"}`.
- IDEMPOTENCY: fully idempotent, safe to retry/duplicate.
- FAILURE MODE: INVALID_REQUEST (malformed filter) / OPERATIONAL_FAILURE
  (storage unavailable). Never FORBIDDEN for the list-all form (an empty
  list is simply the true answer for a snapshot with no addressable
  resources).

### `context.fetch(handle) -> ContextResource`
- INPUT: one handle.
- OUTPUT: `ContextResource` (§R), content included, up to the byte limit.
- AUTHORIZATION: full chain from §10.
- BOUNDARIES: exactly the resource named by the handle, exactly the
  version pinned in `context_addressable_resources`.
- SIZE LIMIT: `max_fetch_bytes` (§16); oversized content is either
  rejected outright or requires `context.slice` (DECISION: reject with a
  clear error naming the slice alternative, never silently truncate —
  silent truncation is an authority-adjacent integrity risk, since a
  truncated instruction-bearing... except I-4 already forbids
  instruction-bearing dynamic content, so the actual risk is a
  *misleadingly incomplete* piece of evidence being treated as complete).
- TIMEOUT: bounded (source read timeout, §16).
- AUDIT EVENT: `context_disclosure_events{operation:"fetch"}`.
- IDEMPOTENCY: same handle -> same content, always (I-3). Safe to retry;
  a duplicate fetch produces a second audit row (accepted — see §Concurrency)
  but never different content.
- FAILURE MODE: NOT_FOUND (no matching row, **or** wrong org/snapshot —
  collapsed to the same model-visible outcome per §17's cross-org
  existence-oracle correction; the true reason is still recorded
  internally in `context_disclosure_events`) / STALE_DRIFT (digest
  mismatch against current storage) / INVALID_REQUEST (malformed handle) /
  OPERATIONAL_FAILURE (storage unavailable — never conflated with
  NOT_FOUND, see §14/§L).

### `context.slice(handle, offset, length) -> ContextResource`
- INPUT: handle + byte or logical-unit range.
- OUTPUT: partial `ContextResource`, `content` covering only the
  requested range, `byte_count` reflecting the slice not the whole.
- AUTHORIZATION/BOUNDARIES: identical to `fetch`, plus range validity.
- SIZE LIMIT: `max_slice_bytes` (can be smaller than `max_fetch_bytes`).
- TIMEOUT: same class as fetch.
- AUDIT EVENT: `context_disclosure_events{operation:"slice"}`, with
  offset/length recorded (extend the event row with nullable
  `slice_offset`/`slice_length` — additive).
- IDEMPOTENCY: same handle+range -> same bytes, always.
- FAILURE MODE: same set as fetch, plus INVALID_REQUEST for an
  out-of-bounds range.

### `context.search(query) -> SearchResult[]`
- Full semantics in §12.
- INPUT: bounded query string (§16 length limit), implicit snapshot scope.
- OUTPUT: ranked `{handle, kind, snippet (bounded), score}[]`.
- AUTHORIZATION: action-capability check for `context.search.invoke`;
  results are filtered to the snapshot's own `context_addressable_resources`
  (never a live corpus query — see §12 DECISION).
- BOUNDARIES: current snapshot's addressable set only.
- SIZE LIMIT: `max_search_results` (§16).
- TIMEOUT: bounded (in-snapshot index lookup, not a live vector query).
- AUDIT EVENT: `context_disclosure_events{operation:"search", query_text,
  result_count}`.
- IDEMPOTENCY: NOT required to be byte-identical across calls if ranking
  has any live component (§12 discusses this trade-off) — but membership
  of the *result set* must always be a subset of the same frozen
  addressable-resource list, which is deterministic.
- FAILURE MODE: INVALID_REQUEST (query too long/empty) / OPERATIONAL_FAILURE
  (index unavailable). Never FORBIDDEN for zero results — mirrors
  `context.inspect`.

### `context.aggregate(handles[]) -> ContextResource`
- INPUT: bounded list of handles (§16 count limit).
- OUTPUT: one concatenated/bounded `ContextResource` combining several
  resources' content, each still wrapped with its own trust/provenance
  markers (never merged into one undifferentiated blob — I-4).
- AUTHORIZATION/BOUNDARIES: each handle individually validated per §10;
  any single invalid handle fails the whole call closed (no partial
  aggregate silently dropping a denied resource — that would make a
  FORBIDDEN outcome invisible to the audit trail).
- SIZE LIMIT: `max_aggregate_bytes`, plus the per-handle count limit.
- TIMEOUT: bounded, scales with handle count.
- AUDIT EVENT: one `context_disclosure_events{operation:"aggregate"}` row
  referencing all constituent handles (extend with a nullable
  `aggregate_member_resource_ids BIGINT[]` — additive).
- IDEMPOTENCY: same handle set -> same concatenated content.
- FAILURE MODE: same set as fetch (§17's cross-org existence-oracle
  correction applies per-handle here too); the first failing handle's
  outcome short-circuits the whole call.

The model never receives a filesystem path or host path anywhere in any of
these outputs — `source_reference` as shown to the model is always the
same redacted/stable label `context_segments.source_reference` already
uses today (never a raw disk path; `internal/staging`'s artifact
mechanics, by contrast, do use real paths internally, which is one more
reason not to reuse that package directly for model-facing output).

## 12. Search semantics

Mission brief explicitly rejects "search everything the agent has access
to" as too ambiguous. DECISION: **a snapshot-scoped index over the
snapshot's own `context_addressable_resources` set, i.e. option
"previously-authorized set of handles" + "snapshot-scoped index," combined
— never a global corpus with post-hoc authorization.**

Concretely: at disclosure time, `context.search` ranks only among rows
already present in `context_addressable_resources` for the current
snapshot (a small, bounded, already-authorized set — typically tens to
low hundreds of resources, not the full RAG/memory corpus). Ranking may
use a live scoring function (e.g. lexical match against the already-frozen
excerpt/snippet text stored in those rows) without re-querying
`rag`/`memory` live, because the candidate *set* is frozen even if the
*order* is computed at call time.

Evaluated against the mission brief's explicit risk list:

- **Cross-org leakage**: impossible by construction — the index is
  literally rows scoped to one `context_snapshot_id`, which is itself
  scoped to one `organization_id` via the existing FK.
  `context.search` never issues a query against `rag`/`memory` storage
  directly.
- **Privilege escalation via semantic search**: impossible — search cannot
  surface a resource that isn't already an addressable-resource row for
  this snapshot, so it cannot escalate beyond what `ContextEngine.Build`
  already decided (I-1).
  This also directly resolves the mission's central worry: search cannot
  become a side door around org_id/actor role/unit restrictions, because
  those restrictions were already applied once, upstream, when the
  addressable-resource rows were written — search only re-orders a fixed
  set, never re-derives membership.
- **Unstable/non-reproducible ranking**: accepted trade-off, explicitly
  scoped — ranking order MAY vary run-to-run (e.g. if it's not a pure
  function of stored fields), but result **membership** (which handles
  could ever appear) is always deterministic and reproducible from the
  snapshot alone. TEST_PLAN.md category A/I should assert membership
  determinism, not order determinism, unless the M2.4 implementation slice
  commits to a pure/deterministic ranking function (recommended: it should,
  since the candidate set is already small — a simple deterministic
  lexical/BM25-style score over the frozen excerpt text is sufficient and
  avoids the reproducibility problem entirely; DECISION: M2.4 should adopt
  a deterministic ranking function specifically to avoid ever needing the
  non-deterministic case).
- **Historical-reproducibility risk**: a `context_disclosure_events` row
  records `result_count` and (recommended addition, additive) the actual
  returned handle list, so a historical search call's *result* is durably
  recorded even if the ranking function itself is not literally re-run
  during audit — the audit trail answers "what did it return," which is
  what reproducibility actually requires, not "would a re-run today
  produce the same order."

**Deferred, explicitly out of M2 scope**: a `context.search` that issues a
*new* semantic query against live RAG (mission brief §O, model 2). See
§22.

## 13. Fetch/slice semantics

Covered in §11's per-operation spec. Key rule: `fetch`/`slice` never
re-run admission logic — they are pure reads against
`context_addressable_resources` + the underlying content (inline segment
content, or a version-pinned read from `rag`/`memory` when `inline=false`).
If `inline=false` and the underlying source has since been modified/
retired, the version-pinned read must still return the pinned version's
bytes if the underlying store retains history (as `rag`/`memory` do — both
are lifecycle/versioned stores per AUDIT.md §5), or must fail STALE_DRIFT
if the pinned version is no longer retrievable. It must never silently
fall back to "whatever the current version is" (I-3).

## 14. Provenance semantics

Every `ContextResource` returned by any operation carries the same
provenance fields `context_segments` already carries for inline content:
`authority_tier`, `instruction_class`, `trust_class`, `data_class`,
`may_grant_capabilities` (always `false` for anything M2 makes
dynamically fetchable — see §B below), plus `content_digest` and
`source_reference`/`source_version` for identity. This is not new
provenance vocabulary — it is the same vocabulary
`context_segments`/`SourceRecord` already use, projected onto a dynamic
read (I-4/I-8).

## 15. Telemetry

M1.2 (`ContextTokenTelemetry`) measures the **initial** provider-visible
context only, keyed 1:1 to one `(model_invocation, execution_context_view)`.
M2 needs a **dynamic** dimension keyed 1:many (one invocation, many
disclosure events over its lifetime). DECISION: do not add these fields to
`model_invocation_render_telemetry` (that table's M1.2 columns are
deliberately all-or-nothing and 1:1 with a view — forcing 1:many data into
it would break that contract). Instead:

- `context_disclosure_events` (§6.2) is itself the raw per-operation
  telemetry log — every operation, outcome, byte count, and estimated
  token count is already a row there.
- A derived, read-only aggregation (a view or a small rollup query, not a
  new mutable table) computes, per `requesting_model_invocation_id`:
  `dynamic_context_fetch_count`, `dynamic_context_bytes`,
  `dynamic_context_estimated_tokens`, `resources_inspected`,
  `resources_fetched`, `search_calls` — exactly the metric set the mission
  brief names, but derived from `context_disclosure_events`, never
  duplicated as a second mutable source of truth.
- `initial_context_bytes`/`initial_estimated_tokens` are not new fields —
  they are simply `ContextTokenTelemetry.ProviderVisibleBytes`/
  `EstimatedProviderVisibleTokens`, already present. M2's aggregation
  joins to them by `execution_context_view_id`, it does not recompute
  them.
- `disclosure_estimated_tokens` in `context_disclosure_events` uses the
  **same estimator identity family** as `ContextTokenTelemetry`
  (`EstimatorID`/`EstimatorVersion` columns, additive) — never a
  differently-calibrated estimator, and never labeled as provider-reported
  usage (I-8, mission brief §J explicit warning).

## 16. Limits

All limits are host-side constants (config, not model-supplied), enforced
before any read touches underlying storage:

- `max_fetch_bytes` (per `context.fetch` call)
- `max_slice_bytes` (per `context.slice` call, ≤ `max_fetch_bytes`)
- `max_search_results` (per `context.search` call)
- `max_search_query_bytes` (query length)
- `max_inspect_results` (per `context.inspect` list call)
- `max_aggregate_bytes` (total, per `context.aggregate` call)
- `max_aggregate_handles` (count, per `context.aggregate` call)
- `max_dynamic_operations_per_invocation` (ties into
  `executionharness.RunPolicy.MaxToolCalls`, but M2 may want a *narrower*
  sub-limit specific to `context.*` tools, since `MaxToolCalls` is shared
  across all tools a future consumer might register)
- per-operation timeout (bounded read against storage; distinct from any
  provider-call timeout)

These are OBSERVABILITY + LIMIT concerns, explicitly not AUTHORIZATION
(mission brief §K): a request that exceeds a limit is INVALID_REQUEST (or
a bounded/rejected fetch, per §11), never FORBIDDEN. M2 does **not**
introduce `max_dynamic_context_bytes`/`max_dynamic_context_fetches` as an
*admission controller* that blocks a whole invocation — it only bounds
individual operations. A future, separate mission may wire these into
`modelruntime`'s cost/admission path; M2 explicitly does not (mission
brief: "M2 must NOT become an automatic admission controller yet").

## 17. Failure model

| Condition | Category | Notes |
|---|---|---|
| Malformed handle syntax | INVALID_REQUEST | never reaches storage |
| Unknown handle (no matching row) | NOT_FOUND | |
| Handle from a different org | NOT_FOUND *(model/API-visible)* | **corrected in independent review round 2** — see note below the table |
| Handle from a different snapshot than the current invocation | NOT_FOUND *(model/API-visible)* | same correction |
| Digest mismatch (content drifted under a pinned version) | STALE_DRIFT | distinct from NOT_FOUND — the identity existed, its content proof failed |
| Resource version missing from underlying store | STALE_DRIFT (if a prior version existed) or NOT_FOUND (if never existed) | |
| Resource corrupt (unreadable bytes) | OPERATIONAL_FAILURE | never reported as FORBIDDEN |
| Authorization mismatch (action-capability denied) | FORBIDDEN | from `internal/authorization`, boundary #1 |
| Read exceeds bounds | INVALID_REQUEST | reject with the limit named, per §11 fetch decision |
| Search scope invalid (empty/oversized query) | INVALID_REQUEST | |
| Artifact/resource missing entirely | NOT_FOUND | |
| Storage unavailable | OPERATIONAL_FAILURE | mission brief explicit: never masquerades as FORBIDDEN or NOT_FOUND |

**Cross-org / cross-snapshot existence oracle (independent review round 2,
P2 finding).** `context_addressable_resources.id` and `context_snapshots.id`
are sequential integers and therefore enumerable. If a cross-org or
cross-snapshot handle attempt returned a *model/API-visible* FORBIDDEN
(distinct from NOT_FOUND), an actor could distinguish "this ID exists in
some other org" from "this ID doesn't exist at all" purely from the
outcome code, without ever seeing that org's content — a metadata leak
across the tenant boundary the rest of this design otherwise closes.
DECISION: the model/API-visible outcome for both "wrong org" and "wrong
snapshot" is **NOT_FOUND**, identical to the outcome for a genuinely
nonexistent ID — a caller outside the correct org/snapshot cannot
distinguish "exists elsewhere" from "does not exist" by outcome code
alone. The **internal** `context_disclosure_events.outcome` column
(not model/API-visible; an operator/audit-only field) retains the true,
more specific reason (`forbidden_cross_org` / `forbidden_wrong_snapshot`
vs `not_found`, additive to the existing `outcome` value set) so audit and
incident review never lose the real distinction — only the model-facing
surface collapses the two. `context_disclosure_events.outcome` therefore
carries strictly more detail than what any `context.*` operation ever
returns to the model, by design.

DECISION (unchanged from the original draft): `context_disclosure_events.
outcome` distinguishes `ok|invalid_request|not_found|forbidden|
stale_drift|operational_failure` at the audit-event category level (with
`forbidden` itself further qualified per the paragraph above for the
cross-org/cross-snapshot case specifically) — an operator reviewing
disclosure history can distinguish "the model tried something it wasn't
allowed to" from "our storage was down," which the mission brief flags as
a real risk (never let unavailability read as a policy violation). "FORBIDDEN"
as a genuinely model-visible outcome is still reachable for the
*action*-authorization boundary (§10 boundary #1 — e.g. the role's
`context.search.invoke` capability itself is denied), which is not an
existence-revealing signal the same way a content-membership FORBIDDEN
would be, since the action check happens identically regardless of any
specific resource's existence.

## 18. Concurrency model

- **Two concurrent fetches of the same handle**: both succeed, both return
  byte-identical content (I-3), both are recorded as separate
  `context_disclosure_events` rows (exactly-once is not required per
  mission brief — safe idempotency and correct audit attribution are).
- **Search + resource invalidation**: "invalidation" of a resource has no
  meaning at the `context_addressable_resources` layer (rows are
  append-only, pinned to a version) — the underlying source's own
  lifecycle (e.g. a memory entry retiring) does not retroactively remove a
  row; a subsequent `fetch` against that pinned version either succeeds
  (source retains history) or fails STALE_DRIFT (source purged it) — never
  silently returns different content.
- **Snapshot creation while the underlying source changes**: irrelevant to
  M2 — `ContextEngine.Build` already handles this today (it reads a
  point-in-time view of `rag`/`memory` when assembling); M2 only pins
  whatever `Build` decided.
- **Concurrent manifest/addressable-resource-row creation**: rows are
  written once, at `Build` time, inside `Store.Create`'s single
  transaction (§9 step 2, corrected in independent review round 2) — the
  same transaction that writes `context_snapshots`/`context_segments` — so
  no separate concurrent-creation race exists, and no reader can ever
  observe a snapshot with segments but no addressable-resource rows (or
  vice versa) mid-build, because both are committed atomically together.
- **Retry of a disclosure call**: safe — same handle, same result, new
  audit row (§I-3 + accepted duplication of audit rows above).
- **Duplicated tool invocation / provider retry**: `executionharness`'s
  existing replay guard on `ToolCallID` already prevents a duplicate tool
  *request* from executing twice at the Harness layer; even if that guard
  were bypassed, `contextdisclosure`'s own read is naturally idempotent
  (I-3), so a duplicate execution is wasteful but never corrupting. Telemetry
  aggregation (§15) must sum over `context_disclosure_events` rows as they
  actually occurred — a provider-level retry that produces two Harness
  tool calls produces two audit rows and two counted operations, which is
  the honest (if slightly inflated) telemetry outcome, not a corrupted
  one; TEST_PLAN.md category F/H should assert this is *counted honestly*
  rather than deduplicated incorrectly to a wrong number.
- **Process restart mid-disclosure-call**: the durable-append-before-
  execute pattern `executionharness` already uses for tool calls in
  general applies unchanged; a `contextdisclosure` read has no external
  side effect to worry about (unlike, say, an email-sending tool), so a
  restart before the audit row is written simply means the call is retried
  cleanly — no compensating action needed.

## 19. Historical compatibility

- Pre-M2 `ContextSnapshot`s have no `context_addressable_resources` rows
  (table didn't exist / wasn't populated for them). A `context.inspect`
  call scoped to such a snapshot correctly returns an empty list — this is
  an honest fact ("this snapshot predates M2 addressability"), never
  backfilled, mirroring the `task_class`/`execution_purpose`/
  `actor_unit_id` nullable-and-not-backfilled precedent (migration 000053).
- Pre-M2 `ExecutionContextView`s are unaffected — M2 adds no column to
  that table (I-5: M2 never touches provider-visible rendering) and no
  reinterpretation of `SelectionKind`/`SelectorAlgorithmVersion` is
  needed.
- Historical model invocations that predate M2 simply have zero
  `context_disclosure_events` rows. Corrected in independent review round
  2: the aggregation MUST NOT report this as "zero dynamic reads" (a
  confirmed-absence claim) — zero rows only proves no `context_disclosure_
  events` row exists, not that the invocation had no equivalent need for
  or access to additional context, since the table didn't exist yet. The
  aggregation reports this as `unavailable`, per the DECISION at the end
  of this section — this bullet previously contradicted that DECISION and
  is now consistent with it.
- Retries under the same idempotency key for a **pre-M2** snapshot behave
  exactly as they do today (unaffected — M2 adds nothing to the
  `ContextSnapshot`/`ExecutionContextView` write paths themselves, only a
  new read/tool layer on top).
- Rollback of a future M2 migration: because every M2 table is strictly
  additive (new tables, or additive nullable columns if any existing table
  is touched at all — current design touches none), a `down` migration is
  a straightforward drop of the new tables with no data-loss risk to any
  M1.x object. No M1.x table is altered by this design.
- DECISION: nowhere does M2 reconstruct disclosure history that didn't
  exist. A pre-M2 invocation's dynamic-context telemetry is `unavailable`
  (zero rows, correctly), never inferred or fabricated as `0` in a way
  that implies "we know it read nothing" — the aggregation's own
  documentation must state this is an absence-of-record, not a positive
  proof of no dynamic reads, for any invocation whose
  `requesting_model_invocation_id` predates the `context_disclosure_events`
  table's existence. Recommend a
  small marker (a `contextdisclosure_available_since` config timestamp or
  equivalent) an operator/auditor can compare against, rather than trusting
  "zero rows = definitely read nothing" silently forever.

## 20. Migration proposal (paper design only — no migration file reserved or written)

Additive only, following the exact pattern of migrations 000051/000053:

- `context_addressable_resources` — new table, per §6.1. FK to
  `(context_snapshot_id, organization_id) -> context_snapshots(id,
  organization_id)` (same composite-FK pattern as `context_segments` and
  `execution_context_views`, so org isolation is enforced by Postgres, not
  only application code). Append-only via the same
  `reject_*_mutation` trigger pattern. `UNIQUE (context_snapshot_id,
  source_reference, source_version)`.
- `context_disclosure_events` — new table, per §6.2. FK to
  `execution_context_views(id)` and to whatever `modelruntime.Invocation`
  table already exists (join, not duplicate, principal/role — per mission
  brief §I). Append-only via the same trigger pattern. Indexes on
  `(requesting_model_invocation_id)` and `(context_snapshot_id,
  created_at)` for the telemetry aggregation query.
- No existing M1.x table is altered. No nullable-during-transition columns
  are needed because nothing existing is being extended — this is a purely
  additive new domain, unlike M1.2/M1.3 which extended pre-existing rows.
- Reversibility: **corrected in independent review round 2** — see §21 for
  the distinction between an application-level rollback (preferred once
  the tables hold real events) and a schema-level `down` migration
  (destructive; DROP). This section previously stated "both new tables can
  be dropped with no impact" without qualification, which contradicted
  §21's separate statement that historical disclosure events must remain
  as audit records — a bare `DROP TABLE` destroys them. §21 is now the
  single source of truth for rollback semantics; a `down` migration file,
  if one is ever written, is a schema-level DROP, and is NOT the
  recommended production rollback path once `context_disclosure_events`
  contains real rows (see §21).
- DECISION not to reserve a migration number here, per mission
  instructions — the exact number is for the implementation mission that
  follows this design.

## 21. Rollback considerations

**Corrected in independent review round 2**: this section previously
coexisted with §20 stating tables "can be dropped with no impact" while
also stating historical events "remain valid... even after the feature is
disabled" — those two claims are only both true if rollback is understood
as two distinct operations, not one. This design now names them
separately and states which is preferred when:

- **APPLICATION ROLLBACK (preferred, once real events exist).** Stop
  registering `context.*` tools in any `executionharness.RunSpec.Tools`,
  which reduces the system to exactly today's zero-tools behavior (§9 step
  4) — no other code path changes. The schema (`context_addressable_
  resources`, `context_disclosure_events`) and every row already written
  to it are left in place, untouched. Historical `context_disclosure_
  events` rows remain valid historical audit records indefinitely, exactly
  as I-6 requires for any other durable M1.x/M2 object — a rollback is not
  an exemption from "historical rows are never recompiled or destroyed."
  This is the recommended production rollback path for any environment
  where `context_disclosure_events` has ever recorded a real disclosure.
- **SCHEMA DOWN (destructive; only safe pre-production or when the tables
  genuinely hold zero real rows).** A `down` migration, if one is ever
  written, performs `DROP TABLE context_disclosure_events` then `DROP
  TABLE context_addressable_resources` — this destroys any M2 telemetry/
  audit history that exists. Because the schema is purely additive and no
  M1.x table or trigger is touched, this is always *mechanically* safe for
  M1.x — no M1.x table, trigger, or FK is affected — but it is NOT safe
  for M2's own audit trail once that trail contains real events, which is
  exactly the failure mode application rollback avoids. Schema-down should
  be treated the same way any other irreversible-data-loss migration
  rollback is treated in this repo: an explicit, confirmed, rare action,
  never the default response to "M2 needs to be disabled."

DECISION: implementation and operations documentation for M2 (a later
mission) should make application rollback the default instruction, and
should require an explicit confirmation step before anyone runs a
schema-down migration against an environment where `context_disclosure_events`
is non-empty.

## 22. Integration points

- `internal/contextengine.DeterministicAssembler.Assemble` — additive,
  pure computation of an `AddressableResources` slice on the returned
  `Assembly` for omitted/excerpted sources (§9 step 2, corrected). No I/O
  added to this function.
- `internal/contextengine/postgres.Store.Create` — additive write of
  `context_addressable_resources` rows inside its existing transaction
  (§9 step 2, corrected). This, not `Assemble`, is the actual write seam.
- `internal/contextengine` (exported surface) — new
  `RenderUntrustedContextResource` function (§9B), extracted from the
  logic `wrapUntrustedData`/`BuildProviderRenderV2` already implement, so
  `contextdisclosure` has one canonical wrapping call instead of a copy or
  a fabricated `Segment`.
- `internal/executionharness` — two seams: (1) the `ToolExecutor` port
  signature changes to accept `ToolExecutionContext` instead of a bare
  `RunIdentity` (§9A) — justified because it has zero production consumers
  today (AUDIT.md R-2), so this is not a breaking change to any real
  caller; (2) a new `ToolCatalog`/`ToolExecutor` implementation
  (`contextdisclosure`) registered by whichever caller opts a run into M2
  (not Executive's typed-task path, which explicitly forbids tools today
  and is out of scope to change here).
- `internal/modelruntime` — read-only: `contextdisclosure`'s telemetry
  aggregation joins `execution_context_views`/`model_invocation_render_telemetry`/
  invocation identity by FK; nothing in `modelruntime` is modified.
- `internal/rag`, `internal/memory` — read-only, version-pinned reads for
  `inline=false` resources, reusing their existing read APIs (not new
  bespoke queries bypassing their own scoping/authz).
- `internal/authorization` — one new capability check
  (`context.search.invoke` or similar), evaluated exactly like any other
  capability today; no change to the authorization engine itself.

## 23. Security analysis

- **Model tooling placement**: `context.*` tools live in
  `internal/contextdisclosure`, registered into `executionharness`'s
  existing `ToolCatalog`/`ToolExecutor` ports — **not** inside any
  `ProviderAdapter`. This preserves provider independence (mission brief
  §S): the exact same tool definitions and executor work unchanged across
  OpenAI-compatible, DeepSeek, Gemini, Alibaba/Claude, MiMo, and OpenAI
  Responses adapters, because all six already pass `ToolDefinitions`
  through the same `CanonicalRequest`/`RawResponse` shape (AUDIT.md §9).
  `ExecutionHarness` is the right host-capability layer, not Model Runtime
  itself, because Model Runtime's job (per its own package boundary,
  AUDIT.md §2) is dispatch/egress/identity/pricing for one invocation, not
  cross-turn tool orchestration — that's exactly what `executionharness`
  already exists to own.
- **No filesystem/host-path exposure**: every `ContextResource.source_reference`
  shown to the model is the same redacted/stable `source_reference` label
  `context_segments` already uses (never a real path); `context.inspect`
  never returns storage keys, table names, or file paths.
- **No live external network/filesystem access for the model**: every
  `context.*` operation resolves against durable Postgres rows the host
  already trusts (`context_addressable_resources`) plus bounded reads
  against `rag`/`memory`'s own existing, already-authorized read paths —
  never an open-ended fetch of an arbitrary URL/path the model supplies.

## 24. Prompt-injection / data-authority treatment

- Every dynamically-fetched `ContextResource` is wrapped via
  `contextengine.RenderUntrustedContextResource` (§9B) — the exported form
  of the exact same escaped structural marker mechanism
  `contextengine.BuildProviderRenderV2` already applies to untrusted/
  dynamic segments (HTML-escape then `[authority:tier=N trust=untrusted
  may_grant=false type="..."]...[/authority]` framing) — before being
  appended to `VisibleHistory` as a tool result. This is not a new
  injection defense and not a second implementation of one — it is the
  same one M1 already built, with a single shared implementation, applied
  consistently to a new delivery path (I-8).
- `ContextResource.trust_class` is always `untrusted` for any resource
  reachable via M2 in this design (§B below) — a fetched RAG/memory/task
  document's text can never be interpreted as an instruction by
  construction, because the same `InstructionClass==InstructionData`
  contract that already gates inline RAG/memory segments (AUDIT.md §2)
  applies identically to dynamically-fetched ones.
- `may_grant_capabilities` is hard-pinned `false` at the
  `context_addressable_resources` table level (§6.1 CHECK) — there is no
  code path by which a dynamic read can carry capability-granting
  authority in this design, closing the loop the mission brief's central
  principle demands.

## 25. Alternatives considered

- **Global live-corpus search with post-hoc authorization** — rejected,
  §12.
- **A model-invented/free-text handle resolved by best-effort matching** —
  rejected outright: violates I-2 (mission brief: "never let a
  model-invented handle become authority by itself"). All handles must
  originate from a prior `context.inspect`/`context.search` response.
- **Storing dynamic-context telemetry as new columns on
  `model_invocation_render_telemetry`** — rejected, §15 (breaks that
  table's 1:1/all-or-nothing contract).
- **A new general "Context Artifact Store" spanning rag/memory/staging/
  objectstorage** — rejected, AUDIT.md §8/§13 (recreates the
  ContextStore/ArtifactStore/ObjectStore/MemoryStore/RAGStore ownership
  confusion the mission brief explicitly warns against).
- **Letting `context.search` re-query RAG live (mission brief §O model
  2)** — deferred, not designed here beyond naming the seam (§O below);
  too many open implications (nondeterminism, cost, query injection,
  reproducibility) to fold into M2's first slice.

## 26. Rejected designs

- Making `ExecutionContextView` itself own the list of available handles
  (mission brief's suggested "ExecutionContextManifest" concept) —
  considered and rejected in favor of the separate, additive
  `context_addressable_resources` table owned by `contextengine`'s own
  `Snapshot`. RATIONALE: `ExecutionContextView` is explicitly documented
  as a derived, immutable *rendering* of one snapshot resolution
  (AUDIT.md §2) — its whole purpose is to be re-derivable/verifiable
  bytes, not a place to hang a second, independently-evolving list of
  resource rows. `ContextSnapshot` is the correct owner because it is
  already the canonical authorization-universe object (§8's binding
  decision); inventing a third top-level entity ("manifest") when
  `Snapshot` can already own this correctly would violate the mission
  brief's own instruction not to invent unnecessary new concepts.
- A capability-typed handle (the handle itself carrying a signed grant,
  independent of DB lookup) — rejected; violates I-2, and duplicates
  authority state in two places (the handle's signature and the DB row),
  which is exactly the kind of two-sources-of-truth bug this codebase has
  a documented history of fixing (AUDIT.md §2, R31 stable/dynamic-tier
  bug).

## 27. Implementation slices (for a later mission — not built here)

Proposed order, each independently reviewable, each adding tests before
widening scope:

- **M2.0** — contract + domain types only: `ContextHandle`,
  `ContextResource`, operation input/output structs, no persistence, no
  wiring. Pure Go package, unit-testable in isolation.
- **M2.1** — durable `context_addressable_resources`: migration, Go
  domain type, and the additive write step inside
  `contextengine.Assembler.Assemble`. No read path yet.
- **M2.2** — `context_disclosure_events` + the authorization/validation
  chain (§10) as a standalone service, exercised only via direct unit/
  integration tests (no Harness wiring yet) — `fetch`/`slice`/`inspect`
  first (simplest, no ranking concerns), `search`/`aggregate` deferred to
  M2.4.
  Note: this repo-observed pattern (AUDIT.md §2) is "prove the read/write
  contract in isolation before wiring a live consumer" — M1.1/M1.2/M1.3
  all shipped their core algorithm before any behavior-visible integration.
- **M2.3** — `executionharness.ToolCatalog`/`ToolExecutor` implementation
  wrapping M2.2, wired into a single, clearly-scoped, non-Executive test
  consumer first (never Executive's typed-task path directly, given
  AUDIT.md R-2's observation that this is the *first* real exercise of the
  tool-call loop).
- **M2.4** — `context.search`/`context.aggregate`, including the
  deterministic ranking function decision from §12.
- **M2.5** — telemetry aggregation (§15) and any `orgctl` inspection
  tooling for `context_disclosure_events`.
- **M2.6** — integration + historical-compatibility test suite (§19) run
  against real pre-M2 snapshots/views, plus documentation of the
  `contextdisclosure_available_since` marker.

Each slice: independently reviewable, ships its own tests before the next
slice widens scope, no slice requires a big-bang refactor of `contextengine`/
`contextcompiler`/`executionharness` (all integration points in §22 are
additive).

---

## M2 CONTRACT

**MUST**
- MUST derive all disclosure authority server-side from
  `context_addressable_resources` rows scoped to the current
  `(organization_id, context_snapshot_id)`; MUST NOT trust any field
  claimed by the handle itself without a matching DB row.
- MUST evaluate every disclosure call against a `ToolExecutionContext`
  (§9A) whose `ContextSnapshotID`/`RequestingModelInvocationID` were
  derived by `Runtime.Execute` from durable state (`RunSpec.Context.ID`,
  the current turn's `ModelResult.InvocationRef`) — MUST NOT derive the
  current snapshot/invocation by any other means (e.g. looking it up from
  `TaskID`), and MUST NOT accept it from anything the model supplies.
  *(round 2)*
- MUST write `context_addressable_resources` rows in the exact same
  PostgreSQL transaction as the `context_snapshots`/`context_segments`
  rows they accompany (`contextengine/postgres.Store.Create`) — MUST NOT
  write them from a separate transaction or from
  `Assembler.Assemble` directly, which MUST remain a pure, I/O-free
  function. *(round 2)*
- MUST pin every addressable resource to a specific `source_version` +
  `content_digest` at snapshot-build time; a fetch MUST return exactly
  that pinned content or fail STALE_DRIFT/NOT_FOUND — MUST NOT silently
  return a different (newer/older) version.
- MUST restrict M2's addressable universe, in this milestone, to the
  evidence/data source kinds `contextengine.Assemble` already forces to
  `InstructionData`/`TrustUntrusted`/no-capabilities (§4B) — MUST NOT make
  role profile, skill content, organization/department AGENT, owner
  constraints, canonical policy, or project/task instructional context
  dynamically fetchable in this milestone. *(round 2)*
- MUST set `may_grant_capabilities=false` and `trust_class='untrusted'`/
  `instruction_class` no higher than `data`/`scoped` for every resource
  reachable via any M2 operation, with no exception.
- MUST wrap all dynamically-disclosed content using
  `contextengine.RenderUntrustedContextResource` (§9B) — the single,
  exported implementation shared with `BuildProviderRenderV2` — before it
  enters `VisibleHistory`; MUST NOT maintain a second copy of that framing
  logic. *(round 2, corrected from referencing the private
  `wrapUntrustedData`)*
- MUST register `context.*` operations as `executionharness` tools, MUST
  NOT implement them inside any `ProviderAdapter`.
- MUST record a `context_disclosure_events` row for every operation
  attempt, success or failure, with an honest **internal** outcome
  category (`ok|invalid_request|not_found|forbidden|stale_drift|
  operational_failure`, with `forbidden` further qualified internally for
  cross-org/cross-snapshot attempts per §17) — MUST NOT let the
  model/API-visible outcome for a cross-org or cross-snapshot handle
  attempt be distinguishable from NOT_FOUND (existence-oracle prevention,
  §17, round 2).
- MUST enforce explicit host-side limits (bytes, count, query length,
  timeout) on every operation before touching underlying storage.
- MUST keep pre-M2 `ContextSnapshot`/`ExecutionContextView` rows
  untouched and MUST report `unavailable` (never a confirmed/fabricated
  zero) dynamic-context telemetry for invocations that predate
  `context_disclosure_events` (§19, round 2: the original wording allowed
  "zero" and "unavailable" to be presented as equivalent, which they are
  not).
- MUST distinguish OPERATIONAL_FAILURE from FORBIDDEN/NOT_FOUND in every
  failure path — storage unavailability MUST NEVER be reported or logged
  as a policy violation.
- MUST retain all `context_disclosure_events` rows across an application-
  level M2 rollback (tools deregistered, schema untouched) — a schema-
  level `down` migration that DROPs the tables is a distinct, destructive
  operation, never the default rollback path once real rows exist (§21,
  round 2).

**MUST NOT**
- MUST NOT allow any `context.*` operation to return content whose
  membership in `context_addressable_resources` was not established at
  the originating snapshot's build time (no live/global corpus queries,
  no post-hoc authorization expansion).
- MUST NOT let a model-supplied/invented handle resolve without a
  matching, independently-validated DB row.
- MUST NOT modify `internal/modelruntime/adapter/*`,
  `internal/modelruntime/costgate`, `internal/modeldispatch`,
  `internal/authorization`'s decision engine, `internal/coderunner`,
  `internal/staging`, or `internal/objectstorage`'s existing key scheme.
- MUST NOT introduce a second provider-visible-render algorithm; dynamic
  content only ever enters the model's context as a Harness tool-result
  message, never spliced into the `contextcompiler`-owned stable prefix.
- MUST NOT duplicate `IsDynamicProviderTier`, `ContextTokenTelemetry`, or
  `ExecutionPrincipal` — reuse via FK/shared function, never redeclare a
  parallel classification.
- MUST NOT become an admission/cost controller — limits defined here are
  OBSERVABILITY + per-operation bounds only, not a billing/reservation
  mechanism.
- MUST NOT let `context.search` issue a live semantic query against `rag`/
  `memory` storage in this milestone (§O model 2, deferred).
- MUST NOT recompile, reinterpret, or retroactively backfill provenance
  for any pre-M2 durable object.

**MAY**
- MAY include a compact, bounded manifest summary of available handles in
  the initial provider-visible render, provided it is produced by the
  existing single-render-algorithm path (`contextcompiler.ResolveProviderContext`)
  and counted within existing M1.2 telemetry, not a side channel.
- MAY apply a live-computed (but deterministic, per §12) ranking function
  within `context.search`'s frozen candidate set.
- MAY extend `context_disclosure_events` with additional nullable,
  additive columns for specific operations (e.g. slice offset/length,
  aggregate member list) as implementation needs dictate.
