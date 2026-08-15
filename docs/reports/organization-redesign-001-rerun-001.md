# ORGANIZATION-REDESIGN-001 — RERUN-001 audit report

**Mission:** ORGANIZATION-REDESIGN-001
**Run:** RERUN-001 (fresh measurement)
**Window:** 2026-08-14T16:54:37Z → 2026-08-15T00:15:13Z
**Subject under study:** frozen worktree `main=4e0853d3263250be3b2e2bd08a05de37db9720a5` + production read-only DB
**Verdict:** `FOUR_QUESTION_BASELINE_PARTIAL` — 3 of 4 measurements valid
**Cost:** $1.7482 (run cap $5.00) · 286 model calls · 607 tool calls
**Status:** `READY_FOR_V2_DESIGN_REVIEW` · `v2_design_allowed: false` · awaiting human review

---

## 1. Why this run exists

An earlier campaign on 2026-08-14 (07:29Z–08:19Z) produced four findings on the same
four questions. **All four were invalidated.** Root cause was in the instrument, not
in the organization:

`call_deepseek_with_tools` returned the evidence packet only when the model stopped
calling tools. On exhausting its tool budget it returned the literal string
`STOP_CONDITION: max_iters reached without final answer` and **discarded `messages`**,
where every tool result lived. All four collections hit that path. The auditor
therefore received a 54-character non-packet four times and answered anyway.

The clearest damage was on FALSIFY-Q4, whose audit carried four `OBSERVED` claims with
file-and-line citations:

| Cited as OBSERVED (campaign 1) | Exists in the worktree |
| --- | --- |
| `contextengine/compiler.go:87` | no |
| `schema/migrations/2023_snapshots.sql:12` | no |
| `tests/contextengine_test.go:145` | no |
| `deployment/k8s/contextengine-deployment.yaml:32` | no |
| `contextcompiler/ast.go` | no |

The real code lives under `internal/contextengine/` and `internal/contextcompiler/`.
Those findings were moved to `invalidated_findings` and the questions reopened. They
carry **zero decisional weight**; they are retained only as evidence of the failure.

RERUN-001 was authorized as a *new measurement*, not as a repair of those results:
the question is what the system concludes when the instrument works.

---

## 2. Results

| Question | Auditor verdict | Confidence | Disposition | Citations | Cost |
| --- | --- | --- | --- | --- | --- |
| FALSIFY-Q1 — executive orchestrator path | `REPLACE` | MEDIUM | DEFER | 4/4 verified | $0.6398 |
| FALSIFY-Q2 — divergent hard-coded counts | `MAKE_DETERMINISTIC` | MEDIUM | **REJECT** | 6/6 verified | $0.1531 |
| FALSIFY-Q3 — inventory of 45 custom mechanisms | — | — | DEFER (not measurable) | 0/0 | $0.3927 |
| FALSIFY-Q4 — contextengine runtime artifacts | `SIMPLIFY` | MEDIUM | **ACCEPT** | 13/13 verified | $0.2100 |

### FALSIFY-Q1 — measurement valid

The five `purpose='executive_ceo_plan'` `context.snapshot_created` markers (outbox ids
29/38/44/49/55, context_snapshot_id 12–16) are **genuine orchestrator executions** of
the CEO-planning stage, not canaries. No later stage is recoverable: the non-truncated
`outbox_events` log contains zero `department_plan`, `department_review` or
`executive_ceo_closure` events, and every table that would hold plan output, invocation
linkage or closure state was truncated to 0 rows on 2026-08-12.

Counter-evidence recorded: the markers may belong to a seeded/demo flow whose business
goal content cannot be examined, or later stages may have executed via an unobserved
path without emitting the expected outbox events.

Deferred by LUNA because the plan content is irrecoverable. Falsifier: any WAL segment,
`pg_dump` or S3 artifact dated 2026-08-10..12 containing `model_invocation_results` for
invocation 10 with a parsed executive plan, or any outbox event with
`purpose=executive_ceo_closure` on the same correlation chain.

### FALSIFY-Q2 — measurement valid, finding rejected

The count divergence does **not** indicate a truth-boundary failure. It is explained by
(a) a stale, already-corrected test assertion (45/3 vs 46/2) documented as pre-existing
drift in `POST_INCIDENT_VALIDATION.md`, (b) distinct semantic scopes
(`organizational_units=6` total vs `operational=4`), and (c) temporal scope plus the
2026-08-12 truncation (tasks 0→3, outbox 1816→1942).

LUNA rejected the proposed finding. The residual concern stands on its own: hard-coded
counts remain a lightweight truth-boundary vulnerability, since any change to
`role-catalog.yaml` can reintroduce drift unless counts are derived from one canonical
source at validation time.

### FALSIFY-Q3 — not measurable with this instrument

No canonical artifact defining a verbatim 45-mechanism inventory, or a 6/19/20 split,
exists in any searchable source. The surviving `45` in approved sources denotes
role/profile counts, not a mechanism inventory.

The evidence packet cited `CONTEXTO_ARQUITECTURA_ORG_V2.md`, `SKILL.md` and `MEMORY.md`.
The controller's citation gate rejected them as unresolvable, which is correct — but the
reason matters and is **not** model fabrication: `docs/canonical/source-manifest.yaml`
declares 63 `/mnt/data/**` paths (including those exact files, with sha256 and byte
counts) whose **bodies are not retained in the repository**. The four read-only tools
cannot open them.

This is a finding about the measurement boundary, and about provenance: the
organization declares canonical sources it does not retain. Q3 cannot be answered until
either those bodies are available to the auditor or the question is reformulated
against sources that exist.

