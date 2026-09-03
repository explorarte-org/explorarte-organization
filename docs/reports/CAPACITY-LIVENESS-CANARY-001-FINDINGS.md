# CAPACITY-LIVENESS-CANARY-001 -- FINDINGS

Status: **CLOSED FOR TONIGHT. PR #183 remains IMPLEMENTED_AND_VERIFIED.
EXACT_LIVE_LIVENESS_FIX remains NOT_YET_PROVEN -- an open gap, not a
failure.** This document closes out a live-canary campaign against
PR #183 (`evidence_proofs` / DURABLE-EVIDENCE-PROOF-CONTRACT.md), records
two findings that survive independently of any one campaign's outcome, and
hands back a concrete, already-verified methodology for whoever resumes
this.

## Classification

```
PR #183
CODE                    = PROVEN
POSTGRES                = PROVEN
MIGRATION               = PROVEN
ORCHESTRATION_WIRING    = PROVEN
PRODUCTION_HEALTH       = PROVEN

EXACT_LIVE_LIVENESS_FIX = NOT_YET_PROVEN
                         != FAILED
```

The first five rows rest on real unit tests (`evidence_proofs_wiring_test.go`
proving orchestration-level exclusion and minting), a real Postgres
integration test (`evidence_proofs_integration_test.go`, 7/7 subtests
including the immutability trigger), the full `go test ./...` suite (107
packages, 0 FAIL), the real integration suite
(`scripts/test-integration.sh executive`, `COMPLETE_GREEN`, 5/5), and a
live, verified production deployment (`orgd`/`model-worker` healthy at
migration tip 63, commit `2a5bcce`). None of that is in question here.

What was attempted tonight, and did not land, was a *live, multi-round R17
campaign* that empirically exercises the exclusion mechanism end to end
against real model output -- the "PROVEN_IN_REAL_MULTIROUND_EXECUTION"
category distinct from the test-level proof above.

## Finding 1 -- citation count is not repository range count

The first canary design assumed `N subjects x 2 relations = 2N ranges`
against `jointAdmissionLimits().MaxRanges = 16`. This is wrong.
`GatherWithCoverage` coalesces nearby citations into shared fragments, and
separately `explorer.go`'s `MaxFiles = 8` binds *before* `MaxRanges = 16`
does whenever candidate subjects live in few distinct files or the search
sensor pulls in incidental matches from unrelated files (including, found
live tonight, the campaign's own freshly-written investigation docs, whose
prose happens to mention several of the chosen subject names).

Concretely: root 18890 requested 9 subjects / 18 slots in one round and
got through the worker/department_review stage without ever approaching
either cap -- the naive citation-count estimate was off by roughly 2x on
ranges alone, before file-budget effects are even considered. **This
invalidates the first two canary designs' experimental setup, not
PR #183's logic.**

The corrected model, confirmed by direct measurement (see Finding 3):
whether a candidate set exceeds capacity is not derivable from citation
count. It must be measured against the real `repositoryevidence.PlanSlots`
call, with the real `Limits`, against the real pinned tree, before
resources are spent finding out empirically.

## Finding 2 -- LLM worker convergence is a separate, real constraint

Independent of Finding 1: asking a real LLM worker to precisely ground
many simultaneous `repository://` citations in one delivery, within a
3-attempt task budget, converged unreliably across every naive attempt
tonight -- a miscited range, a duplicate range claimed for both relations,
an entirely empty citation set, an oversized summary, a typo'd role ID, a
CEO plan fragmenting into multiple department plans, and (in the run that
came closest) a design document reproducing citation text as prose,
tripping the adversarial-review bundle's source-reproduction guard.

**This is worth its own investigation later, and should not be folded into
CAPACITY-LIVENESS.** It is a property of how much unaided precision a
department worker can sustain per delivery, not of the joint-admission
protocol PR #183 changes. Tonight's partial mitigation -- computing exact
citations host-side and handing them to the worker to transcribe verbatim
-- measurably worked (round 1 of the v4 attempt succeeded on its first
try, where three prior designs failed repeatedly) and is a reasonable
starting point for that future investigation, but was not itself the
subject under test.

## Finding 3 -- a deterministic preflight, and what it found

