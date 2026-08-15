# ORGANIZATION-REDESIGN-001 — BOUNDARY-REPAIR-001

```
MISSION:                     ORGANIZATION-REDESIGN-001
PHASE:                       BOUNDARY-REPAIR-001
INPUT:                       RERUN-001 = FOUR_QUESTION_BASELINE_PARTIAL
FINAL_VERDICT:               BOUNDARIES_EXHAUSTED_WITH_IRRECOVERABLE_UNKNOWN
MODEL_SPEND:                 $0.00
V2_DESIGN_ALLOWED:           false
QUESTION_EXPANSION_EXECUTED: false
```

**Date:** 2026-08-15T00:53:05Z
**Scope:** resolve only the evidence boundaries left open by FALSIFY-Q1 and FALSIFY-Q3
in RERUN-001. No architecture change, no question reopening, no model execution.

FALSIFY-Q2 (finding REJECTED) and FALSIFY-Q4 (finding ACCEPTED) were not reopened.

## Methodological principle enforced

```
failure_to_observe(X) != evidence_of_absence(X)
```

The four layers are kept separate throughout: **system under study** (the organization),
**instrument** (controller, prompts, parsers, tool harness), **observer** (DeepSeek, Grok,
Luna), **meta-validation** (tests and audit of the instrument). Instrument defects are
not evidence about the organization.

---

## 1. Q1 — forensic boundary repair

```
executive_ceo_plan execution:   OBSERVED
downstream historical chain:    IRRECOVERABLE WITH RETAINED EVIDENCE
mechanism_absent:               NOT ESTABLISHED
classification:                 Q1_HISTORICAL_EVIDENCE_IRRECOVERABLE
                                (WITH CURRENT RETAINED/ACCESSIBLE EVIDENCE)
```

### Search performed — within the accessible measurement boundary

Six classes of artifact were inspected, read-only, across the storage reachable from this host: `~/db-snapshots/**` recursively; a
host-wide scan for `*.dump`, `*.sql`, `*.sql.gz`, basebackups and `pg_dump` output; a
host-wide scan for any file over 1 MB dated 2026-08-09 to 2026-08-13; Docker containers
and their retained JSON logs; PostgreSQL WAL segments; object-storage references in
compose and config.

**Eleven candidate artifacts were found. None retains the pre-truncation relational
state required to resolve Q1.** The surviving pre-truncation outbox and audit history
is explicitly handled below and was already available to RERUN-001.

### The decisive artifact

The oldest retained artifact is `pre-07199f4-20260813T024806Z.sql`
(2026-08-13T02:48Z, plain-format `pg_dump`). Row counts read directly from its `COPY`
blocks:

| Table | Rows |
| --- | ---: |
| `tasks` | 0 |
| `task_events` | 0 |
| `model_invocations` | 0 |
| `model_invocation_results` | 0 |
| `decision_graph_nodes` | 0 |
| `decision_graph_edges` | 0 |
| `context_snapshots` | 0 |
| `context_segments` | 0 |
| `outbox_events` | 1816 |
| `audit_events` | 6 |
| `organization_roles` | 48 |

Every other retained dump is strictly later (2026-08-13T22:40Z through
2026-08-14T05:36Z), so none can recover state the earliest one already lacks.

The retained WAL belongs to a PostgreSQL container created 2026-08-14T03:23Z and covers
only the post-cutover period. No container or application log from 2026-08-10..12
survives.

**Pre-truncation history does survive, in one place only:** `outbox_events` (and 6
`audit_events` rows). That is exactly why the table above shows 1816 rows there while
every relational table Q1 depends on shows 0. The live `outbox_events` table (1942 rows)
is a **superset** of the 1816 rows in the oldest dump, and RERUN-001 already measured
against it. The dumps therefore offer no source that was not already available, and the
irrecoverability recorded here is scoped to the relational evidence Q1 requires — not to
pre-truncation history as a whole.

### Restore decision

```
restore:                    AUTHORIZED but NOT_RUN_BY_DESIGN
EXPECTED_INFORMATION_GAIN:  0
```

An isolated restore was authorized by the mandate. It was not run because the candidate
artifacts deterministically demonstrate that they do not contain the required rows —
the counts above were read from the dump file itself, so restoring it could not surface
information the file does not hold.

