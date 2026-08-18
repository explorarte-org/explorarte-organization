# M2 — Addressable Context + Progressive Disclosure: DESIGN

This is a design proposal, not an implementation. No code, schema, or
migration in this repository is changed by this document. All claims about
current behavior are as audited in `AUDIT.md`; this document is DECISION +
RATIONALE for what M2 should become, built strictly on top of the M1/M1.1/
M1.2/M1.3 guarantees audited there.

**Scope split, made explicit in round 3 (P2 finding).** Everything in this
document is what independent review round 3 labeled **M2a**: recovering
*addressability* for content the assembler already omits today
(`context_segments.included=false`) — this milestone does not, by itself,
change what any current invocation already receives inline, and therefore
does not by itself shrink today's initial provider-visible byte count for
any existing task shape. A distinct, later decision — **M2b**: changing
`Assembler.Assemble`'s own admission policy so that some content currently
included inline is deliberately moved to addressable-only — is what would
actually produce the "compact initial context" reduction that motivates
progressive disclosure in the first place. M2b requires its own admission-
policy design (which currently-inline sources become addressable-only, and
under what conditions) and is explicitly out of scope for this document;
§2's goals below describe what M2a alone delivers. **Stated plainly (round
4): Context Assembly V2's token-governance/optimization work is NOT
considered economically complete merely because M2a exists.** M2a builds
real, necessary infrastructure (identity, sealing, disclosure, telemetry,
authorization) that M2b will depend on — but M2a alone changes nothing
about what any current invocation's initial prompt costs. Anyone
evaluating "did M2 reduce our token spend" against M2a alone is asking a
question this document was never designed to answer; that question is
M2b's, and M2b remains a distinct, later, not-yet-designed decision.

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

- Let an execution discover/retrieve additional, already-authorized
  content mid-execution via explicit, bounded, audited host-mediated
  calls, for content that is currently omitted/dropped entirely (this is
  M2a, this document's actual scope — see the scope-split note above; it
  makes previously-unreachable content reachable, it does not by itself
  shrink what is already sent inline today; that reduction is M2b, a
  distinct future decision).
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
naming the seam); **M2b — any change to `Assembler.Assemble`'s admission
policy that would move currently-inline content to addressable-only (round
3 scope-split note, above) — a distinct future decision requiring its own
design, not a small extension of this one.**

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