Before spending on a fourth live attempt, the real admission mechanism was
run locally and read-only, against the actual pinned tree at
`TARGET_SHA=2a5bcce49da803221537bd3a9bdd3b5bacfaf17e`, using the exact
call `probeAdjudicationRequirements` itself makes
(`repositoryevidence.PlanSlots(ctx, "explorarte-organization", TARGET_SHA,
source, repositoryevidence.DefaultLimits(), 24, slots)`, `source` a real
`gitsource.Source` over the deployed worktree) -- no live campaign, no
model calls, zero cost. This is the **CANARY-V4 PRECONDITION**: know,
before paying for a round, whether the chosen scenario actually
reproduces the old ceiling being crossed.

Eight subjects were chosen for maximum physical dispersion (distinct
files, distinct packages, none mentioned in this session's own fresh
docs, to avoid Finding 1's contamination):

| Set | Subjects | Slots | Result |
|---|---|---|---|
| round 1 | `RetryPolicy`, `EmbedItem`, `RankedChunk` | 6 | **covered 6/6** |
| round 2 (new only) | `ProgramReservation`, `ControllerRefinementRequest`, `BronzePaper`, `ContextResource`, `SkillAssignment` | 10 | **covered 10/10** |
| full cumulative (all 8, one fresh probe) | all of the above | 16 | **covered 15, undelivered 1** (`SkillAssignment/application`) |

The cumulative probe genuinely fails -- `MaxFiles = 8` is exhausted before
`SkillAssignment`'s application excerpt (itself found in a documentation
file, not code, which is why it needed its own separate file slot) can be
read. Round 1 alone and round 2's five new subjects alone both admit
cleanly on their own. This is exactly the shape PR #183 exists to route
around: if round 1's three subjects are already durably proven, round 2's
fresh probe only needs to admit the five new ones -- which fit -- instead
of re-admitting all eight -- which do not.

The exact fragment-level citations this preflight computed (via the same
`repositoryevidence.ExcerptRelations` classifier production uses) were
handed to the department worker verbatim in the v4 goal, to remove
Finding 2's derivation burden from the experiment.

## The v4 live attempt

Root task **18910**. Round 1's worker (task 18913) succeeded on its
**first attempt**, citing exactly the three provided references --
confirming the host-computed-citation approach resolves Finding 2's
convergence problem for at least one round. Department review completed.

The campaign then blocked with `adversarial_review_bundle_rejected`:
*"candidate design carries organizational repository source: it
reproduces 48 characters of
repository://explorarte-organization@2a5bcce.../internal/webevidence/rank.go#L1-L40"*.
This is a **separate, correct guard** (the department worker's own
standing contract: *"cite it, do not reproduce it"*) -- triggered because
the v4 goal instructed the worker to *"transcribir exactamente"* the
citation strings, and the worker wrote them into the report's own prose
rather than only into the structured `evidence_refs`/`evidence[].ref`
fields. A second resume pass produced no new task and repeated the
identical block -- the root's own attempt budget (2) was left unspent
(`attempt_count: 0`), consistent with this being a hard block rather than
an automatically-retried failure.

**Round 2 -- the decisive test -- was never reached.** This is a fixable
goal-wording defect in the campaign design, not a finding about PR #183
either way.

## Recommendation for whoever resumes this

The subject/citation set in Finding 3's table is already verified and
does not need to be recomputed. A v5 needs exactly one change: instruct
the worker that citations belong **only** in `evidence_refs` and each
`evidence[].ref` field -- never written out as literal text inside the
document body -- matching the standing worker-task contract precisely
instead of overriding it with "transcribe exactly." Everything else
(subjects, exact fragment references, round split, hard spend cap, single
department, no clustered fields) carries forward unchanged.

## Disposition of tonight's roots

None of the following constitutes evidence against PR #183 -- each died
for a reason unrelated to joint-admission logic:

| Root | Outcome |
|---|---|
| 18875, 18876 | Abandoned unresumed -- submission budget too small for CEO planning's own context cost |
| 18878 | Round-1 worker exhausted 3 attempts on citation precision (pre-Finding-3 methodology) |
| 18879 | Blocked on an unrelated repository-evidence context-lookup failure, not root-caused |
| 18889 | Abandoned mid-flight -- CEO planning fragmented into 3 department plans and 3 worker tasks; never converged |
| 18890 | Reached department_review with 9 subjects / 18 citations without ever approaching either cap (Finding 1), then exhausted 3 worker attempts on citation precision |
| 18910 | Round 1 succeeded (Finding 3's methodology validated); blocked pre-round-2 by an unrelated adversarial-review guard |

None of these roots should be resumed or reused -- each is left as
historical evidence of its own specific, already-diagnosed failure mode,
exactly as recorded above.