Note that Q3 also failed in campaign 1, by a different mechanism (a `max_iters`
timeout that GROK then argued was itself evidence of simplification — correctly
rejected by LUNA). Two campaigns, two failure modes, same question.

### FALSIFY-Q4 — measurement valid, finding accepted

**This reverses the invalidated campaign-1 finding.** Contextengine **does** persist
durable artifacts outside `context_snapshots`: `context_segments` rows plus shared
`audit_events` / `outbox_events`. Context assembly is MIXED — the canonical
source→segment→snapshot assembly is performed and persisted by contextengine, while the
per-invocation profile projection and provider render happen ephemerally inside the
modelruntime dispatcher over the already-persisted snapshot.

Thirteen of thirteen cited paths verified. Residual unknowns are declared, including
`model_invocation_render_telemetry` DB state (not queried) and whether
`context.snapshot_reused` / `_invalidated` events ever occurred pre-truncation — none
survive, which is not evidence of absence.

---

## 3. Measurement context

One context item was injected into all four role prompts. **No question wording, id or
scope was changed** (`scope_note.questions_changed: false`).

**CTX-TRUNCATION-001** — Production suffered a data truncation on 2026-08-12. The live
`tasks` table retains only 3 rows, all post-truncation smoke-support tasks.
`outbox_events` was *not* truncated and spans 2026-08-10 → 2026-08-14, retaining 192
`task.created`, 278 `model.invocation_requested`, 91 `model.invocation_succeeded`, 184
`model.invocation_failed` and 80 `task.finalized` pre-truncation events.

Status: **UNAUDITED** — collected during this run, never falsified by GROK nor disposed
by LUNA. It was supplied as a hypothesis to test, with the instruction that a count of
zero over truncated data is not evidence that a mechanism was never exercised.

This matters because the original four questions were framed on zero counts
(0 model_invocations, 0 decision_graph rows, tasks in no_action) that are now known to
be at least partly artifacts of the truncation.

---

## 4. Instrument validation

```
evidence_packets_delivered: 3/4     citation_gate_failures: 0
evidence_gate_blocks: 5             parse_failures: 0
accepts_downgraded_by_gate: 0       worktree_untouched: true
halt_code: none                     candidate_new_questions: none proposed
```

Cost by model — GROK $1.1832 (68%), DEEPSEEK $0.5124, LUNA $0.0526. The cost driver is
how often a question returns to the auditor, not how much evidence DeepSeek gathers:
607 tool calls cost less than half of what 8 audits cost.

### Instrument findings (about the apparatus, not the organization)

1. **`evidence_propagation_missing` (FALSIFY-Q3)** — citations unresolvable because the
   canonical corpus lives outside the measurement boundary (see §2).
2. **`gate_false_positive` (FALSIFY-Q2)** — the controller's own evidence gate required a
   top-level `provenance` key while the packet carried provenance inside each of its 13
   observations. A valid measurement was discarded by the instrument, not by the
   evidence. Detected on review, the packet was re-admitted without re-collection, and
   the gate now accepts nested provenance.

### Controller defects fixed during the run

| Defect | Fix |
| --- | --- |
| Tool results discarded on budget exhaustion | Forced final packet with no tools; `DEEPSEEK_MASTER_V4 §29` makes early emission part of the task; per-turn budget notices at 75% / 90% |
| Auditor called with an empty packet | Evidence gate blocks the call; structural validation (observations, provenance, conclusion, resolvable citations), never length |
| Citations never checked | Deterministic verification of every cited path against the worktree; an ACCEPT with unverifiable citations is downgraded automatically |
| Question identity lost across refinements | Canonical `current_question_id`, preserved through `REQUEST_EVIDENCE`; reconciler reopens stale `IN_PROGRESS` |
| Unbounded refinement | Max 3 rounds per question, then DEFER |
| Malformed YAML lost the packet or the audit | Deterministic scalar repair, then one bounded re-emission |
| One question's failure killed the campaign | Halt severity policy: money, scope and integrity halt; instrument failures degrade that question and the campaign continues |
| "No questions left" reported as done | Saturation requires five conditions, all checked |

Regression harness: `~/redesign-001/_fixtest/test_loop_fix.py`, 87 checks, all model
calls stubbed (no API traffic, $0).

---

## 5. Termination

```
no open questions remain, but audit is NOT saturated:
unresolved HIGH-impact questions left as UNKNOWN: ['FALSIFY-Q1', 'FALSIFY-Q3']
(LUNA stop.required=false, reason='V2 gate is not met; continue to the next material open question.')
```

The run stopped without claiming saturation. `v2_design_allowed: false`,
`shadow_v2_started: false`. No V2 work is authorized by this report.

**Open for human review:** whether Q1 and Q3 are worth pursuing given that both are
blocked by artifacts the instrument cannot reach (truncated tables; unretained canonical
sources), and whether the four-question baseline should be reformulated now that the
zero-count premise is known to be partly an artifact.

---

## 6. Reproduction

| Path | Contents |
| --- | --- |
| `~/redesign-001/state/rerun_report.txt` | this run's mandated FINAL OUTPUT |
| `~/redesign-001/state/campaign_run2_state.json` | run state, findings, halt history, instrument findings |
| `~/redesign-001/state/campaign_loop_state.json` | campaign 1, findings under `invalidated_findings` |
| `~/redesign-001/logs/model_calls.jsonl` | every model call; source of all cost figures |
| `~/redesign-001/logs/rerun.log` | controller log for both runs |
| `~/redesign-001/prompts/deepseek_master_v4.txt` | master prompt used for collection |
| `~/redesign-001/_fixtest/test_loop_fix.py` | offline regression harness |