**This is not an omitted procedure or a missing test.** It is a decision not to spend
effort on an operation whose information gain is provably zero.

### Rule

```
IRRECOVERABLE != MECHANISM_ABSENT
```

The five `purpose='executive_ceo_plan'` markers remain OBSERVED. What is irrecoverable is
the downstream chain and the plan content. **This report does not assert that
`department_plan`, `department_review` or `executive_ceo_closure` never executed.** It
asserts that no retained artifact can establish whether they did.

### Remaining unknowns

- Plan content of the five `executive_ceo_plan` attempts.
- Whether any downstream stage executed without emitting the expected outbox events.
- The goal/task correlation chain behind the markers.

---

## 2. Q3 — canonical source body recovery

```
manifest entries checked:   63
recovered exact bodies:     4 / 63
classification:             Q3_SOURCE_BODY_UNAVAILABLE
CANONICAL_PROVENANCE_GAP:   present = true
original Q3:                UNMEASURABLE WITH CURRENT RETAINED SOURCES
REFORMULATED-Q3-001:        PROPOSED_ONLY / NOT_EXECUTED
```

### Recovery rule

```
actual_sha256 == source-manifest declared_sha256   (bytes cross-checked)
```

No near-match was accepted. A document with a different hash is a different artifact,
not the canonical body.

### Search performed

Every entry in `docs/canonical/source-manifest.yaml` was checked by exact hash against
`/home/ubuntu`, `/opt/explorarte`, `/mnt`, `/srv` and Docker volumes / accessible
retained storage.

**Four of sixty-three bodies were recovered**, all of them role and agent profile
documents that survive verbatim inside repository worktrees:

- `/mnt/data/branch0_work/source/organizacion/ingenieria_ia/AGENT.md`
- `/mnt/data/branch0_work/source/organizacion/ingenieria_ia/ingeniero_ia/PERFIL.md`
- `/mnt/data/branch0_work/source/organizacion/ingenieria_ia/semantic_engineer/PERFIL.md`
- `/mnt/data/branch0_work/source/organizacion/investigacion/research_worker_hourly/PERFIL.md`

### Bodies required by Q3 — all missing

- `CONTEXTO_ARQUITECTURA_ORG_V2.md` (14653 bytes, sha256 `5720003f98b8b6b3…`)
- `SKILL.md` (5652 bytes) and the relevant `SKILL(1..3).md` variants
- `rol-razonamiento-logico.md` (4041 bytes)
- the remaining `branch0_work` sources required by the original formulation

`source-manifest.yaml` retains only path, sha256 and byte count for these. The four
read-only measurement tools cannot open a body that is not stored.

### Rule

```
missing canonical body != absent mechanism
```

The provenance gap is recorded as a provenance gap. It is **not** converted into an
architectural simplification finding.

### REFORMULATED-Q3-001 — proposal only

> Restricted to artifacts that demonstrably exist (frozen worktree `main=4e0853d3` and
> the production read-only DB): which custom mechanisms are declared in source,
> migrations and enabled role configuration, and which of those show runtime evidence in
> the surviving `outbox_events` and `embedding_invocations`?

Explicitly excludes the unretained `/mnt/data` corpus. **Not authorized for execution**;
recorded for human review only.

---

## 3. Model gate result

```
DeepSeek calls:      0
Grok calls:          0
Luna calls:          0
Total model cost:    $0.00
```

`MODEL_CALL_GATE_NEVER_OPENED`. Neither Q1 nor Q3 recovered material new evidence, so no
model call had positive expected information value.

**Zero calls is not incomplete work.** It is the gate being enforced correctly: budget
was available and deliberately not spent, because spending it would have produced
another restatement of "nothing was found" rather than new evidence.

---

## 4. Instrument housekeeping

### Budget

```
authorized_target_budget:   $2.50
authorized_hard_cap:        $3.00
RERUN-001 configured_cap:   $5.00
RERUN-001 actual_spend:     $1.7482
classification:             MANDATE_CONFIGURATION_DRIFT
budget_violation:           false
```

Cause: the operator extrapolated an earlier "$3–5 additional" remark instead of using the
explicit mandate figure. The cap has been corrected to $3.00 (target $2.50) in both the
controller and the external budget watchdog. RERUN-001 never approached either figure, so
no budget violation occurred.

