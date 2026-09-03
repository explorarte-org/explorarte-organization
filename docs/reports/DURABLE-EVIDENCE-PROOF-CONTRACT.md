# DURABLE-EVIDENCE-PROOF-CONTRACT — Design Proposal

Status: **DESIGN ONLY, NOT AUTHORIZED TO IMPLEMENT.** Follow-up to
`CAPACITY-LIVENESS-INVESTIGATION.md`, which established the finding this
answers: `EVIDENCE_CAPACITY_LIVENESS_INCOMPLETENESS`, real and reproduced
live, not hypothetical. This document proposes a contract, not code, and
authorizes no implementation.

## The three invariants this design does not touch

1. **Obligations remain monotonic.** No discharge, no subsumption, no
   removal from `inForce`. `evidenceRequirementsForRound`'s accumulation
   loop is untouched.
2. **Raw evidence provenance remains strict.** A proof is never a model's
   assertion; it is host-authored, derived only from content the host
   itself classified via the existing `ExcerptRelations` mechanism.
3. **Models cannot self-assert proof or discharge.** No `EvidenceItem` or
   worker-result field can create, extend, or reference a proof directly —
   only the host, at the moment it verifies a citation, may mint one.

Everything below is additive accounting, not a relaxation of any of these.

## Q1 — What makes a proof durable?

A new, immutable table, `evidence_proofs`, written once and never updated —
matching this codebase's own established pattern for exactly this kind of
record (`model_invocation_inputs`, `execution_context_views`: both real,
both enforced immutable by a `BEFORE UPDATE OR DELETE` trigger that
unconditionally rejects both operations). A proof, once minted, is either
valid forever against the SHA it names, or invalidated wholesale by Q4 —
never edited.

```sql
CREATE TABLE evidence_proofs (
    id               BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id  TEXT NOT NULL,
    root_task_id     BIGINT NOT NULL,
    subject          TEXT NOT NULL,
    relation         TEXT NOT NULL,       -- 'definition' | 'application'
    base_sha         TEXT NOT NULL,
    source_reference TEXT NOT NULL,       -- the repository:// citation this proof was minted from
    content_digest   TEXT NOT NULL,       -- sha256 of the excerpt content that earned the classification
    minted_by        TEXT NOT NULL DEFAULT 'host',  -- always 'host'; column exists so the invariant is checkable, not just documented
    invalidated_at   TIMESTAMPTZ,         -- see Q4; the one field this table ever sets after insert
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT evidence_proofs_minted_by_host CHECK (minted_by = 'host')
);
CREATE UNIQUE INDEX evidence_proofs_slot_sha ON evidence_proofs (root_task_id, subject, relation, base_sha) WHERE invalidated_at IS NULL;
```

`minted_by` is redundant with invariant 3 by construction (the write path
is host code, never worker-controlled) but exists as a literal, auditable
column precisely so the invariant is checkable in a query, not just true by
convention.

## Q2 — What SHA/base identity does a proof fix?

`base_sha`, the exact commit `probeAdjudicationRequirements` already reads
via `frozenDesignBaseSHA` — the same pin every other citation in this
system is checked against. A proof is meaningless without it, matching
`VerifiedCitation.BaseSHA`'s existing field.

## Q3 — How does a proof relate to obligations?

By `(subject, relation)` — the exact `EvidenceSlot` shape already in use
everywhere in this package. A proof does not reference an
`EvidenceRequirement` row directly (there is no durable requirement-row
identity to reference today; requirements are reconstructed from
`task_evidence` metadata each read, per `evidenceRequirementsForRound`) —
it references the slot the requirement names. `evidenceSlots(candidate)`
already produces exactly this shape today; a proof answers "is this slot
already discharged" the same way `available` already answers "was this
slot ever shown."

## Q4 — How does a proof get invalidated on WORLD_CHANGED?

Reuse, don't reinvent: `ReasonWorldChangedSinceFreeze`
(`design_freeze_phase.go:131`) already exists as this system's real,
established mechanism for "the promotion target moved since a durable
decision was made about it." A proof's `base_sha` is exactly such a
decision. Rather than a new invalidation pathway, the SAME check that
already stops a run whose promotion target moved should, in the same pass,
mark every `evidence_proofs` row for that `root_task_id` with `base_sha !=`
the new target as `invalidated_at = now()`. An invalidated proof is a
tombstone, not a deleted row — provenance requires knowing a proof once
existed and was retired, not silently vanishing it.

## Q5 — Can one proof satisfy multiple obligations?

Yes, when genuinely true, and this is not new: `ExcerptRelations` and
`ProbeSubjectSupply`'s own comment already establish that one excerpt can
classify as both `definition` and `application` for the same subject
(tonight's `validatePackage` case, and PR #180's fix). A proof is keyed
per `(subject, relation)`, so the SAME `source_reference`/`content_digest`
naturally produces two proof rows sharing identical evidence — this is
already how `PlanSlots`' own coverage accounting treats it (one fragment,
two covered slots), just made durable instead of ephemeral.

What a proof must **not** do is satisfy two *different subjects* from one
citation — that is exactly R5's incident, and PR #180's fix already drew
this line correctly (corroboration is checked per subject, from real
content, never assumed across subjects). This design does not change that
boundary.

