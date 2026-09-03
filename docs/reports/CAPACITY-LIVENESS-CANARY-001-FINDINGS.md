# CAPACITY-LIVENESS-CANARY-001 -- FINDINGS

Status: **CLOSED WITH A REPRODUCED LIVE GAP.** PR #183's storage, minting,
invalidation, and exclusion mechanisms are deployed and individually verified,
but the exact cross-round growth claim is **not complete**. V6 proved that
already-existing proofs are excluded from a later probe. V7 then reached the
previously untested crossing case and proved that a first revise cannot use the
current round's valid evidence to admit novel requirements when the cumulative
fresh probe is already over capacity.

## Classification

```text
PR #183
CODE_PRESENT                    = PROVEN
POSTGRES_STORE                  = PROVEN
MIGRATION                       = PROVEN
PRODUCTION_HEALTH               = PROVEN
LIVE_MINT                       = PROVEN             (V6, root 18929)
LIVE_EXCLUSION_OF_SAVED_PROOFS  = PROVEN             (V6, root 18929)

FIRST_REVISE_MINT_THEN_GROWTH   = FAILED_LIVE        (V7, root 18948)
EXACT_CAPACITY_LIVENESS_FIX     = INCOMPLETE
```

This supersedes the earlier `EXACT_LIVE_LIVENESS_FIX = NOT_YET_PROVEN`
classification. The gap is no longer merely an unexecuted scenario: V7
executed it against production and received the old cumulative-capacity
failure before any proof could be minted for that root.

## Decisive result

The durable-proof implementation has two real, independently observed
behaviors:

1. If proofs already exist at the root's frozen `base_sha`, the next
   adjudication excludes those slots from `PlanSlots`. V6 demonstrated this in
   production.
2. On the first adjudication that both validates current evidence and proposes
   capacity-crossing growth, those current slots do not yet have proofs. The
   implementation probes the full current-plus-novel set first and mints only
   after the whole probe succeeds. V7 demonstrated that this ordering rejects
   the growth and leaves zero proofs.

The second behavior is the remaining liveness defect. It is not caused by SHA
drift, citation quality, an unavailable repository sensor, or a guessed
capacity boundary.

## Deterministic precondition

The canary set was measured with the same
`repositoryevidence.PlanSlots(..., DefaultLimits(), 24, slots)` path used by
the host. The original run used commit `2a5bcce`; V7 re-ran the preflight at
its actual frozen design base,
`8a4c342772fee64f8b84c58a14b133bbd07bf9b2`, immediately before submission.
Both commits produce the intended shape:

| Set | Subjects | Slots | Result at `8a4c342` |
|---|---|---:|---|
| Round 1 | `RetryPolicy`, `EmbedItem`, `RankedChunk` | 6 | covered 6/6 |
| Round 2, novel only | `ProgramReservation`, `ControllerRefinementRequest`, `BronzePaper`, `ContextResource`, `SkillAssignment` | 10 | covered 10/10 |
| Fresh cumulative probe | all eight | 16 | covered 15/16; `SkillAssignment/application` undelivered |

The cumulative run consumed all eight file slots. Round 1 and the novel set
each fit on their own. This is the exact crossing shape the durable-proof
mechanism was meant to make live.

## V6 -- live mint and live exclusion proved

V6 was root task **18929**, correlation
`executive:af7997ef69c40595508df36a44d6272f`. It ended at the explicitly
allowed terminal state `design_rounds_exhausted`. All child tasks completed;
there were no dead letters and no exhausted child attempts.

The goal incorrectly required citations at `2a5bcce` after main had advanced
to `8a4c342` through the findings merge. The host correctly froze the mission
at `8a4c342`, and the worker correctly cited that real base. The adversarial
reviewer correctly rejected the contradiction twice, so V6 never requested
the five novel subjects.

That campaign nevertheless produced strong live evidence:

- Adjudication task **18939**, invocation **227**, returned `revise` and
  restated the three already-in-force evidence requirements. Its successful
  probe minted six rows at `2026-09-03 04:20:29+00`: each of the three subjects
  with `definition` and `application`.
- Adjudication task **18947**, invocation **232**, again restated the same six
  slots. `ValidProofs` found the six saved rows at the same base and the probe
  had no raw slots left to gather. No row was inserted, updated, or
  invalidated.
- The final table contained exactly six rows, all at `8a4c342`, with null
  `invalidated_at` values.

Therefore `MintProof` and exclusion of already-saved proofs both ran in a real
production campaign. V6 did **not** prove growth because neither adjudication
proposed the five novel subjects.

## V7 -- the exact growth scenario failed live

V7 was root task **18948**, correlation
`executive:e6a812bfbb1fc7c8ace8c37a2ba56f81`. Submission verified that both the
deployed repository worktree and `origin/main` were still exactly `8a4c342`.
The SHA-drift confounder from V6 was absent.

The first-round worker path completed, department review task **18963**
completed, and adversarial review task **18967** / invocation **239** returned
`verdict=accept` with an empty findings array. The decisive task was design
adjudication **18971**:

| Attempt | Invocation | Result |
|---:|---:|---|
| 1 / 3 | 240 | Model referenced finding IDs the review never raised; correctly rejected as a model-result contract error. |
| 2 / 3 | 241 | Returned the intended `revise`, no accepted/rejected findings, and exactly the five novel subjects with both relations. The host rejected it with the measured cumulative `CAPACITY_CONFLICT`. |
| 3 / 3 | 242 | Again referenced nonexistent finding IDs; correctly rejected as a model-result contract error. |

The exact durable failure summary for attempt 2 was:

```text
CAPACITY_CONFLICT at pin 8a4c342772fee64f8b84c58a14b133bbd07bf9b2:
undelivered=[SkillAssignment/application]
ranges=8/16 bytes=13648/98304 lines=340/400
already_in_force(fixed, cannot be dropped)=
[EmbedItem/application, EmbedItem/definition,
 RankedChunk/application, RankedChunk/definition,
 RetryPolicy/application, RetryPolicy/definition]
```

Task 18971 ended `dead_letter`, 3/3 attempts, reason
`model_result_contract_rejected`. The root reports `failed` using the last
attempt's nonexistent-finding error. That terminal reason must not obscure the
middle attempt: invocation 241 was structurally correct and reached the exact
capacity gate under test.

The final production query for root 18948 returned **zero rows** from
`evidence_proofs`; nothing was invalidated because nothing had been minted.

## Root cause -- proof minting happens after the gate that needs the proof

`probeAdjudicationRequirements` currently performs this sequence:

1. Load cumulative current-round requirements.
2. Add novel requirements proposed by the adjudicator.
3. Load proofs already saved for this root and base SHA.
4. Remove only those already-saved slots from the raw probe.
5. Run `PlanSlots` for everything else.
6. If any slot is undelivered, return `CapacityConflict` immediately.
7. Only after a fully successful plan, call
   `mintProofsForNewlyCovered(...)`.

On V7's first valid growth attempt there could not yet be saved proofs for the
six round-1 slots: this was the adjudication that was supposed to validate and
mint them. The raw probe therefore contained all sixteen slots, failed at the
known old boundary, returned before step 7, and minted nothing. Retrying cannot
change that state.

V6 succeeded at minting only because invocation 227 restated the six current
slots without adding novel ones, so the whole probe fit. Its next adjudication
could then exclude them. V7 shows that this two-event sequence does not fit the
real two-round protocol when the first revise must both settle the current
evidence and bind the next round's growth.

## Why the existing tests did not close this case

The orchestration tests prove useful pieces, but not this transition:

- The exclusion test starts with proofs pre-seeded in the fake store.
- The minting test uses a cumulative set that fits, so the function reaches
  `mintProofsForNewlyCovered`.
- The Postgres integration test verifies persistence and immutability without
  running this campaign transition.

A regression test needs an initially empty proof store, current obligations
that fit alone, novel obligations that fit alone, and a cumulative set that
does not fit. A valid first `revise` must both mint the current covered slots
and admit the novel set after excluding those newly minted slots.

## Separate model-output findings

V7 also reproduced two model reliability issues that are not the capacity
root cause:

- The department planner split a requested single deliverable into three
  worker tasks. Only the three owner-required subjects received authorized
  evidence, so the candidate still reached review with the intended initial
  evidence contract.
- Adjudication invocations 240 and 242 invented review finding IDs. The host
  rejected both correctly. Invocation 241 did not have this defect and is the
  clean capacity-liveness observation.

These failures explain V7's final `dead_letter`; they do not explain or weaken
the independently recorded `CAPACITY_CONFLICT` on invocation 241.

## Disposition of campaign roots

| Root | Outcome |
|---:|---|
| 18875, 18876 | Submission budget too small for CEO planning context. |
| 18878 | Worker exhausted citation-precision attempts before the deterministic methodology. |
| 18879 | Unrelated repository-evidence context lookup failure. |
| 18889 | Planner fragmented the campaign and workers did not converge. |
| 18890 | Demonstrated that citation count is not range count, then exhausted citation attempts. |
| 18910 | Round 1 succeeded; blocked by source reproduction caused by goal wording. |
| 18929 (V6) | `design_rounds_exhausted`; live mint and later exclusion proved, growth never requested because of SHA contradiction. |
| 18948 (V7) | `failed`; clean attempt 249/invocation 241 reproduced the exact cumulative conflict before first-round proofs could be minted. |

## Required next change

Do not run another paid canary with the same implementation. First define and
test a safe ordering that proves current-round delivered slots from host-
verified evidence before pricing novel next-round requirements, without
allowing model-authored evidence or partially covered novel slots to become
proofs. Then deploy that change and repeat the same V7 precondition unchanged.

The success condition for the next canary is objective: six round-1 proofs are
minted, the subsequent raw probe contains only the ten novel slots, all ten are
admitted, and the root ends with sixteen valid proofs at one frozen base SHA.