### Instrument versions

RERUN-001 was **not** executed under a single immutable instrument.

| Version | Evidence gate | Effect |
| --- | --- | --- |
| `instrument_vA` | required a top-level `provenance` key | False-positive rejection of FALSIFY-Q2: a valid 13-observation packet carrying provenance per observation was degraded as unmeasurable |
| `instrument_vB` | accepts provenance nested inside observations; structural validation only | The **same** Q2 evidence packet was re-admitted — no recollection, no new model evidence |

FALSIFY-Q2's measurement therefore rests on evidence gathered under `vA` and admitted
under `vB`.

---

## 5. CTX-TRUNCATION-001 — facts and hypotheses separated

The measurement context injected during RERUN-001 mixed observations and interpretation in
a single field. It is now split.

### OBSERVED_FACTS

1. A data truncation occurred on 2026-08-12.
2. The live `tasks` table retains 3 rows, all created 2026-08-14T03:58:18Z.
3. `outbox_events` spans 2026-08-10 to 2026-08-14 and was not truncated.
4. Pre-truncation outbox counts: 192 `task.created`, 278 `model.invocation_requested`,
   91 `model.invocation_succeeded`, 184 `model.invocation_failed`, 80 `task.finalized`.
5. Exactly 5 pre-truncation `context.snapshot_created` events carry
   `purpose='executive_ceo_plan'` (2026-08-10 05:45:31Z–06:49:08Z, actor `empresa/ceo`).

### HYPOTHESES — `UNAUDITED / NOT DISPOSED`

1. Historical zero counts in `tasks`, `model_invocations` and `decision_graph` may be
   artifacts of the truncation rather than properties of the architecture.

These hypotheses were never falsified by Grok nor disposed by Luna. **They must not be
cited as established findings.**

---

## 6. Decision consequences

- **Q1 must not be retried against the same historical evidence boundary.** The
  accessible retained artifact set within the searched measurement boundary has been
  exhaustively enumerated, and none of it holds the relational state Q1 requires. This
  makes no claim about inaccessible provider snapshots, unknown external archives, or
  artifacts outside the searched boundary.
- **Q1's UNKNOWN is historical irrecoverability, not negative evidence.** It does not
  support any claim that downstream stages were absent, dormant or vestigial.
- **The original Q3 formulation must not be retried** unless the exact canonical bodies
  become available and hash-verify against the manifest.
- **Missing canonical bodies must not be interpreted as absent mechanisms.**
- **REFORMULATED-Q3-001 is a proposal only** and is not authorized for execution.
- **No DeepSeek, Grok or Luna call was justified in BOUNDARY-REPAIR-001.**
- **V2 remains unauthorized.** `v2_design_allowed: false`, `shadow_v2_started: false`.
- **General question expansion remains unauthorized** pending human review.

---

## 7. Evidence references

Operational evidence remains outside git, in the campaign working directory:

| Path | Contents |
| --- | --- |
| `~/redesign-001/state/boundary_repair_report.txt` | this phase's primary report |
| `~/redesign-001/state/boundary_repair_001.json` | structured artifact inventory, hash search results, classifications |
| `~/redesign-001/state/rerun_report.txt` | RERUN-001 mandated FINAL OUTPUT |
| `~/redesign-001/state/campaign_run2_state.json` | run state, findings, halt history, instrument findings |
| `~/redesign-001/state/q3_body_search.json` | per-entry canonical body search result |

Each of these is bound to this report by sha256 and byte size in
[`organization-redesign-001-evidence-manifest.md`](organization-redesign-001-evidence-manifest.md),
so a reviewer can verify that an artifact has not changed since the report was written.

DB dumps, physical backups, raw model traces and generated evidence bundles are **not**
copied into git. `docs/reports/` stores the durable reviewable report, not the raw
evidence.

---

## Related

- [`organization-redesign-001-rerun-001.md`](organization-redesign-001-rerun-001.md) —
  the RERUN-001 measurement this phase takes as input.
- [`organization-redesign-001-evidence-manifest.md`](organization-redesign-001-evidence-manifest.md) —
  sha256 and byte size of every off-git artifact these reports reference.
