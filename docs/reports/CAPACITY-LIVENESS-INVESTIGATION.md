# CAPACITY-LIVENESS-INVESTIGATION

Status: **INVESTIGATION ONLY, NOT AUTHORIZED TO IMPLEMENT.** Scoped narrowly
per the owner's own framing: a real design-adjudication liveness gap
surfaced live during SELF-AUDIT-001 (2026-09-03), and this document answers
the ten questions posed about it, with evidence, before any remediation is
attempted. No code changes accompany this document.

## Live reproduction (not hypothetical)

Root task 18862's design adjudication (`internal/executive/evidence_capability.go`)
repeated the identical rejection three consecutive times:

```
joint evidence capacity cannot deliver, at pin 206c9ef...:
MissionPolicy.Normalize/application, MissionPolicy.Normalize/definition
```

`MissionPolicy.Normalize` is real, exists at
`internal/engineeringmission/policy.go:46`, is called from three real sites
(`policy.go:97,122`, `recovery.go:565,601`, `service.go:239`) — all inside
this campaign's own `AllowedPaths`. Every constituent fact was true. The
request was rejected purely because it could not be added to what six
earlier, independently-adjudicated subjects had already committed the
mission to. Task 18871 (design-adjudication) then exhausted its attempts,
blocking the run.

## Q1 — What exactly accumulates: obligations, raw evidence, or both?

**Both, and they are the same thing at admission time.** `evidenceRequirementsForRound`
(`internal/executive/evidence_requirements_store.go:83`) merges every prior
round's obligations unconditionally (`for candidate := 1; candidate <= round; candidate++`).
`probeAdjudicationRequirements` (`evidence_capability.go:68`) then re-runs
`repositoryevidence.PlanSlots` against the **full accumulated set** — inForce
plus novel — every single time, under one fixed `jointAdmissionLimits()`
(`Limits{MaxFiles: 8, MaxRanges: 16, MaxBytes: 96*1024, MaxSearches: 12, MaxLines: 400}`,
`internal/repositoryevidence/types.go:202`). There is no separate accounting
for "obligations we owe" versus "evidence bytes we must re-show" — owing an
obligation IS re-paying its full raw-evidence cost, every round, against the
same fixed ceiling.

## Q2 — Can an obligation be discharged?

**No, and this is explicit, deliberate design, not an oversight.**
`adoptAdjudicationRequirements`'s own comment states it directly: *"Nothing
already in force is touched -- an adjudicator that wants more binds the NEXT
round, never the contract a design was already written against."*
(`evidence_adoption.go:34`). `withoutSlotsAlreadyInForce` only ever *filters
out* a restated slot from the *novel* set; it has no path that removes
anything from *inForce*.

## Q3 — Can an obligation subsume another?

**No such mechanism exists anywhere in this package.** `evidenceSlots`,
`AdoptEvidenceRequirements`, and `mergeRequirement` all combine and append;
none collapse two obligations into one, and no `EvidenceRequirement` field
records a "this generalizes X" relationship.

## Q4 — Can already-verified evidence become a durable, compact proof?

**Partially exists as a primitive, not persisted as a durable artifact.**
`VerifiedCitation` (`repository_citation_verifier.go:41`) already has the
right shape for a "proof": `Reference`, `BaseSHA`, `TaskID`, `InvocationID`,
`ResultDigest`. But it is computed fresh inside one `VerifyRepositoryCitations`
call and used immediately for that same invocation's review bundle
(`authorizedEvidenceRefs`) — it is never written to durable storage, and
nothing later reads one back. Confirmed by search: `VerifiedCitation` appears
in exactly one file. There is no `proof://` namespace, no durable table, no
digest-only citation form anywhere in this codebase today.

There is, however, a real, already-working **within-one-computation**
compaction mechanism: `ProbeSubjectSupply`'s own comment states *"One excerpt
can prove several roles at once when it physically contains a declaration and
a use closer than one window apart"* (`capability.go:139`) — this is exactly
tonight's `validatePackage` shape, and `GatherWithCoverage`/`PlanSlots`
already exploit it live, inside a single joint-admission dry run. What does
**not** exist is any way to carry that already-computed compact result
*forward* into the next round instead of recomputing (and re-paying for) it
from raw excerpts again.

## Q5 — Is the limit computed per snapshot or per mission?

**Per snapshot** (`Limits` is passed fresh into one `NewExplorer`/`PlanSlots`
call, no mission-level running total exists) **but checked against a
mission-wide, ever-growing demand** (the cumulative `candidate` set from Q1).
This is the precise shape of the liveness gap: a fixed-size bucket, refilled
from empty every round, but required to hold a monotonically growing amount
of water.

## Q6 — Can the demonstration be partitioned across multiple snapshots?

**No.** `PlanSlots` builds one `Explorer` and one `Selection{Terms, RequiredTerms, Slots}`
list, run through `GatherWithCoverage` once, under one `Limits`. There is no
concept of "prove subset A now, subset B next round, adjudicate on the
union of two proof sets" — admission is all-or-nothing over the entire
candidate set in one dry run.