Note (round 4): `Assemble` treats all three of `SourceApprovedMemory`/
`SourceRAGEvidence`/`SourceWebEvidence` identically at the Go level for
this untrusted/data enforcement — but M2a's own schema-level DECISION
below excludes `SourceWebEvidence` from the addressable universe anyway,
for a *different* reason (I-3 pinning/retention, not I-4 authority) — see
the table's `SourceWebEvidence` row and §6.1's source-kind decision.

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
| `SourceWebEvidence` (web evidence) | **MUST NEVER BE DYNAMICALLY FETCHABLE in this milestone** (round 4 — corrected from round 2/3's "ADDRESSABLE, deferred") | Same untrusted/data authority argument as the other two applies in principle, but `internal/webevidence.Evidence` has an `ExpiresAt` field and its own package doc states evidence is "always task-scoped, always time-limited" and explicitly "[not a] RAG/Memory candidate"; `context_segments.source_kind`'s own CHECK constraint (migration 000006) doesn't even list `'web_evidence'` as a valid value, though `contextengine.SourceWebEvidence` is a real Go `SourceKind`. §6.1's schema (round 4) excludes it from the initial `resource_kind` CHECK entirely, not merely "deferred" — I-3's pinning guarantee cannot be met for content designed to expire. Addable later via an explicit migration only if `internal/webevidence` gains a non-expiring, version-pinned retention mode. |
| Role profile | **MUST NEVER BE DYNAMICALLY FETCHABLE** (in this milestone) | Instruction-bearing; `Assemble` does not (and must not) force it to untrusted/data. Making it addressable would require either weakening its authority when fetched (dangerous) or a new "addressable instructions" authority model M2 does not define. |
| Approved skill content | **MUST NEVER BE DYNAMICALLY FETCHABLE** (in this milestone) | Same reasoning as role profile. |
| Organization AGENT / Department AGENT | **MUST NEVER BE DYNAMICALLY FETCHABLE** (in this milestone) | Same reasoning; also the highest-authority-tier sources in the system (AUDIT.md). |
| Owner constraints | **MUST NEVER BE DYNAMICALLY FETCHABLE** (in this milestone) | Same reasoning. |
| Canonical policy | **MUST NEVER BE DYNAMICALLY FETCHABLE** (in this milestone) | Same reasoning. |
| Project/task instructional context | **MUST NEVER BE DYNAMICALLY FETCHABLE** (in this milestone) | Same reasoning — this is instruction-bearing context, not evidence, even though it is task-specific rather than organization-wide. |
| Potentially-large artifacts (generic) | **MAY INLINE / ADDRESSABLE, case-by-case** | Only if the concrete artifact is itself evidence-shaped (e.g. a large RAG document); M2 does not create a new artifact category — see AUDIT.md §8/§13 and DESIGN.md §26 (rejected: new Context Artifact Store). |
| Canonical context segments already inlined today | **MUST INLINE** (unchanged) | Everything the assembler already includes verbatim stays included verbatim; M2a only adds a disclosure path for what is *omitted*, never removes existing inline content and never introduces an excerpt/full-document relationship (round 4 — see §6.1's M2a resource definition). |

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
- `BindingResolver`/`ResolvedBinding` (§9A) — the component that turns
  `executionharness`'s opaque `ToolExecutionContext` refs into DB-proven
  `ContextSnapshotID`/`ExecutionContextViewID`/
  `RequestingModelInvocationID`. `ToolExecutionContext` itself is an
  `executionharness` type (§9A, round 3) — `contextdisclosure` consumes it,
  does not own it.
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

**M2a resource definition, frozen in round 4 (P3 finding: "larger unit
behind an excerpt" was ambiguous and had no basis in current code):** an
addressable resource is **exactly one evidence `SourceRecord` that the
current `Assemble` call evaluated and omitted** — nothing else. Round 2/3
drafts of this section described a resource as possibly representing "a
larger unit a segment was excerpted from (e.g. a full RAG document a
segment only excerpted)." OBSERVED: `DeterministicAssembler` (`internal/
contextengine/assembler.go`) has no concept of "the full document behind
an excerpt" — it works entirely in terms of `SourceRecord`s it was handed
and a binary included/omitted decision per record; there is no
parent/child or excerpt/full-document relationship anywhere in its domain
model today. INFERENCE: describing M2a resources as potentially
representing a "larger unit" invented an identity relationship
(excerpt → parent) that has no existing contract to be pinned to — exactly
the kind of ambiguity that lets an implementer build something M2a never
actually specified. DECISION: `context_addressable_resources` rows
correspond 1:1 to omitted `SourceRecord`s only. A future "excerpt → full
version" capability, if ever needed, requires its own resource-identity
contract and is explicit future work, not something M2a's schema already
half-supports.

```
id                  BIGINT PK
organization_id     TEXT NOT NULL
context_snapshot_id BIGINT NOT NULL  -- FK (id, organization_id) -> context_snapshots
segment_id          BIGINT NULL      -- FK -> context_segments(id). Round 4:
                                     -- given the M2a resource definition
                                     -- above (1:1 with an OMITTED SourceRecord),
                                     -- this is expected to be NULL for
                                     -- every M2a row in practice (an omitted
                                     -- record has no context_segments row --
                                     -- CHECK (NOT included AND content IS NULL),
                                     -- migration 000006) -- retained as a
                                     -- nullable column for forward
                                     -- compatibility only, not exercised by
                                     -- M2a.
resource_kind       TEXT NOT NULL CHECK (resource_kind IN
                                     ('approved_memory','rag_evidence'))
                                     -- ROUND 4, P1-2: restricted to exactly
                                     -- the two source kinds M2a admits (see
                                     -- the source-kind decision below this
                                     -- table) -- NOT the full
                                     -- context_segments.source_kind set.
                                     -- web_evidence is deliberately excluded
                                     -- (see decision below); role_profile,
                                     -- approved_skill, task_context, etc.
                                     -- are structurally impossible to
                                     -- represent in this table now, not
                                     -- merely discouraged by Go code.
source_reference    TEXT NOT NULL CHECK (length(trim(source_reference))
                                     BETWEEN 1 AND 500) -- same bound as
                                     -- context_segments.source_reference
source_version      TEXT NOT NULL CHECK (length(trim(source_version))
                                     BETWEEN 1 AND 240) -- same bound as
                                     -- context_segments.source_version
authority_tier      TEXT NOT NULL CHECK (authority_tier IN
                                     ('approved_memory','rag_evidence'))
                                     -- ROUND 4: matches resource_kind's
                                     -- restricted set 1:1 for M2a (tier and
                                     -- kind happen to share the same two
                                     -- string values in contextengine's own
                                     -- enums, mirroring context_segments)
authority_priority  INTEGER NOT NULL CHECK (authority_priority = 6)
                                     -- ROUND 4: 6 is the ONLY priority
                                     -- context_segments' own tier->priority
                                     -- CHECK (migration 000006) assigns to
                                     -- authority_tier IN
                                     -- ('approved_memory','rag_evidence') --
                                     -- reusing that exact mapping, not a new
                                     -- one invented for this table.
instruction_class   TEXT NOT NULL CHECK (instruction_class = 'data')
                                     -- ROUND 4: was a free TEXT column;
                                     -- M2a's evidence-only contract (§4B)
                                     -- means this is never anything else --
                                     -- now a DB-level fact, not a Go-level
                                     -- promise.
trust_class         TEXT NOT NULL CHECK (trust_class = 'untrusted')
                                     -- ROUND 4: same reasoning as
                                     -- instruction_class above.
data_class          TEXT NOT NULL CHECK (data_class IN
                                     ('public','organizational','sanitized'))
                                     -- same closed set as
                                     -- context_segments.data_class
                                     -- (migration 000006) -- unlike
                                     -- instruction_class/trust_class, M2a
                                     -- resources CAN vary in data
                                     -- classification (a sanitized memory
                                     -- entry is still evidence, still
                                     -- untrusted, but its data_class is a
                                     -- property of the underlying content,
                                     -- not of M2a's authority ceiling).
may_grant_capabilities BOOLEAN NOT NULL DEFAULT FALSE
                                     CHECK (may_grant_capabilities = FALSE)
                                     -- ROUND 4: was DEFAULT FALSE with only
                                     -- a comment promising it stays FALSE --
                                     -- now a CHECK, so no row can ever be
                                     -- inserted with this TRUE, at the
                                     -- database level, independent of any
                                     -- Go-level validation (I-4/§4B).
content_digest      TEXT NOT NULL CHECK (content_digest ~ '^[0-9a-f]{64}$')
                                     -- sha256 of the full underlying content
                                     -- (was undconstrained TEXT; now matches
                                     -- context_segments.content_hash's own
                                     -- format CHECK)
byte_count          BIGINT NOT NULL CHECK (byte_count >= 0)
inline              BOOLEAN NOT NULL -- true if already present verbatim in a
                                     -- context_segments row (fetch = local read);
                                     -- false if it must be retrieved from its
                                     -- owning subsystem (rag/memory) at read time
search_text         TEXT NULL CHECK (search_text IS NULL OR
                                     octet_length(search_text) <= 4096)
                                     -- added in independent review round 3
                                     -- (P1 finding: context.search had
                                     -- nothing frozen to search against --
                                     -- see §12A). A bounded, immutable
                                     -- excerpt/descriptor computed once
                                     -- during Assemble, from content that
                                     -- is actually available at assembly
                                     -- time (never re-fetched later). NULL
                                     -- for a resource with no reasonable
                                     -- textual excerpt (rare; §12A covers
                                     -- what a NULL means for search).
                                     -- Retention/classification policy for
                                     -- this column: §12B (round 4).
created_at          TIMESTAMPTZ NOT NULL
UNIQUE (context_snapshot_id, source_reference, source_version)
-- append-only (blocks UPDATE/DELETE, per the existing
-- reject_context_segment_mutation trigger pattern -- see §6.1B for why
-- that alone is NOT sufficient to make this table's per-snapshot
-- membership set actually closed, and what closes it)
```

**Round 4 source-kind decision (P1-2).** M2a's initial `resource_kind`/
`authority_tier` CHECK allows exactly `approved_memory` and `rag_evidence`
— **`web_evidence` is deliberately excluded**, reversing round 2/3's
implicit inclusion of `SourceWebEvidence` alongside the other two. OBSERVED
evidence for this exclusion, gathered directly from the repo rather than
assumed: (1) `internal/webevidence.Evidence` (`internal/webevidence/
types.go`) has an `ExpiresAt time.Time` field and the package's own doc
comment states evidence is "always task-scoped, always time-limited,
always untrusted... [see] why nothing here can become a RAG/Memory
candidate" — this is the opposite of what M2's I-3 pinning guarantee
needs (a handle must keep returning its pinned content for as long as the
historical snapshot exists, which for web evidence could easily outlive
`ExpiresAt`). (2) `web_evidence` (`migrations/000033_create_web_evidence.up.sql`)
is its own separate table with a `BEFORE UPDATE` reject trigger and an
expiry index — it was never integrated into `context_segments`'
`source_kind` CHECK constraint (`migrations/000006_create_context_engine.up.sql`),
which still only allows `('canonical_document','owner_constraint',
'organization_agent','department_agent','role_profile','approved_memory',
'approved_skill','project_context','task_context','rag_evidence')` — no
`'web_evidence'` value, even though `contextengine.SourceWebEvidence`
(`internal/contextengine/domain.go`) is a real Go-level `SourceKind` that
`Assemble` will process. INFERENCE: this is a pre-existing gap in `main`,
unrelated to M2 — if `Store.Create` ever tried to persist a
`context_segments` row with `source_kind='web_evidence'`, it would violate
that CHECK constraint today. This is not something M2 introduces or must
fix (out of scope for a docs-only mission, and orthogonal to M2a — noted
here only as independent confirmation that web evidence was never
designed to flow through the same durable-segment machinery approved
memory/RAG evidence use). DECISION: M2a excludes `web_evidence` from the
addressable universe entirely. It can be added later via an explicit
migration if and when its persistence/version-retention story is
independently closed (i.e. if `internal/webevidence` ever gains a
non-expiring, version-pinned retention mode) — not assumed now.

DECISION: this table is scoped **per snapshot**, not global. A resource
that happens to be addressable in two different snapshots gets two rows
(with possibly different `authority_*`/`trust_*` fields, because those are
properties of *this snapshot's inclusion decision*, not of the underlying
content). RATIONALE: reproducibility (§C/§M) requires that "what could this
execution have read" be answerable purely from one snapshot's own rows,
without joining across snapshots or trusting that a resource's
authorization was stable over time.

### 6.1B `context_addressable_resource_sets` — the real seal

> Added in independent-review round 3 (P1 finding: "the sealed universe
> isn't actually sealed").

OBSERVED: the existing append-only pattern this design planned to reuse
(`context_segments_no_update`, `migrations/000006_create_context_engine.up.sql`)
is `BEFORE UPDATE OR DELETE ON context_segments` — it blocks mutating or
removing an existing row, but says nothing about a **new** `INSERT`. Applied
verbatim to `context_addressable_resources`, nothing in the schema as
originally drafted would reject a later `INSERT ... WHERE
context_snapshot_id = S` adding a row to an already-built, already-served
snapshot `S`. That would silently grow `S`'s authorized universe after the
fact — a direct violation of I-1 ("membership established at
`ContextSnapshot` build time"), and the append-only trigger alone gives no
protection against it, because append-only was never designed to bound a
*set's membership count*, only to protect *existing rows'* content.

DECISION: a second table makes "sealed" a verifiable, structural property
instead of a claim in a doc comment:

```
context_snapshot_id BIGINT PK           -- FK (id, organization_id) ->
                                         -- context_snapshots, 1:1
organization_id     TEXT NOT NULL
resource_count       INTEGER NOT NULL   -- exact count of
                                         -- context_addressable_resources
                                         -- rows for this snapshot at seal
                                         -- time
manifest_hash        TEXT NOT NULL      -- sha256 over the sorted list of
                                         -- (resource_id, content_digest)
                                         -- pairs sealed for this snapshot --
                                         -- a second, independent integrity
                                         -- check beyond the count alone
sealed_at             TIMESTAMPTZ NOT NULL
```

`Store.Create` writes exactly one `context_addressable_resource_sets` row
per M2-era snapshot build, **unconditionally** (even when
`resource_count = 0`), inside the same transaction as the snapshot/
segments/addressable-resource rows (§9 step 2).

**Round 4, Problem A — the seal row itself was not protected against
`UPDATE`/`DELETE`.** Round 3 froze `INSERT` rejection once a seal exists,
but never froze the seal row against being deleted and recreated (or
updated in place) — so a conceptual `DELETE seal(S); INSERT extra
resource for S; recreate seal(S)` sequence was not actually excluded by
anything written down, which reopens I-1 exactly the way the original
missing-seal gap did. DECISION: `context_addressable_resource_sets` gets
its own `BEFORE UPDATE OR DELETE` trigger, identical in spirit to
`context_segments_no_update`/`reject_context_segment_mutation`
(`migrations/000006_create_context_engine.up.sql`) — a seal row, once
written, is exactly as immutable as the rows it seals. This is not
optional defense-in-depth; without it, the `BEFORE INSERT` trigger on
`context_addressable_resources` (below) is checking a value that could
itself be deleted out from under it.

**Round 4, Problem B — the naive `BEFORE INSERT` check has a race window.**
A trigger that only asks "does a seal row exist right now?" is not
sufficient under concurrency. Concrete interleaving this design must rule
out, not merely acknowledge:

```
T1 (Store.Create building snapshot S):        T2 (some other inserter):
  BEGIN
  INSERT context_snapshot S
  INSERT resources for S
  [transaction still open, not yet committed]
                                                BEGIN
                                                INSERT resource X for S
                                                BEFORE INSERT trigger fires:
                                                  "does a seal row for S
                                                   exist?" -- NO (T1 hasn't
                                                   written it yet, and even
                                                   if it had, T1's row isn't
                                                   visible to T2 pre-commit)
                                                trigger allows the insert
                                                [T2 blocks or proceeds,
                                                 depending on unrelated
                                                 locking -- nothing in the
                                                 naive trigger forces T2 to
                                                 wait on T1 at all]
  INSERT seal row for S
  COMMIT
                                                COMMIT (if it was never
                                                forced to wait) -- X is now
                                                a member of S's addressable
                                                set even though the trigger
                                                "passed" before the seal
                                                existed.
```

A trigger that isn't forced to wait on `T1` can observe "no seal yet" and
be allowed through, and nothing re-runs the check after `T1` commits — so
a naive `BEFORE INSERT ... IF NOT EXISTS (seal row)` is insufficient by
itself; it must also force a genuine wait.

DECISION: the `BEFORE INSERT ON context_addressable_resources` trigger's
protocol is, in this exact order:

1. **Lock the owning `context_snapshots` row first** — the trigger
   function issues the equivalent of `SELECT 1 FROM context_snapshots
   WHERE id = NEW.context_snapshot_id FOR UPDATE` before doing anything
   else. Standard Postgres row-lock semantics mean this blocks until
   whatever transaction is currently holding that row's lock (i.e. `T1`,
   which inserted the snapshot row and hasn't committed yet) either
   commits or rolls back — `T2` cannot proceed past this line while `T1`
   is still building/sealing `S`.
2. **Only after acquiring that lock**, check whether a
   `context_addressable_resource_sets` row exists for `S`.
3. If it exists (meaning `T1` committed and sealed `S` before `T2`
   acquired the lock): **reject** the insert.
4. If it does not exist (meaning either `T1` hasn't reached that point
   yet in a still-running transaction that `T2` is NOT blocked behind for
   some other reason, or `T1` rolled back entirely, or `S` genuinely has
   no seal because it predates M2): allow the insert, subject to every
   other constraint on the table (§6.1's CHECKs, the `UNIQUE` constraint).

This is deliberately **lock the owning snapshot row, then check the
seal — never check the seal, then lock**: checking first and locking
second is exactly the naive, race-prone order the interleaving above
defeats. Locking first means `T2` cannot observe "unsealed" and then have
`T1`'s seal commit invisibly out from under it — by the time `T2`'s check
runs, it has already waited for `T1` to fully finish (commit or
rollback), so the check sees the true, final state. `internal/tasks/
postgres/create.go`'s `SELECT ... FOR UPDATE` conflict-handling pattern
(established for M1.3's binding fix) is the existing repo precedent for
"lock the row a decision depends on before making the decision" —
applying the same idiom inside a trigger function (Postgres `plpgsql`
supports `SELECT ... FOR UPDATE` in a trigger body) is a standard
technique, not a new mechanism invented for this design.

**Round 4 — honest scoping of the "M2-era" marker.** Round 3 stated
seal-row presence is *the* structural signal that a snapshot is M2-era.
That is true only as a property of one specific writer,
`contextengine/postgres.Store.Create` — not as a database-enforced
invariant that *every* `context_snapshots` row created after some cutover
point necessarily has a seal. DECISION (evaluated both options the
mission brief named): **(A) a canonical `Store.Create` invariant, backed
by a read-path requirement, not (B) a database-enforced "every new
snapshot must have a seal" constraint.** RATIONALE for choosing A over B:
a database-level "snapshot requires seal" rule (e.g. a `DEFERRABLE
INITIALLY DEFERRED` constraint trigger on `context_snapshots` checked at
commit) would need to somehow distinguish "created before the M2 rollout"
from "created after, by a code path that forgot to seal it" — but a
migration only changes schema, not history, and there is necessarily a
window between the schema migration landing and the `Store.Create` binary
change deploying (ordinary rolling-deploy reality, not unique to this
design) during which new snapshots are legitimately created with the old,
unseal-aware code. A hard DB constraint would either have to special-case
that window (fragile, timing-dependent) or reject perfectly legitimate
snapshots created in it. Honest statement of the guarantee instead:
**every snapshot created by the current, M2-aware `Store.Create` is
sealed, unconditionally, inside the same transaction as its other rows —
seal-row absence means either "predates M2 entirely" or "was created
during the rollout window by not-yet-updated code," both of which
correctly and safely fall back to "no addressable universe, same as any
pre-M2 snapshot" (§6.1's read-path requirement, unchanged in spirit from
round 3) — never a false positive claiming an unsealed snapshot has
resources it doesn't, and never a crash or hard rejection of a
legitimately-unsealed historical or rollout-window row.** Test fixtures
and legitimate scripts that construct a `context_snapshots` row directly
(bypassing `Store.Create`) simply produce another instance of this same,
already-handled "no seal" case — not a new category requiring special
handling.

The read path (`context.inspect`/`fetch`/`slice`/`search`/`aggregate`)
MUST require a matching seal row to exist for the current snapshot before
trusting any `context_addressable_resources` row for it, and SHOULD
verify `resource_count`/`manifest_hash` against what it actually reads as
a defense-in-depth integrity check (mirroring the spirit of
`ExecutionContextView`'s own digest-based `ErrExecutionContextViewIntegrity`
re-verification). A pre-M2 snapshot has neither addressable-resource rows
nor a seal row — the **absence of a seal row is itself the honest,
Store.Create-invariant "this snapshot predates M2, or predates this
binary's M2-aware Store.Create" signal**, which is a stronger and simpler
mechanism than the separately-proposed `contextdisclosure_available_since`
config timestamp (§19) — RATIONALE: a config timestamp can be
misconfigured or forgotten; a seal row's presence is a fact recorded
transactionally with the snapshot itself, at the exact moment that
matters, and needs no operator-maintained value to stay correct. §19 is
updated accordingly to drop the config-timestamp recommendation in favor
of seal-row presence.

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
requested_handle         TEXT NULL         -- the raw handle string presented,
                                            -- kept even on failure, for audit.
                                            -- Nullable (round 3 correction --
                                            -- see the note below the table):
                                            -- fetch/slice/single-handle
                                            -- inspect have exactly one;
                                            -- search has none; aggregate has
                                            -- several, recorded instead in
                                            -- aggregate_member_resource_ids
                                            -- below.
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
query_digest              TEXT NULL CHECK (query_digest IS NULL OR
                                     query_digest ~ '^[0-9a-f]{64}$')
                                            -- for search only. ROUND 4
                                            -- (§12B): replaces round 3's
                                            -- raw query_text -- no raw
                                            -- model-composed query text is
                                            -- ever persisted, only its
                                            -- sha256 digest, to avoid
                                            -- accidentally storing a
                                            -- pasted credential/secret
                                            -- durably and indefinitely.
query_byte_count           INTEGER NULL     -- for search only, round 4
                                            -- (§12B), bounded by §16
                                            -- max_search_query_bytes
result_count              INTEGER NULL     -- for search
search_algorithm_id       TEXT NULL        -- for search only, added round 3
                                            -- (§12A) -- mirrors EstimatorID's
                                            -- pattern (§15) so a future
                                            -- ranking-algorithm change is
                                            -- itself auditable
search_algorithm_version   TEXT NULL       -- for search only, added round 3
returned_resource_ids       BIGINT[] NULL  -- for search only, added round 3
                                            -- (§12A) -- the actual ordered
                                            -- result set, bounded by
                                            -- max_search_results (§16)
aggregate_member_resource_ids BIGINT[] NULL -- for aggregate only -- every
                                            -- constituent handle's resolved
                                            -- resource_id, bounded by
                                            -- max_aggregate_handles (§16)
slice_offset               BIGINT NULL     -- for slice only
slice_length                BIGINT NULL    -- for slice only
created_at                TIMESTAMPTZ NOT NULL
-- append-only, same trigger pattern as context_segments / execution_context_views
```

**Round 3 correction**: round 2's draft declared `requested_handle TEXT NOT
NULL`, which cannot cleanly represent `context.search` (no handle at all —
a query instead) or `context.aggregate` (several handles, not one).
`requested_handle` is now nullable and used only for the single-handle
operations (`fetch`, `slice`, and a handle-scoped `inspect`); `search` uses
`query_digest`/`query_byte_count`/`returned_resource_ids` (round 4, §12B —
never raw query text); `aggregate` uses `aggregate_member_resource_ids`.
This replaces the round-2 draft's vaguer "extend with a nullable column as
needs dictate" notes under §11's per-operation specs (§11 is updated to
point here instead of re-describing ad hoc additive columns per
operation).

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
   `insertAddressableResource` for each, **then write exactly one
   `context_addressable_resource_sets` seal row (§6.1B, added in round 3)
   for this snapshot — unconditionally, even when the resource count is
   zero** — inside that same transaction, before `Commit`. The seal write
   is not optional or deferred: it is what makes the addressable universe
   actually closed (§6.1B), not merely append-only. DECISION: `contextengine`
   remains the only writer of
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
   provider-visible view exactly as today (I-5), completely unmodified by
   M2 — **corrected in independent review round 3**: the original wording
   here allowed an optional manifest of available handles "as part of the
   stable prefix," which is wrong. OBSERVED (`internal/contextcompiler/
   contextcompiler_compiler.go`, with a code comment documenting exactly
   this class of bug from R31 hardening): `StablePrefix`/`DynamicSuffix`
   is a real, load-bearing partition — `TierTask`, `TierProject`,
   `TierRAGEvidence`, `TierApprovedMemory`, and `TierApprovedSkill` are all
   *dynamic* tiers precisely because their content is not byte-identical
   across snapshots/tasks, and `StablePrefix` exists specifically to hold
   what genuinely is reusable/cacheable across invocations. A handle
   necessarily encodes `(SnapshotID, ResourceID, ResourceVersion,
   ContentDigest)` — snapshot-specific by construction (§7) — so any
   manifest containing handles is exactly the kind of content
   `StablePrefix` must never hold; putting it there would break the
   cacheability R31/M1.2 exist to protect. DECISION: no manifest is sent
   in this milestone at all — a model that wants to know what's
   addressable calls `context.inspect()` (§11), which needs no prior
   knowledge of any handle. If a future milestone wants to proactively
   summarize available handles up front, that summary MUST be part of
   `DynamicSuffix`, counted as dynamic-context telemetry like any other
   disclosure, and MUST NOT enter `StablePrefix` under any circumstance.
4. `executionharness.RunSpec.Tools` is populated (by whatever caller wires
   up the run — Executive today forbids all tools; a future M2 consumer
   opts in explicitly) with the `context.*` tool definitions.
5. Model requests a tool call; `Runtime.Execute`'s existing loop applies
   unchanged: catalog lookup, `sameToolDefinition` drift check, replay
   guard, `MaxToolCalls` budget, re-authorize, durable-append-before-
   execute.
6. `contextdisclosure.ToolExecutor.Execute` receives a `ToolExecutionContext`
   (§9A) — never a bare `RunIdentity` — first calls its own
   `BindingResolver.Resolve` (§9A, round 3) to turn its opaque
   `InitialContextRef`/`RequestingInvocationRef` into a DB-proven
   `ResolvedBinding{ContextSnapshotID, ExecutionContextViewID,
   RequestingModelInvocationID}`, then validates the handle server-side
   (§7/§10) against that resolved binding, confirms a matching
   `context_addressable_resource_sets` seal row exists for the current
   snapshot (§6.1B — a snapshot with no seal row has no addressable
   universe to read from, full stop), looks up
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
> lacks sufficient identity to satisfy its own contract"); **revised in
> round 3** (P1 finding: round 2's fix made `executionharness` interpret
> `InitialContext.ID`/`ModelResult.InvocationRef` as typed
> Context-Engine/Model-Runtime IDs, which breaks the Harness's own
> provider/subsystem-independence — the exact property AUDIT.md and §23
> otherwise insist M2 must preserve).

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
  durable, and never supplied by the model: `spec.Context.ID` (the
  `RunSpec.Context`/`InitialContext.ID` string, set by whichever caller
  constructs the `RunSpec` — Executive/a future M2 consumer — never the
  model) and `modelResult.InvocationRef` (set on the `ModelResult`
  returned by `r.models.Invoke(...)` for the *current* turn, before the
  tool-request loop runs).
- **Round 3 correction**: `internal/executionharness/types.go`'s own
  `InitialContext.ID` is documented and typed as an opaque `string` — the
  Harness core does not know, and per AUDIT.md's own observation must not
  know, that this string happens to be a `contextengine.Snapshot` ID for
  today's one caller. `modelruntimeadapter/adapter.go:161`'s
  `strconv.ParseInt(projection.Prefix.Context.ID, 10, 64)` and
  `adapter.go:388`'s `strconv.FormatInt(created.ID, 10)` are exactly that
  interpretation — but they live in `modelruntimeadapter`, a package that
  already knows about `contextengine`/`modelruntime` concretely, not in
  `executionharness` core. Round 2's draft moved that interpretation into
  `Runtime.Execute` itself (`ToolExecutionContext.ContextSnapshotID
  int64`), which would make generic `executionharness` implicitly depend
  on Context-Engine/Model-Runtime ID shapes — a coupling AUDIT.md
  correctly flagged as something M1.x deliberately avoided.
- **Round 3 correction, factual**: round 2's draft also stated
  `executionharness.ToolExecutor` "has zero production consumers" — this
  is not quite right. `internal/executive/runtimeadapter/harness.go`
  defines `executiveToolExecutor{}`, a real (if deny-all) implementer,
  registered via `executionharness.New(..., executiveToolCatalog{},
  executiveToolExecutor{}, ...)`, with a static interface assertion `var _
  executionharness.ToolExecutor = executiveToolExecutor{}`. It is never
  actually *reached* in production because Executive's `RunSpec.Tools` is
  always empty and `MaxToolCalls` is `0` (AUDIT.md R-2's real point,
  correctly made, just imprecisely worded) — but it does exist and does
  compile against the port today. Any port signature change mechanically
  requires updating this stub too (no behavior change: it still
  immediately denies/errors) — DESIGN.md states this explicitly so the
  M2.3 implementer isn't surprised by a compile failure in an unrelated
  package.

INFERENCE: `Runtime.Execute` already possesses everything needed to prove
a disclosure call's binding at the exact moment it dispatches to the tool
executor — the question round 3 corrects is *where the interpretation of
that data into typed Context-Engine/Model-Runtime identity happens*, not
*whether the data is threaded through at all*. Resolving this by having
`contextdisclosure` independently "look up the current snapshot for this
TaskID" would reintroduce an *implicit* association exactly like the one
M1.3's `TaskClassOf(actorRoleID)` proxy was removed for; letting
`executionharness` itself parse the ID would break its
provider/subsystem-independence instead. Both are avoidable.

DECISION: `executionharness.ToolExecutor`'s port carries only **opaque
references**, never typed IDs:

```go
// ToolExecutionContext is the trusted binding every ToolExecutor
// implementation is evaluated against. InitialContextRef and
// RequestingInvocationRef are opaque strings from Runtime.Execute's own
// already-durable state (RunSpec construction, never the model; the
// current turn's already-completed ModelResult, never a later turn) --
// executionharness itself never parses or interprets them as anything
// more specific than opaque references, preserving the same
// subsystem-independence RunIdentity/InitialContext already have today.
type ToolExecutionContext struct {
    Identity                RunIdentity
    InitialContextRef       string // == RunSpec.Context.ID, unparsed
    RequestingInvocationRef string // == the current turn's
                                    // ModelResult.InvocationRef, unparsed
}

type ToolExecutor interface {
    Execute(context.Context, ToolExecutionContext, ToolRequest) (ToolExecutionResult, error)
}
```

`Runtime.Execute` constructs `ToolExecutionContext{Identity: spec.Identity,
InitialContextRef: spec.Context.ID, RequestingInvocationRef:
modelResult.InvocationRef}` once per turn (not per tool call — all tool
calls within one turn share the same requesting invocation) and passes it
to every `r.tools.Execute` call in that turn's tool-request loop —
mechanically identical to round 2's draft except that both new fields stay
`string`, unparsed, exactly mirroring how `InitialContext.ID` is already
typed today.

The parsing/interpretation work moves into `contextdisclosure` itself, as
a new, explicitly-owned component:

```go
// BindingResolver turns the opaque refs a ToolExecutionContext carries
// into concrete, DB-proven identity. This is where "InitialContextRef
// happens to be a contextengine.Snapshot ID" and "RequestingInvocationRef
// happens to be a modelruntime.Invocation ID" is known -- knowledge that
// belongs to contextdisclosure (which already legitimately depends on
// contextengine/modelruntime), never to executionharness core.
type BindingResolver interface {
    Resolve(ctx context.Context, toolCtx ToolExecutionContext) (ResolvedBinding, error)
}

type ResolvedBinding struct {
    ContextSnapshotID           int64
    ExecutionContextViewID      int64
    RequestingModelInvocationID int64
}
```

`Resolve` parses `InitialContextRef`/`RequestingInvocationRef` (the same
`strconv.ParseInt` pattern `modelruntimeadapter` already uses) and then
*proves* the resulting IDs against durable storage — confirms the
snapshot exists, confirms the invocation exists and really did reference
that snapshot (mirroring `modelruntimeadapter.Adapter`'s own existing
`validateCreatedInvocation`/`validateExistingInvocation` cross-checks) —
never trusting the parsed values as authoritative until a DB row confirms
them (I-2). `contextdisclosure.ToolExecutor.Execute` calls
`BindingResolver.Resolve` first, before anything else, and treats a
resolution failure as its own INVALID_REQUEST/NOT_FOUND per §17 — never a
silent fallback.

RATIONALE: this satisfies I-1/I-2 for the disclosure boundary while
keeping `executionharness` exactly as subsystem-independent as it is
today — `RunIdentity`/`InitialContext` remain opaque-to-the-Harness
values a caller supplies and a different, caller-specific layer
interprets, which is the same shape the codebase already uses for
`InitialContext.ID` itself (`modelruntimeadapter`, not `Runtime.Execute`,
is what currently knows it's a snapshot ID). `contextdisclosure` is
exactly the kind of package that is *allowed* to know about
`contextengine`/`modelruntime` concretely (§5), so moving the
interpretation there costs nothing architecturally and avoids coupling
`executionharness` to two other subsystems' ID shapes.

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

- **"May this actor/role invoke this `context.*` operation at all?"** — an
  ordinary `internal/authorization` capability check, evaluated once per
  operation the same way any other capability is
  (org+revision+role+capability+resource+action_digest,
  `internal/authorization.EvaluationRequest`). This is boundary #1 from
  AUDIT.md §6.
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
remains the authority boundary for *actions*.** These are not conflated: a
role could be allowed to invoke `context.search` (action-authorized) and
still get zero results because its snapshot has no matching addressable
resources (content-unauthorized) — that is the expected, correct outcome,
not an error.

### 10A. Capability/action matrix (round 4, P2 finding: this was still
illustrative — "`context.search.invoke` or similar" — and must be
normative before M2.2)

DECISION on capability grouping: **two capabilities, not five, not one.**
`internal/authorization` capability IDs observed in the repo
(`internal/authorization/domain.go`, and usage sites across `internal/
executive`, `internal/rag`, `internal/tasks`, etc.) consistently follow a
two-level dotted `<domain>.<verb>` convention — `model.invoke`,
`task.execute`, `organization.activate_skill`, `code.commit`,
`cell.read_clinical_data`, `deployment.request`,
`project.delegate_department` — never a deeper `a.b.c` form, and
`identifierPattern` (`internal/authorization/validation.go`) permits but
the codebase never actually uses one. `context.inspect`, `context.fetch`,
`context.slice`, and `context.aggregate` share an identical privilege
question — "may this actor read an already-membership-proven resource of
this snapshot at all" — there is no meaningful least-privilege distinction
between "may inspect metadata" and "may fetch content" that the
`context_addressable_resources` membership check (boundary #2) doesn't
already enforce independently for each; splitting them into four
capabilities would be capability proliferation without a corresponding
security benefit (mission brief's own "no capability proliferation
without reason" instruction). `context.search` is different in kind, not
degree: it is the one operation whose *output* (ranked results/snippets)
is a function of a query the model supplies, not simply "return what this
handle already names" — a role that shouldn't be able to explore/rank a
snapshot's addressable set at all (even though it could fetch a specific,
already-known handle) is a real, distinct privilege boundary. DECISION:

| Tool | Capability ID | Resource type | Resource ID / scope | Action digest input |
|---|---|---|---|---|
| `context.inspect` | `context.disclose` | `context_snapshot` | `ResolvedBinding.ContextSnapshotID` (§9A) | digest of `(organization_id, context_snapshot_id)` |
| `context.fetch` | `context.disclose` | `context_snapshot` | `ResolvedBinding.ContextSnapshotID` | same |
| `context.slice` | `context.disclose` | `context_snapshot` | `ResolvedBinding.ContextSnapshotID` | same |
| `context.aggregate` | `context.disclose` | `context_snapshot` | `ResolvedBinding.ContextSnapshotID` | same |
| `context.search` | `context.search` | `context_snapshot` | `ResolvedBinding.ContextSnapshotID` | same |

Each row's `internal/authorization.EvaluationRequest` is scoped by
`context_snapshot`, not by the individual resource being disclosed —
scoping by resource would require an authorization evaluation per handle
before boundary #2 even runs, which is redundant (boundary #2 already
proves per-resource membership more precisely than a capability grant
ever could) and would push `internal/authorization` into content-adjacent
decisions it does not otherwise make (mission brief §H's explicit warning
against conflating the two boundaries). `OrganizationRevisionID`/
`ActorRoleID` come from `ResolvedBinding`/`ToolExecutionContext.Identity`
exactly as any other capability check in this codebase already sources
them — no new identity plumbing.

**Model-visible outcome table, made unambiguous (round 4):**

| Situation | Model-visible outcome |
|---|---|
| Action denied (`context.disclose`/`context.search` not granted to this role) | **FORBIDDEN** — this is boundary #1, and does not reveal anything about any specific resource's existence (§17's cross-org existence-oracle concern does not apply here: the denial is identical regardless of which resources exist). |
| Action evaluation itself unavailable (`internal/authorization` cannot be reached) | Neither FORBIDDEN nor a silent pass — mirrors `executionharness.ErrAuthorityUnavailable`'s existing distinct-from-denial semantics (`internal/executionharness/errors.go`): the operation does not complete, no content is returned, and the caller may retry; this is an OPERATIONAL_FAILURE-class outcome, never treated as either "denied" or "authorized." |
| Action allowed, but the specific resource/snapshot doesn't match (boundary #2 fails) | **NOT_FOUND** (per §17's cross-org/cross-snapshot correction — never FORBIDDEN here, to avoid the existence oracle). |
| Action allowed, resource genuinely doesn't exist | **NOT_FOUND**. |

DECISION: **action-level FORBIDDEN and content-level NOT_FOUND are never
the same code path and never collapse into each other.** Round 3's §11
wording for `context.inspect` ("Never FORBIDDEN for the list-all form")
and for `context.search` ("Never FORBIDDEN for zero results") was
correct about *content*-level denial but imprecise as written — read
literally, it could be misunderstood as "this operation can never return
FORBIDDEN at all," which is false: an action-capability denial for
`context.disclose`/`context.search` correctly and normally returns
FORBIDDEN, for any operation, before any content read is attempted.
Corrected wording: **"Never a content-membership FORBIDDEN; an
action-capability denial may still return FORBIDDEN."** §11's per-operation
specs are updated below to say this precisely instead of the ambiguous
"Never FORBIDDEN."

A retrieval must prove, at minimum (mission brief §H): organization match,
snapshot match, execution/invocation match, resource membership, resource
version, and — where the action itself is capability-gated — principal/
role via the existing `internal/authorization` check. Concretely, the
"execution/invocation match" check compares the handle's claimed
`SnapshotID` against `ResolvedBinding.ContextSnapshotID` (§9A, round 3) —
the value `contextdisclosure.BindingResolver.Resolve` produced from
`ToolExecutionContext.InitialContextRef` and DB-verified, never from the
handle's own claims or from a second, independently-trusted lookup by
`TaskID`. All are re-derived server-side per I-2; none are read from
client-claimed handle fields without a DB lookup confirming them.

**Ordering, made explicit (round 4):** action-capability authorization
(boundary #1, `context.disclose`/`context.search`) is evaluated **before**
any handle/resource lookup (boundary #2) for every operation — denial
occurs before any content read is even attempted, so a FORBIDDEN response
carries zero information about whether the requested resource(s) would
otherwise have existed. This mirrors `executionharness.Runtime.Execute`'s
own existing ordering (`AuthorizeExecution` runs before the tool executor
is ever invoked, `internal/executionharness/runtime.go`), extended one
level deeper into `contextdisclosure` for the `context.*`-specific
capability.

## 11. Proposed host capability API

All operations execute inside `contextdisclosure.ToolExecutor`, registered
as `executionharness` tools (never inside a `ProviderAdapter` — see §23).
Every operation below receives a `ToolExecutionContext` (§9A) from
`executionharness` and immediately resolves it to a `ResolvedBinding` via
`BindingResolver` (§9A, round 3) before doing anything else — never a bare
`RunIdentity`, and never the unresolved opaque refs. Names illustrative
per mission brief.

### `context.inspect(snapshot_id implicit, handle?) -> ResourceDescriptor[]`
- INPUT: optional handle filter; if omitted, lists all addressable
  resources for the current snapshot (bounded, §16).
- OUTPUT: `{handle, kind, source_reference (redacted to a stable label,
  not a raw path), byte_count, trust_class, data_class}` — metadata only,
  no content.
- AUTHORIZATION: `context.disclose` action-capability check (§10A) +
  implicit snapshot scoping (no content-membership check needed beyond
  "belongs to this snapshot," since no content is returned).
- BOUNDARIES: current snapshot only.
- SIZE LIMIT: max N resources listed per call (§16).
- TIMEOUT: short (metadata-only, DB read).
- AUDIT EVENT: `context_disclosure_events{operation:"inspect"}`.
- IDEMPOTENCY: fully idempotent, safe to retry/duplicate.
- FAILURE MODE: INVALID_REQUEST (malformed filter) / OPERATIONAL_FAILURE
  (storage unavailable, or the `context.disclose` authorization check
  itself being unavailable — §10A). Never a content-membership FORBIDDEN
  for the list-all form (an empty list is simply the true answer for a
  snapshot with no addressable resources) — but an action-capability
  denial of `context.disclose` itself still returns FORBIDDEN, before any
  list is computed (§10A, round 4 correction to round 3's ambiguous "Never
  FORBIDDEN" wording).

### `context.fetch(handle) -> ContextResource`
- INPUT: one handle.
- OUTPUT: `ContextResource` (§R), content included, up to the byte limit.
- AUTHORIZATION: `context.disclose` action-capability check (§10A) + full
  membership chain from §10.
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
- AUDIT EVENT: `context_disclosure_events{operation:"slice",
  slice_offset, slice_length}` (§6.2 — columns frozen there, round 3).
- IDEMPOTENCY: same handle+range -> same bytes, always.
- FAILURE MODE: same set as fetch, plus INVALID_REQUEST for an
  out-of-bounds range.

### `context.search(query) -> SearchResult[]`
- Full semantics in §12/§12A. Determinism contract frozen in §12A (round
  4) — see there for the full statement; summarized here for the
  operation spec.
- INPUT: bounded query string (§16 length limit), implicit snapshot scope.
- OUTPUT: **deterministically** ranked `{handle, kind, snippet (bounded),
  score}[]` (round 4: no longer "ranking MAY vary" — see §12A), where
  `snippet` is derived from `context_addressable_resources.search_text`
  (§6.1/§12A) — a resource with `search_text IS NULL` is never returned by
  `context.search` (still visible via `context.inspect`, still fetchable
  by handle via `context.fetch` — §12A).
- AUTHORIZATION: `context.search` action-capability check (§10A — a
  distinct capability from `context.disclose`); results are ranked only
  against `search_text` for rows already present in
  `context_addressable_resources` for the current snapshot (never a live
  corpus query, never a re-read of full content — see §12/§12A DECISION).
- BOUNDARIES: current snapshot's addressable set only.
- SIZE LIMIT: `max_search_results` (§16).
- TIMEOUT: bounded (in-snapshot index lookup, not a live vector query).
- AUDIT EVENT: `context_disclosure_events{operation:"search",
  query_digest, query_byte_count, result_count, search_algorithm_id,
  search_algorithm_version, returned_resource_ids}` (§6.2/§12A/§12B —
  columns frozen there; `query_digest`/`query_byte_count` replace round
  3's raw `query_text` column, round 4 — see §12B; `requested_handle` is
  NULL for this operation).
- IDEMPOTENCY: **round 4 correction** — same snapshot seal + same query
  bytes + same `search_algorithm_id`/`search_algorithm_version` MUST
  produce byte-identical `returned_resource_ids`, scores, and snippets,
  always (§12A) — this replaces round 3's "NOT required to be
  byte-identical... if ranking has any live component," which is no
  longer the design's contract.
- FAILURE MODE: INVALID_REQUEST (query too long/empty) / OPERATIONAL_FAILURE
  (index unavailable, or the `context.search` authorization check itself
  being unavailable — §10A). Never a content-membership FORBIDDEN for zero
  results — mirrors `context.inspect` — but an action-capability denial of
  `context.search` itself still returns FORBIDDEN, before any ranking is
  computed (§10A, round 4 correction).

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
- AUDIT EVENT: one `context_disclosure_events{operation:"aggregate",
  aggregate_member_resource_ids}` row referencing all constituent handles
  (§6.2 — column frozen there, round 3; `requested_handle` is NULL for
  this operation).
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
- **Unstable/non-reproducible ranking**: **round 4 correction — no longer
  an accepted trade-off.** Rounds 2/3 left ranking order as "MAY vary,"
  with membership determinism as the only frozen guarantee. Round 4 (P2
  finding) freezes full determinism instead — order, scores, and snippets
  included, not just membership — now that round 3 already froze every
  input the ranking function needs (`search_text`, a bounded query,
  `search_algorithm_id`/`search_algorithm_version`). See §12A's
  determinism contract for the precise statement; there is no longer a
  non-deterministic case for M2.4 to implement.
- **Historical-reproducibility risk**: a `context_disclosure_events` row
  records `result_count` and `returned_resource_ids` (§6.2/§12A — a
  frozen, typed column as of round 3, not merely a recommendation), so a
  historical search call's *result* is durably recorded even if the
  ranking function itself is not literally re-run during audit — the
  audit trail answers "what did it return," which is what reproducibility
  actually requires, not "would a re-run today produce the same order."

## 12A. What `context.search` actually searches against

> Added in independent-review round 3 (P1 finding: "`context.search` has
> no frozen searchable representation to run against").

OBSERVED: §12's original text asserted ranking uses "the already-frozen
excerpt/snippet text stored in those rows" — but the `context_addressable_
resources` schema as drafted through round 2 had no such field at all
(`source_reference`, `source_version`, digest/byte-count/provenance
columns only, no text). Compounding this, `context_segments.content` is
`NULL` by CHECK constraint for any omitted segment
(`migrations/000006_create_context_engine.up.sql`:
`(NOT included AND content IS NULL AND omission_reason IS NOT NULL AND
byte_count = 0)`) — exactly the segments M2 is most interested in making
addressable. So even falling back to segment content wouldn't have
worked for the omitted case, and re-reading the full underlying resource
at every search call would reintroduce a live storage dependency §12
explicitly rejects.

DECISION: `context_addressable_resources.search_text` (§6.1, added round
3) is a bounded (≤4096 bytes), immutable excerpt/descriptor computed once,
during `Assemble`, from whatever content the assembler actually has in
hand at that moment — for an omitted segment, the assembler necessarily
saw the full candidate content before deciding to omit it (that decision
is what it's making), so deriving a bounded excerpt costs nothing extra;
for an `inline=false` RAG/memory resource, `search_text` is typically a
title/summary/excerpt already available on the underlying `SourceRecord`
without a second read. `search_text` may be `NULL` for a resource with no
reasonable textual excerpt (rare) — a `NULL` `search_text` means that
resource simply cannot be *found via `context.search`*, but remains fully
visible via `context.inspect` (list-all) and fetchable via `context.fetch`
by handle — an honest, visible gap, never a silent exclusion from the
addressable universe itself (§4B/§8's sealing guarantees are about
*membership*, which `search_text` nullability never affects).

`context_disclosure_events` (§6.2) gains three additional columns,
specific to `operation='search'` rows: `search_algorithm_id`,
`search_algorithm_version` (mirroring the `EstimatorID`/`EstimatorVersion`
pattern §15 already uses for token estimation — so a future ranking-
algorithm change is itself auditable), and `returned_resource_ids BIGINT[]`
(bounded by `max_search_results`, §16) — the actual ordered result set a
historical search call produced, durably recorded independent of whether
the ranking function itself could be re-run identically during a later
audit. This directly strengthens the "historical-reproducibility risk"
bullet below: it was previously described as a recommended addition;
round 3 promotes it to a concrete, named, typed column.

### Determinism contract (round 4, P2 finding: "`context.search` retains
two incompatible determinism contracts")

OBSERVED: by the end of round 3, `context.search`'s only unfrozen input was
the ranking function's *behavior*, not its *inputs* — the candidate set
(`context_addressable_resources` scoped to the sealed snapshot), the text
being ranked (`search_text`), the query (bounded, §16), and the algorithm's
own identity (`search_algorithm_id`/`search_algorithm_version`) were all
already frozen per-snapshot facts. INFERENCE: once every input to a
function is frozen, there is no remaining justification for treating its
*output* as non-deterministic — "ranking MAY vary" was describing a
freedom the design no longer actually needed to reserve, and kept two
incompatible statements alive simultaneously (§12's "MAY vary" vs §11's
audit columns implicitly assuming a specific returned order worth
recording). DECISION: freeze full determinism.

**Normative statement:** for a given sealed snapshot, the SAME
`(snapshot seal, query bytes, search_algorithm_id, search_algorithm_version)`
tuple MUST always produce the SAME ordered `returned_resource_ids`, the
SAME scores, and the SAME snippets — no exceptions, and no residual
"MAY vary" case. The ranking function MUST be a **pure function of
snapshot-local frozen fields only**: `search_text`, `resource_id`,
`source_reference`, `content_digest`, and the query bytes — nothing else.
It MUST NOT consult a wall clock, a random seed, a live embedding call, the
current state of `rag`/`memory` storage, or any unspecified database
row-ordering (e.g. relying on physical `SELECT` order without an explicit
`ORDER BY` over deterministic columns).

**Tie-break, frozen as a total ordering:** `score DESC, source_reference
ASC, resource_id ASC` — chosen because `source_reference` is already a
stable, human-meaningful identity field on every row (§6.1), and
`resource_id` (the table's own primary key, monotonically assigned) is a
final, guaranteed-unique tiebreaker for the residual case of two rows with
identical `score` AND identical `source_reference` (impossible today given
`UNIQUE (context_snapshot_id, source_reference, source_version)`, but kept
as an explicit last resort rather than leaving any residual ambiguity). A
different total ordering MAY be chosen by the M2.4 implementer if it
better preserves some other real ranking property, but it MUST be total
(no two distinct rows may ever compare as exactly equal) and MUST be
computed only from snapshot-local frozen fields.

**Algorithm versioning:** if the ranking algorithm's behavior ever changes,
`search_algorithm_version` MUST increment — the design MUST NOT mutate an
existing version's semantics in place. A historical
`context_disclosure_events` row's `search_algorithm_id`/
`search_algorithm_version` remains the honest record of which algorithm
actually ran; `returned_resource_ids` (§6.2) remains what it already was
as of round 3 — durable *audit evidence* of what a historical call
actually returned — but round 4 makes clear this is no longer serving as a
*compensating* mechanism for a non-reproducible function (round 3's
framing: "the audit trail answers what did it return... not would a
re-run today produce the same order"). Under round 4's contract, a re-run
today (same tuple, same algorithm version) WOULD produce the same order —
`returned_resource_ids` is now confirmatory audit evidence of a
reproducible fact, not a substitute for one.

**Deferred, explicitly out of M2 scope**: a `context.search` that issues a
*new* semantic query against live RAG (mission brief §O, model 2). See
§22.

## 12B. Plaintext durability policy: `search_text` and search queries

> Added in independent-review round 4 (P2 finding: "`search_text` and
> `query_text` introduce new durable plaintext surfaces without an
> explicit classification/retention/redaction policy").

Round 3 added two new plaintext-bearing columns without a stated content
policy: `context_addressable_resources.search_text` and (originally)
`context_disclosure_events.query_text`. Both can contain organizational
knowledge, user/model-authored text, or — in the query case, since a model
composes it live — potentially sensitive strings a model was never
supposed to write down durably (API keys, credentials, PII pasted into a
query by mistake). Persisting them "because they're useful" without a
policy is not acceptable; each gets its own decision.

### 12B.1 `search_text`

- **Classification**: derived *exclusively* from a `SourceRecord` that
  already passed the same admission/content policy its full content did
  (§6.1's M2a resource definition — an omitted `SourceRecord`, not
  arbitrary text) — `search_text` never introduces content that wasn't
  already going to be durably stored somewhere in this system (the
  underlying `rag`/`memory` store already retains the full record;
  `search_text` is a bounded excerpt of something already durable, not a
  new independent disclosure).
- **Bound**: ≤4096 bytes (§6.1's existing CHECK, unchanged).
- **Data-class preservation**: `search_text` MUST NOT elevate the
  `data_class` its own `context_addressable_resources` row already carries
  (§6.1) — a `sanitized`-classified resource's `search_text` is itself
  implicitly `sanitized`-tier content and must be treated that way by
  anything consuming it (e.g. an `orgctl` inspection tool must respect the
  row's `data_class` when deciding whether to display `search_text`).
- **Never a raw storage path**: `search_text` is derived text/excerpt
  content, never a file path, table name, or storage key (same rule as
  `source_reference`, §11).
- **Never authority**: `search_text` is presentation/ranking data only; it
  is never treated as `instruction_class`/`trust_class`-bearing (§6.1's
  CHECKs already force `instruction_class='data'`/`trust_class='untrusted'`
  for the whole row, and `search_text` inherits that, never escalates it).
- **Immutability/retention**: `search_text` is part of the same append-only
  `context_addressable_resources` row (§6.1) — its retention is identical
  to the row's own retention, which is identical to the owning
  `ContextSnapshot`'s retention (indefinite, historical, per M1.x's
  existing durability model). DECISION: no separate retention policy for
  `search_text` — inventing one would create a second retention clock
  independent of the snapshot it belongs to, which nothing else in this
  system does.
- **Source retirement**: if the underlying `SourceRecord` (a memory entry,
  a RAG chunk) is later consolidated, retired, or superseded, the
  snapshot's own `search_text` is UNAFFECTED — this is the same "historical
  durable objects remain valid AS-IS" principle §19 already states for the
  rest of `context_addressable_resources`. DECISION: honestly, this design
  does **not** promise retroactive deletion of `search_text` (or any other
  M2a column) if the source is later subject to a deletion/right-to-erasure
  request — that promise, if needed, is a property of the *snapshot's*
  retention/deletion story as a whole (`context_segments` already faces
  this exact question for inline content, and M2 does not change or
  extend whatever answer M1.x already has for it). M2a inherits that
  answer rather than inventing a different one for `search_text`
  specifically.

### 12B.2 Search query persistence

**DECISION (mission brief's explicit instruction to choose one): (A) do
not persist raw `query_text`.** `context_disclosure_events` persists
`query_digest` (sha256 of the raw query bytes) and `query_byte_count`
only, alongside the already-frozen `search_algorithm_id`/
`search_algorithm_version`/`returned_resource_ids`.

RATIONALE: OBSERVED — `internal/contentpolicy`
(`internal/contentpolicy/contentpolicy.go`) already exists as this
repo's shared credential-detection/redaction primitive:
`Analyze(text string) Assessment` and `RedactCredentials(text string)
(string, []Finding)`, already consumed by `internal/rag`, `internal/
memory`, `internal/modelruntime`, `internal/tasks`, and `internal/
pdfingest` for exactly this class of problem (ingesting model/user-
composed or externally-sourced text that might carry secrets). A
`context.search` query is model-composed, live, per-invocation text —
structurally the same risk category `contentpolicy` already exists to
handle for other ingestion paths, not a new kind of problem M2 needs its
own sanitizer for. Two options were available: (B) persist a
`contentpolicy`-sanitized/redacted representation, or (A) persist no raw
text at all, only a digest + byte count. DECISION: **A**, not B — RATIONALE:
`query_digest`/`query_byte_count` are already sufficient for every
legitimate use this design actually needs a persisted query for (§I's
attribution requirements, replay/audit correlation, §12A's determinism
contract — which only needs the *bytes* to have been the same across
calls, provable by digest equality, never the literal text) — B would add
a `contentpolicy.RedactCredentials` call on the hot path of every
`context.search` invocation for a benefit (human-readable historical
query text) this design has no stated requirement for, and redaction is
necessarily imperfect (a credential-detection pass can miss a novel
secret shape) — A has no such residual risk because it never stores
plaintext at all, redacted or not. If a genuine future need for
human-readable historical query text emerges, that is a deliberate, later
decision to adopt B — not a default this design should reach for now.

`query_byte_count` (not `result_count`, which already exists) is added
alongside `query_digest`; both are additive columns replacing round 3's
`query_text TEXT NULL` (§6.2, §11's `context.search` spec updated above).

### 12B.3 Consequence for the `content.disclose`/`context.search`
capability boundary and downstream tooling

No `orgctl`/audit-inspection tool built on top of `context_disclosure_events`
can ever display "the query the model searched for" as literal text —
only its digest and byte count. This is a deliberate loss of
human-readable audit convenience in exchange for never persisting
model-composed plaintext that could contain a pasted secret. An operator
who needs to correlate a specific historical search to a specific model
turn already can, via `requesting_model_invocation_id` and the
invocation's own recorded request (wherever `modelruntime` already
persists it, subject to its own existing content policy) — this design
does not need to duplicate that.

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
dynamically fetchable — see §4B), plus `content_digest` and
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
`context.search` capability itself is denied, §10A), which is not an
existence-revealing signal the same way a content-membership FORBIDDEN
would be, since the action check happens identically regardless of any
specific resource's existence.

## 17A. Audit-store unavailability vs content-store unavailability

> Added in independent-review round 3 (P2 finding: "every attempt MUST
> be audited" is impossible if the audit store itself is the thing that's
> down).

OBSERVED: `context_disclosure_events` and the underlying content a
disclosure reads (inline `context_segments.content`, or a live `rag`/
`memory` read for `inline=false` resources) can fail independently, but
`context_disclosure_events` lives in the same Postgres the rest of
`contextengine` uses — so "the audit log is down" and "the content store
is down" are not always distinguishable failures, and are sometimes the
literal same failure.

DECISION: three cases, not one:

1. **Underlying content-store failure, audit DB healthy** (e.g. a
   `rag`/`memory` read times out, but `context_disclosure_events` can
   still be written). Record `outcome=operational_failure` and return
   OPERATIONAL_FAILURE to the caller, exactly as originally designed. This
   is the common case §17's table already covers correctly.
2. **Audit DB itself unavailable** (the same Postgres instance
   `context_disclosure_events` lives in is unreachable). `contextdisclosure`
   MUST fail closed: no content is returned to the model, regardless of
   whether the underlying content read would have succeeded. Persistence
   of the audit event itself cannot be guaranteed in this case by
   definition — there is nowhere durable to record the attempt. This is
   the one narrow, explicitly-acknowledged exception to "every attempt is
   recorded," and it is safe precisely because it is fail-closed: no
   disclosure of content ever occurs without either a successful audit
   row or an explicit, documented reason none could be written.
3. **Both unavailable simultaneously**: same as case 2 — fail closed, no
   content returned.

RATIONALE for making the asymmetry (fail-closed favors "no disclosure
without an audit trail" over "always disclose, log best-effort") explicit
rather than papering over it: a disclosure mechanism that could ever
return content with zero possibility of an audit record — even in a rare
outage window — would be a real, if narrow, accountability gap for
something whose entire design purpose is auditability (§9 step 6, §I).
Refusing to disclose during exactly that narrow window costs availability,
never correctness or auditability. This mirrors the general principle
`internal/executionharness`'s own crash-safety pattern already uses
elsewhere (durable-append-before-execute) — prefer "nothing happened" over
"something happened with no record of it."

The revised M2 CONTRACT MUST bullet (§ above) and TEST_PLAN.md category J
are both updated to test this three-way split explicitly, not just "audit
row exists or OPERATIONAL_FAILURE."

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
  table's existence. **Revised in round 3**: rather than a separately
  operator-maintained config timestamp, the honesty marker is the
  **presence of a `context_addressable_resource_sets` seal row (§6.1B)**
  for the invocation's `context_snapshot_id` — a snapshot with no seal row
  predates M2 by construction (the seal is written unconditionally, inside
  the same transaction, for every M2-era snapshot, even one with zero
  addressable resources); a snapshot *with* a seal row and zero
  `context_disclosure_events` rows for a given invocation is a genuine,
  positive "this invocation made zero dynamic reads." This is strictly
  more reliable than a config timestamp, which could be misconfigured or
  never set, because the seal row is a transactional fact recorded at
  exactly the moment it needs to be true, not an operator-maintained
  value that could drift from reality.

## 20. Migration proposal (paper design only — no migration file reserved or written)

Additive only, following the exact pattern of migrations 000051/000053:

- `context_addressable_resources` — new table, per §6.1. FK to
  `(context_snapshot_id, organization_id) -> context_snapshots(id,
  organization_id)` (same composite-FK pattern as `context_segments` and
  `execution_context_views`, so org isolation is enforced by Postgres, not
  only application code). CHECK constraints (round 4, §6.1) restrict
  `resource_kind`/`authority_tier` to `('approved_memory','rag_evidence')`,
  pin `authority_priority=6`, `instruction_class='data'`,
  `trust_class='untrusted'`, `may_grant_capabilities=false`, and bound
  `content_digest`/`source_reference`/`source_version`/`search_text` —
  mirroring `context_segments`' own CHECK discipline (migration 000006),
  not a new pattern. Append-only via the same `reject_*_mutation` trigger
  pattern (blocks `UPDATE`/`DELETE`) **plus a second, new `BEFORE INSERT`
  trigger (§6.1B) using a lock-then-check protocol (round 4, §6.1B Problem
  B: `SELECT ... FOR UPDATE` on the owning `context_snapshots` row first,
  then check for a sealed `context_addressable_resource_sets` row, then
  allow or reject)** — this is the piece that actually closes the set
  under concurrency, since the append-only pattern alone only ever
  protected existing rows, never bounded new ones, and a naive
  check-then-lock trigger has a race window (§6.1B). `UNIQUE
  (context_snapshot_id, source_reference, source_version)`.
- `context_addressable_resource_sets` — new table, per §6.1B.
  `context_snapshot_id BIGINT PRIMARY KEY` FK to `(id, organization_id) ->
  context_snapshots`, so at most one seal row can ever exist per snapshot.
  Written unconditionally by `Store.Create` in the same transaction as the
  snapshot/segments/addressable-resource rows (§9 step 2) — never a
  separate, later write. **Its own `BEFORE UPDATE OR DELETE` trigger
  (round 4, §6.1B Problem A) makes the seal row itself immutable** —
  without this, round 3's `BEFORE INSERT` guard alone would be checking a
  value that could itself be deleted and recreated.
- `context_disclosure_events` — new table, per §6.2 (round 3: includes
  `search_algorithm_id`/`search_algorithm_version`/`returned_resource_ids`,
  `aggregate_member_resource_ids`, `slice_offset`/`slice_length`,
  `requested_handle` now nullable; round 4: `query_digest`/
  `query_byte_count` replace round 3's raw `query_text`, §12B). FK to
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
  TABLE context_addressable_resources` then `DROP TABLE
  context_addressable_resource_sets` (round 3 addition — §6.1B) — this
  destroys any M2 telemetry/audit history that exists. Because the schema
  is purely additive and no
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
  pure computation of an `AddressableResources` slice (each entry
  including its bounded `search_text` excerpt, §12A, round 3) on the
  returned `Assembly` for omitted `SourceRecord`s (§9 step 2, corrected;
  round 4's M2a resource definition, §6.1 — no excerpt/parent-document
  concept). No I/O added to this function.
- `internal/contextengine/postgres.Store.Create` — additive write of
  `context_addressable_resources` rows **plus exactly one
  `context_addressable_resource_sets` seal row (§6.1B, round 3)** inside
  its existing transaction (§9 step 2, corrected). This, not `Assemble`,
  is the actual write seam.
- `internal/contextdisclosure` (new package) — owns `BindingResolver`
  (§9A, round 3), the component that turns `executionharness`'s opaque
  `ToolExecutionContext` refs into DB-proven `ResolvedBinding`. This is
  the one place in the design where "an opaque ref happens to be a
  contextengine/modelruntime ID" is known — deliberately not in
  `executionharness`.
- `internal/contextengine` (exported surface) — new
  `RenderUntrustedContextResource` function (§9B), extracted from the
  logic `wrapUntrustedData`/`BuildProviderRenderV2` already implement, so
  `contextdisclosure` has one canonical wrapping call instead of a copy or
  a fabricated `Segment`.
- `internal/executionharness` — two seams: (1) the `ToolExecutor` port
  signature changes to accept `ToolExecutionContext` (§9A, round 3 —
  opaque `InitialContextRef`/`RequestingInvocationRef` strings, never
  typed Context-Engine/Model-Runtime IDs) instead of a bare `RunIdentity`
  — this is not fully consumer-free (round 3 correction: `executive/
  runtimeadapter.executiveToolExecutor{}` is a real, if deny-all,
  implementer and must be mechanically updated to the new signature, with
  no behavior change, for the repo to compile); (2) a new
  `ToolCatalog`/`ToolExecutor` implementation (`contextdisclosure`)
  registered by whichever caller opts a run into M2 (not Executive's
  typed-task path, which explicitly forbids tools today and is out of
  scope to change here).
- `internal/modelruntime` — read-only: `contextdisclosure`'s telemetry
  aggregation joins `execution_context_views`/`model_invocation_render_telemetry`/
  invocation identity by FK; nothing in `modelruntime` is modified.
- `internal/rag`, `internal/memory` — read-only, version-pinned reads for
  `inline=false` resources, reusing their existing read APIs (not new
  bespoke queries bypassing their own scoping/authz).
- `internal/authorization` — two new capability checks, frozen in §10A
  (round 4): `context.disclose` (gates inspect/fetch/slice/aggregate) and
  `context.search` (gates search, separately), each evaluated exactly like
  any other capability today; no change to the authorization engine
  itself.

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
  reachable via M2 in this design (§4B — corrected round 3; the addressable
  universe is evidence-only, task/project instructional context is
  explicitly excluded, so "RAG/memory/task document" was already stale
  wording) — a fetched RAG/memory-evidence document's text can never be
  interpreted as an instruction by construction, because the same
  `InstructionClass==InstructionData` contract that already gates inline
  RAG/memory segments (AUDIT.md §2) applies identically to
  dynamically-fetched ones.
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
- **M2.1** — durable `context_addressable_resources` **and
  `context_addressable_resource_sets` (round 3, §6.1B; hardened round 4)**:
  migrations (the CHECK-constrained evidence-only schema, §6.1's
  `resource_kind`/`authority_tier`/`instruction_class`/`trust_class`/
  `may_grant_capabilities` CHECKs; the seal table's own `BEFORE UPDATE OR
  DELETE` immutability trigger; the `BEFORE INSERT` seal-enforcement
  trigger on `context_addressable_resources` using the lock-then-check
  protocol, §6.1B Problem B), Go domain types, the additive write step
  inside `contextengine.Assembler.Assemble` (including `search_text`
  computation, §12A) and `Store.Create` (§9 step 2). No read path yet.
  Includes dedicated tests proving: the seal trigger rejects a late
  `INSERT` (round 3's headline P1); the seal row itself rejects
  `UPDATE`/`DELETE` (round 4); and the lock-then-check protocol actually
  serializes a concurrent insert against an in-flight `Store.Create`
  transaction via an explicit, barrier-coordinated integration test
  against real PostgreSQL (TEST_PLAN.md C8, round 4 — not a
  launch-two-goroutines-and-hope test).
- **M2.2** — `context_disclosure_events` (including its `query_digest`/
  `query_byte_count` columns, round 4 §12B) + the authorization/validation
  chain (§10/§10A — the frozen `context.disclose`/`context.search`
  capability matrix, round 4) as a standalone service, exercised only via
  direct unit/integration tests (no Harness wiring yet) — `fetch`/`slice`/
  `inspect` first (simplest, no ranking concerns), `search`/`aggregate`
  deferred to M2.4. Includes `contextdisclosure.BindingResolver` (§9A,
  round 3) and its own tests proving it correctly resolves and DB-verifies
  opaque refs, and fails closed on an unresolvable/mismatched one.
  Note: this repo-observed pattern (AUDIT.md §2) is "prove the read/write
  contract in isolation before wiring a live consumer" — M1.1/M1.2/M1.3
  all shipped their core algorithm before any behavior-visible integration.
- **M2.3** — the `executionharness.ToolExecutor` port signature change to
  `ToolExecutionContext` (§9A, round 3 — including the mechanical,
  behavior-preserving update to `executive/runtimeadapter.
  executiveToolExecutor{}` this requires) and the
  `contextdisclosure.ToolCatalog`/`ToolExecutor` implementation wrapping
  M2.2, wired into a single, clearly-scoped, non-Executive test consumer
  first (never Executive's typed-task path directly, given AUDIT.md R-2's
  observation that this is the *first* real exercise of the tool-call
  loop).
- **M2.4** — `context.search`/`context.aggregate`, implementing §12A's
  frozen determinism contract (round 4: total-ordering tie-break, pure
  function of snapshot-local frozen fields only, `search_algorithm_id`/
  `search_algorithm_version` versioning discipline) from day one — not a
  "MAY vary" placeholder to be tightened later.
- **M2.5** — telemetry aggregation (§15) and any `orgctl` inspection
  tooling for `context_disclosure_events`.
- **M2.6** — integration + historical-compatibility test suite (§19) run
  against real pre-M2 snapshots/views, plus documentation of the
  seal-row-presence honesty marker (§6.1B/§19, round 3).

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
- MUST evaluate every disclosure call against a `ResolvedBinding` (§9A)
  produced by `contextdisclosure.BindingResolver.Resolve` from
  `executionharness`'s opaque `ToolExecutionContext.InitialContextRef`/
  `RequestingInvocationRef` — themselves derived by `Runtime.Execute` from
  durable state (`RunSpec.Context.ID`, the current turn's
  `ModelResult.InvocationRef`) — and DB-verified before use. MUST NOT
  derive the current snapshot/invocation by any other means (e.g. looking
  it up from `TaskID`), MUST NOT accept it from anything the model
  supplies, and `executionharness` core itself MUST NOT parse or interpret
  these refs as typed Context-Engine/Model-Runtime IDs — only
  `contextdisclosure`'s `BindingResolver` may. *(round 2; refined round 3)*
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
  dynamically fetchable in this milestone. *(round 2)* **Enforced at the
  database level, not merely in Go**: `context_addressable_resources`'
  `resource_kind`/`authority_tier`/`instruction_class`/`trust_class`/
  `may_grant_capabilities`/`authority_priority` CHECK constraints (§6.1,
  round 4) make it structurally impossible to insert a row representing
  anything outside `approved_memory`/`rag_evidence` at
  `InstructionData`/`TrustUntrusted`/no-capabilities — `web_evidence` is
  excluded from this initial set entirely (§6.1's source-kind decision,
  round 4) pending its own independent version-retention story.
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
  attempt whose failure or success does not itself stem from the audit
  store being unavailable — **qualified in round 3** (P2 finding: "record
  every attempt" is physically impossible when the same Postgres instance
  that stores `context_disclosure_events` is itself down). See §17A for
  the precise three-way split (content-store failure with audit DB
  healthy / audit DB itself unavailable / both). Every recorded row uses
  an honest **internal** outcome category (`ok|invalid_request|not_found|
  forbidden|stale_drift|operational_failure`, with `forbidden` further
  qualified internally for cross-org/cross-snapshot attempts per §17) —
  MUST NOT let the model/API-visible outcome for a cross-org or
  cross-snapshot handle attempt be distinguishable from NOT_FOUND
  (existence-oracle prevention,
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
- MUST NOT allow any `INSERT` into `context_addressable_resources` for a
  `context_snapshot_id` that already has a sealed
  `context_addressable_resource_sets` row (§6.1B, round 3) — enforced by a
  Postgres trigger that **locks the owning `context_snapshots` row before
  checking for a seal, never the reverse order** (round 4, P1 finding: a
  check-then-lock trigger has a race window a concurrent inserter could
  exploit before a snapshot's `Store.Create` transaction commits its
  seal — §6.1B's Problem B). A snapshot's addressable universe MUST be
  exactly what was sealed at build time, never larger.
- MUST NOT allow `context_addressable_resource_sets` itself to be
  `UPDATE`d or `DELETE`d, ever (round 4, P1 finding: §6.1B's Problem A —
  round 3 froze new addressable-resource inserts once sealed but never
  froze the seal row itself, which would have let a
  delete-insert-reseal sequence reopen an already-sealed snapshot).
  Enforced by its own `BEFORE UPDATE OR DELETE` trigger, identical in
  spirit to `context_segments_no_update`.
- MUST NOT represent a `context_addressable_resources` row as anything
  other than exactly one omitted `SourceRecord` the current `Assemble`
  call evaluated (round 4, P3 cleanup) — MUST NOT introduce a "larger
  unit a segment was excerpted from," a parent/full-document relationship,
  or any other identity beyond the omitted record itself; `Assemble` has
  no such concept today (§6.1), and inventing one in this document without
  a real contract behind it is exactly the ambiguity round 4 removes. An
  excerpt→full-version capability is explicit future work, not part of
  M2a.
- MUST NOT persist a `context.search` query's raw text anywhere, redacted
  or not (§12B, round 4) — only `query_digest`/`query_byte_count`.
  `internal/contentpolicy`'s `RedactCredentials` MUST NOT be treated as a
  license to store a "sanitized" version instead; the decision is not to
  store query text at all.
- MUST NOT gate `context.inspect`/`context.fetch`/`context.slice`/
  `context.aggregate` and `context.search` behind the same capability
  matrix cell interchangeably, and MUST NOT treat "action denied" and
  "content not found" as the same model-visible outcome or the same
  internal audit category (§10A, round 4) — `context.disclose` gates the
  first four; `context.search` is its own capability; action denial is
  FORBIDDEN, content-membership mismatch is NOT_FOUND, and the two are
  never conflated in either direction.
- MUST NOT let `context.search`'s ranking function consult anything other
  than snapshot-local frozen fields (`search_text`, `resource_id`,
  `source_reference`, `content_digest`, the query bytes) — MUST NOT use a
  wall clock, random seed, live embedding call, live `rag`/`memory` read,
  or unspecified database row ordering (§12A's determinism contract,
  round 4). The same frozen inputs MUST always produce the same ordered
  `returned_resource_ids`, scores, and snippets — there is no longer a
  permitted "ranking MAY vary" case.
- MUST NOT allow a manifest of available handles, or any handle-bearing
  content, to enter `StablePrefix` (§9 step 3, round 3) — handles are
  snapshot-specific by construction (§7) and therefore never
  cross-snapshot-stable; if ever sent proactively, such a summary MUST be
  part of `DynamicSuffix` only.
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
- MAY, in a future milestone, proactively summarize available handles as
  part of `DynamicSuffix` only (never `StablePrefix` — corrected in round
  3, §9 step 3), counted as dynamic-context telemetry like any other
  disclosure. This milestone sends no manifest at all — a model calls
  `context.inspect()` to discover handles.
- MAY apply a live-computed (but deterministic, per §12) ranking function
  within `context.search`'s frozen candidate set.
- MAY extend `context_disclosure_events` with further additional
  nullable, additive columns beyond the operation-specific set §6.2
  already freezes (`slice_offset`/`slice_length`, `search_algorithm_id`/
  `search_algorithm_version`/`returned_resource_ids`,
  `aggregate_member_resource_ids`, round 3), as future operations or
  implementation needs dictate.
