# ORGANIZATION-REDESIGN-001 — RERUN-002-CLEAN-ROOT

```
MISSION:                     ORGANIZATION-REDESIGN-001
PHASE:                       RERUN-002-CLEAN-ROOT (+ citation-gate and reliability repairs)
FINAL_VERDICT:               CLEAN_BASELINE_VALID   (3/3 measurements valid)
MEASUREMENT_ROOT:            /home/ubuntu/campaign-52275ca
MEASUREMENT_SHA:             52275cadf8bc8270e11509e10a89746e601fdf53
TOTAL_RERUN_002_COST:        $2.1028  (cap $3.00)
MISSION_CUMULATIVE:          $5.3239  (hard cap $35.00)
PRODUCTION_MUTATED:          false
V2_DESIGN_ALLOWED:           false
QUESTION_EXPANSION_EXECUTED: false
```

**Window:** 2026-08-15T02:48:56Z → 2026-08-15T13:43:33Z
**Supersedes:** RERUN-001, whose evidence was collected with `tool_root != citation/integrity_root`
(`ROOT_MISMATCH_MATERIAL_IMPACT`). RERUN-001 findings carry zero decisional weight.

---

## 1. Why this run exists

RERUN-001 measured four questions with a broken instrument: the code-reading tools resolved
against `/home/ubuntu/pdf-chunk-overflow@f3c49066` while citation verification and the integrity
fingerprint resolved against `/home/ubuntu/build-4e0853d3@4e0853d3`. Every tool call of that run
was replayed against both trees; all four questions consumed differing output (untracked
artifacts visible in root listings, and in Q3's case an untracked binary actually read).

RERUN-002 re-measured Q1, Q2 and Q4 from `UNKNOWN` on a single canonical root, with no prior
verdict, disposition or conclusion exposed to any model.

---

## 2. Instrument state

```
measurement_root == tool_root == citation_root == integrity_root
clean tree required; startup fails closed otherwise
offline regression harness: 141 checks, ALL PASS
```

Three defects were found and repaired **during** this run. Each had already destroyed a valid
measurement before being fixed.

### 2.1 Citation extractor

The gate treated every path-like token anywhere in a packet as a claim that must resolve inside
the measurement root. Four false-negative classes were confirmed against real cases:

| Case | Before | After |
| --- | --- | --- |
| `.github/workflows/ci.yml` and 3 sibling workflows | rejected (leading dot stripped) | `REPOSITORY_CITATION` |
| `_test.go` | rejected as a missing file | `GENERIC_PATTERN` (describes 233 files, names none) |
| `pre-07199f4-…sql` | rejected as external | `CONTEXT_EVIDENCE_REFERENCE`, valid only because registered with sha256 + bytes |
| `boundary-repair-001.md` inside a prose field | rejected | `PROSE_MENTION` — the full path was already verified elsewhere in the same packet |

Citations are now typed by **where the claim is made**: tokens inside provenance-bearing fields
(`provenance`, `citations`, `observations`, `findings`, …) are verified with unchanged strictness;
tokens appearing only in narrative are recorded, not enforced.

The gate was **not** loosened. An asserted repository path that does not exist is still
unverifiable; an unregistered external path is still rejected; a fabricated
`OBSERVED (path:line)` claim inside `observations` is still caught — a first draft of this repair
silently lost that detection and the harness caught the regression.

### 2.2 Transport retry

`IncompleteRead(2891 bytes read)` killed RERUN-002's first attempt at Q2 mid-collection and cost
the whole run. Transport failures are now retried at the single point all three providers share:
2 bounded retries, 2s/6s backoff, identical request body (hash-verified), partial bytes discarded
and never merged, one JSONL record per attempt. Exhaustion raises `TransportFailure` —
explicitly **not** a model reasoning failure.

During this phase the retry fired for real: **20 transient failures across 80 attempts, 16
retries**. Without it the run would have died again.

Replay of a failed model turn is safe here because `tool_policy = READ_ONLY`. This does not
generalise to side-effectful tools.

### 2.3 Supervisor process identity

A supervisor waited indefinitely because `pgrep -f <pattern>` matched watcher shells whose own
command lines contained the pattern. Process identity is now exact: the executable must be
python and the script must appear as its own argv element. Tests prove that a watcher shell
mentioning the pattern is not the controller — and that fuzzy `pgrep` would still confuse it.

---

## 3. Results

| Question | Auditor verdict | Confidence | Disposition | Citations | `question_total_cost` |
| --- | --- | --- | --- | --- | --- |
| FALSIFY-Q1 | — | — | **ACCEPT** | 5/5 both rounds | $0.4328 |
| FALSIFY-Q2 | `DELETE` | MEDIUM | **ACCEPT** | 8/8 | $0.6482 |
| FALSIFY-Q4 | `KEEP` | MEDIUM | **DEFER** | 4/4 | $0.5998 |

`question_total_cost` is cumulative **per question within RERUN-002**, not the increment of the
final continuation phase. The continuation that followed the instrument repairs cost
`continuation_incremental_cost = $0.3617` (DeepSeek 51, Grok 2, Luna 3), moving RERUN-002 from
$1.7412 to $2.1028. Adding the per-question totals to the continuation increment would double-count.

### FALSIFY-Q1 — executive orchestrator path

Measurement preserved from the clean run; not re-executed in later phases.

The run established the **root cause of the 2026-08-12 data loss**: a misdirected integration
test executed `TRUNCATE organizations … CASCADE` against the shared development database.
Separately, the outbox consumer path is decisively **not executed** — 0 claims, 0 attempts,
0 publishes across all 1942 outbox rows. The exercised-path fraction remains UNKNOWN because the
truncation destroyed the denominator's source tables.

See §5 — this is a data-safety finding, not remediated here.

### FALSIFY-Q2 — divergent hard-coded counts

**The defect is LOCALIZED, not systemic.** The stale fixture
`docs/canonical-single-provider-test` (45 imported / 3 proposed, 7 operational departments) is
unreachable by any test, CI, build or deployment path in the frozen worktree, and the validator
defect was a single hardcoded 45/3 assertion in one integration test, already corrected to 46/2
on current main.

Counter-evidence recorded and not flattened: the fixture remains a committed artifact whose
divergent counts form an unsynchronised parallel representation; validator behaviour was derived
from source and `docs/handoffs/POST_INCIDENT_VALIDATION.md`, not execution-observed; a zero-match grep cannot
see untracked or future consumers. This points at a systemic gap in **test-data hygiene and
post-incident cleanup**, which is a different claim from the localized assertion drift.

Falsifier: executing the integration test against the stale fixture path, or
`git log -- docs/canonical-single-provider-test`.

### FALSIFY-Q4 — contextengine runtime artifacts

**UNKNOWN, and deliberately so.** The 296 `context.snapshot_created` events are immutably linked
to production `Store.Create` transactions (single emitter; snapshot, audit and outbox written in
the same transaction), and the 296 snapshot rows were **durably committed and then removed
out-of-band**. The current zero-row state is therefore a truncation artifact, **not**
ephemerality — the opposite of what the invalidated campaign-1 finding asserted. Ongoing durable
persistence is equally not established, because the rows no longer exist.

Luna deferred rather than accepting. Unknowns are declared, including that the sequence state is
permission-denied and pre-freeze git history is not inspectable with the four read-only tools.

### Auditor verdict vocabulary — checked, no drift

Q2's `DELETE` was verified against the instrument's own contract before freezing:

```
audit_result_schema_v3.yaml:  KEEP | MERGE | SIMPLIFY | DELETE | REPLACE | MAKE_EPHEMERAL | MAKE_DETERMINISTIC
grok_master_v3.txt:           same seven values
```

`DELETE` is inside the enum. **No `AUDITOR_SCHEMA_DRIFT`.** Q2's `grok_verdict = DELETE` is
schema-valid.

```
MANDATE_SCHEMA_TAXONOMY_MISMATCH
impact_on_RERUN_002 = NONE
```

The runtime schema is authoritative for future campaigns. No historical model output was modified
and no schema was changed in this phase.

The divergence runs the other way and is recorded here: `INSUFFICIENT_EVIDENCE`, listed as an
allowed verdict in the phase mandate, does **not** exist in the instrument's enum. The auditor
could never emit it. Where an audit lacks evidence it must currently express that through
`confidence`, `measurement_limitations` and `unknowns` rather than as a verdict. Reconciling
mandate vocabulary with the schema is pending; it affects no result in this run.

---

## 4. Protocol deviation, detected and corrected

After the transport failure, the state reconciler reopened Q2 and Luna issued a **new** task
instead of resuming the pending third refinement. A deterministic equivalence check compared the
two:

```
Q2_RESUME_EQUIVALENCE_CHECK = NEW_REFINEMENT_TASK
identical: question_id · refinement 3/3 · prior_audit_hash · tool_policy · measurement_root
different: task objective and scope
```

Task A targeted a specific unresolved point (validator execution status; reachability and
provenance of `canonical-single-provider-test`). Task B re-investigated Q2 from scratch.

Run B was aborted mid-collection and recorded as `ABORTED_PROTOCOL_DEVIATION`
(cost $0.0529, 13 calls, `decisional_weight: 0`, evidence not used in the disposition). Task A
was restored verbatim and verified by capturing the prompt from the controller's own code path
in both states:

```
prompt A original   2338abf97a8f9b38d961a51e598b2ee48cd9088d  (3721 chars)
prompt A restored   2338abf97a8f9b38d961a51e598b2ee48cd9088d  (3721 chars)
```

The refinement counter was not reset.

---

## 5. Data safety finding — separate remediation required

```
truncate_root_cause_observed: true
remediation_executed:         false
```

**DATA-SAFETY-001** — frozen as an independent finding, deliberately not merged into the V2
redesign track:

```
destructive test  ->  wrong shared DB  ->  TRUNCATE ... CASCADE
                  ->  state loss  ->  subsequent destruction of historical observability
```

The second-order damage is the part that shaped this whole campaign: the truncation did not only
destroy state, it destroyed the ability to observe what the system had done. Q1's UNKNOWN
fraction and Q4's DEFER both trace back to it.

It warrants remediation before destructive tests continue to run against shared infrastructure —
but fixing it must **not** retroactively alter this baseline, which is why nothing was remediated
during measurement.

---

## 6. Termination

```
no open questions remain, but audit is NOT saturated:
unresolved HIGH-impact questions left as UNKNOWN: ['FALSIFY-Q4']
```

Luna's own reason for continuing: the benchmark, incident replay and final adversarial review
remain outstanding. **A valid baseline is not a completed audit.**

`v2_design_allowed: false`. `shadow_v2_started: false`. No finding here authorizes deletion,
refactor or V2 design — including Q2's `DELETE` verdict, which is an auditor recommendation about
a stale fixture, not an authorization.

---

## 7. What this run cost, and where it went

| | |
| --- | --- |
| RERUN-002 total | $2.1028 |
| Mission cumulative | $5.3239 of $35.00 |
| Grok share (mission) | $3.3932 — 66% |
| DeepSeek share | $1.5665 |
| Luna share | $0.1661 |

The cost driver remains how often a question returns to the auditor, not how much evidence is
gathered. Three of the mission's five dollars bought measurements that were later invalidated —
by fabricated evidence, then by a contaminated root. The expensive failure mode of this campaign
has consistently been the instrument, not the budget.

---

## 8. Evidence references

| Path | Contents |
| --- | --- |
| `~/redesign-001/state/rerun002_report.txt` | mandated FINAL OUTPUT of this run |
| `~/redesign-001/state/campaign_run3_state.json` | run state, findings, rulings, aborted runs |
| `~/redesign-001/state/reliability_replay.json` | Q4 extractor replay, Q2 resume point |
| `~/redesign-001/state/q2_equivalence.json` | A vs B equivalence comparison |
| `~/redesign-001/state/root_mismatch_replay.json` | RERUN-001 per-call replay against both trees |
| `~/redesign-001/logs/transport_attempts.jsonl` | every transport attempt, with request hashes |
| `~/redesign-001/logs/tool_trajectory.jsonl` | every tool call with resolved root and result hash |
| `~/redesign-001/_fixtest/test_loop_fix.py` | offline regression harness, 141 checks |

Raw evidence is not copied into git. See
[`organization-redesign-001-evidence-manifest.md`](organization-redesign-001-evidence-manifest.md)
for sha256 binding of the earlier artifacts.

## Related

- [`organization-redesign-001-rerun-001.md`](organization-redesign-001-rerun-001.md) — superseded measurement
- [`organization-redesign-001-boundary-repair-001.md`](organization-redesign-001-boundary-repair-001.md) — historical evidence boundary
- [`organization-redesign-001-evidence-manifest.md`](organization-redesign-001-evidence-manifest.md)