## Q7 — What signal does the adjudicator actually receive on rejection?

A single string: `"joint evidence capacity cannot deliver, at pin %s: %s"`
with `%s` being the sorted, comma-joined `subject/relation` pairs from
`plan.Undelivered` (`evidence_capability.go:110`). It names **what** could
not be delivered. It does not say:
- how many ranges/bytes/lines were requested versus available,
- what is already in force and therefore fixed,
- that the obligation is monotonic and cannot be dropped,
- what action (if any) would change the outcome.

## Q8 — Why did the adjudicator repeat essentially the same request three times?

Directly explained by Q7: the retry loop's only informative signal is "these
two slots didn't fit." Nothing in that signal distinguishes "you asked for
too much in this one request, ask for less" from "this is structurally
unsatisfiable given what you already committed to, and no rephrasing of
this same request will ever succeed." The adjudicator's real action space
(per this session's own read of the retry contract) is limited to producing
another `EvidenceRequirementProposal` — there is no `allowed_recovery_actions`
field, no exposed capacity/obligation ledger, nothing to reason about *why*
a differently-worded version of the same ask would fare any better. Three
identical failures burning three real attempts before the task exhausts is
the direct, mechanical consequence of an unactionable rejection reason.

## Q9 — Does a valid mission exist whose correct solution is impossible under current limits?

**Yes — this is not hypothetical, tonight's own campaign is the proof.**
Six subjects were adjudicated as genuinely necessary across two design
rounds (`MaxDesignRounds` and `MaxDepartmentReplans`-bounded, both real,
both already validated by real revisions this session made earlier). Every
one of those six subjects, and the seventh (`MissionPolicy.Normalize`), is
real: exists in the pinned tree, inside `AllowedPaths`, independently
supplyable per `ProbeSubjectSupply`. No agent produced anything false. No
evidence is missing from the repository. The failure is not "this
particular design was badly formed" -- it is "the protocol's own contract
grew, by its own explicit adoption rule, past what one fixed-size snapshot
can jointly deliver, with no mechanism to shrink it back down." This is an
incompleteness of the protocol, in the sense the owner's framing named it,
not a defect in any one design, agent, or piece of evidence.

## Q10 — Minimum change that recovers liveness without weakening provenance?

Not decided here — this document answers what is true, not what to build.
Three observations bound the shape any real fix should take, grounded in
what was actually found above rather than proposed from first principles:

1. **The failure is in Q1's conflation, not in Q2's discipline.** Obligations
   staying monotonic (Q2, Q3: no discharge, no subsumption) is a real,
   deliberate safety property this session's own reading of the code found
   well-reasoned (AUTONOMY-SMOKE-017-R5's actual incident: silently dropping
   requirements let repository blindness become undetectable). The bug is
   that *raw evidence cost* is currently yoked to *obligation cardinality* —
   Q1 found these are the same accounting today, and Q4 found the primitive
   that could separate them (`VerifiedCitation`) already exists but is never
   persisted. A fix that keeps obligations monotonic while making their
   *evidentiary cost* not monotonic (a durable, digest-anchored proof record
   an obligation can be satisfied *by reference to*, once genuinely
   verified, instead of by re-showing its raw excerpt every round) directly
   targets the actual conflation this investigation found, without touching
   the property Q2/Q3 confirmed is deliberate and sound.
2. **Raising the limits (16/96KB/400 → larger) does not fix Q9's finding.**
   Q9's case is that the *ceiling is fixed and demand is monotonic* — any
   finite ceiling is eventually exceeded by a mission with enough
   legitimately-necessary subjects, so widening it only raises the
   complexity threshold at which Q9 recurs, and (per the owner's own
   framing, independently consistent with tonight's G3-003 finding on the
   ~80K-token floor) trades this failure for the input-size failure this
   session already spent real effort tracing.
3. **Q7/Q8's fix is independent and much smaller than Q1-Q6's.** Regardless
   of whether/how compaction or proof-carry-forward is ever built, the
   rejection reason can be made structurally richer (name the ceiling, the
   requested count, and that in-force obligations are fixed) without
   changing any admission logic — this alone would very likely have stopped
   the adjudicator from spending three identical attempts tonight, and is
   safe to build independently of the larger question in observation 1.

## What this investigation does not claim

This is not classified as an incident against tonight's eight fixes: the
guardrail (`probeAdjudicationRequirements` refusing to promise undeliverable
evidence) is confirmed **WORKING_AS_DESIGNED** — it correctly prevented the
adjudicator from committing to an obligation no snapshot could ground, which
is exactly its job. The gap is entirely in what happens *after* a correct
rejection: the adjudicator's recovery loop, and the protocol's inability to
ever shrink accumulated evidentiary cost even when the underlying obligation
was legitimately and permanently proven.