## Q6 — Can proofs compose (proof → proof)?

**No — recommended against, deliberately.** A proof always traces to raw
evidence directly, never to another proof. Three reasons, each grounded in
what this investigation already found:

- **Audit simplicity.** `content_digest` must always be checkable back to
  a real, currently-valid excerpt of `base_sha`. A proof-of-a-proof would
  need its own digest scheme over *proof records* rather than *source
  content*, doubling what an auditor must trust.
- **Invariant 3 enforcement gets harder, not easier.** The value of "a
  proof is host-authored, from real content" is exactly that it is one
  hop from the source. Composition reintroduces the possibility of a
  proof whose ultimate grounding was never re-checked at mint time.
- **Q9's own finding doesn't need it.** The capacity problem this
  investigation found is about *raw evidence bytes* being re-paid every
  round for obligations already proven — flat proofs already solve that
  (Q9's accounting below). Composition would solve a different, harder
  problem (proofs *about* proofs) that no live finding has yet shown a
  need for.

## Q7 — How are cycles avoided?

By construction, given Q6: if a proof's only permitted reference is to raw
repository content (never to another proof), the reference graph is
strictly bipartite (proofs → source excerpts) and a cycle is not
representable in the schema at all — no cycle-detection logic is needed
because no edge exists for a cycle to be made of.

## Q8 — How is a proof audited back to raw repository evidence?

`content_digest` (sha256 of the excerpt) plus `source_reference` (the
`repository://repo@sha/path#Lx-Ly` citation) plus `base_sha` together let
any later reader re-fetch the exact named range from `base_sha` and
independently confirm the digest matches — the same auditability
`VerifiedCitation.ResultDigest` already establishes for a single
invocation's result, extended to survive past that one invocation.

## Q9 — How is capacity accounted after persistence?

This is the actual fix to `EVIDENCE_CAPACITY_LIVENESS_INCOMPLETENESS`.
`probeAdjudicationRequirements` currently builds `probeSlots` from the
**entire** cumulative `candidate` set and pays each slot's full raw-excerpt
cost against `jointAdmissionLimits()` every round. Proposed change to that
one function only:

```
for each slot in candidate:
    if a non-invalidated evidence_proofs row exists for (subject, relation, base_sha):
        slot is PRE-SATISFIED -- excluded from probeSlots, costs zero range/byte/line budget this round
    else:
        slot joins probeSlots as today -- pays its real raw-evidence cost

plan = PlanSlots(..., jointAdmissionLimits(), probeSlots)  // only the NOT-yet-proven slots

for each slot newly Covered in plan:
    mint an evidence_proofs row for it (Q1-Q5), from the same content PlanSlots' own
    dry-run already read -- no new repository read, no new model call
```

Tonight's reproduction: subjects 1-6 were already adjudicated (their
citations survived real `VerifyRepositoryCitations` calls in earlier
rounds — genuine proof-minting moments that simply weren't captured
durably). Under this accounting, round 3's probe would cost only
`MissionPolicy.Normalize`'s own 2 slots against the full 16/96KB/400
budget, not 14 pre-existing slots plus 2 new ones. The obligation count
stays monotonic (unchanged, per invariant 1); the *raw evidence being
actively re-transported* does not.

## Q10 — How is "discharged but still in force" represented?

Not as a new state on `EvidenceRequirement` — that type stays exactly as
it is (`Subject`, `Relations`, `Source`), preserving invariant 1's
monotonicity untouched. "Discharged" is purely a derived fact: a
requirement's slot is discharged if and only if a non-invalidated
`evidence_proofs` row exists for it. Nothing is ever removed from
`inForce`; a discharged slot is still, correctly, "in force" — it is just
no longer *unpaid*. This is deliberately a read-time join
(`evidence_proofs` against the existing `inForce` set), not a new column
on the requirement itself, so the requirement's own durable record never
needs to change shape or be rewritten — matching how `available`,
`plan.Covered`, and `plan.Undelivered` are already computed fresh each
call rather than stored as mutable state.

## Q7/Q8 from the investigation doc — the adjudicator's rejection signal

Independent of everything above (the investigation's own observation 3),
`probeAdjudicationRequirements`'s error should carry structured counts, not
only names:

```go
type CapacityConflict struct {
    RequiredRanges, AvailableRanges int
    RequiredBytes,  AvailableBytes  int
    RequiredLines,  AvailableLines  int
    AlreadyInForce  []EvidenceSlot  // fixed, cannot be dropped -- states invariant 1 explicitly
    Undelivered     []EvidenceSlot
}
```

surfaced through the same `result_summary` transport the plain-text
version uses today, so the adjudicator's next attempt can distinguish "ask
for less" from "this exact request is arithmetically impossible given what
is already committed" — the distinction Q8 found missing tonight.

## What this design deliberately leaves for a future document

- The exact SQL migration and Go types for `evidence_proofs` (sketched
  above, not finalized).
- Whether `CapacityConflict` should also surface a suggested minimal set
  to defer (this document does not claim the adjudicator needs that to
  recover — only that it needs to be able to tell an impossible request
  from a merely oversized one).
- Retention/pruning for `evidence_proofs` itself (a real question, given
  tonight's separate `WAVE5-JSON-RETENTION-FINDINGS.md` -- deliberately
  out of scope here).
