# G3-003 — Context-assembler trace (the gap `what-not-to-change.md` item 7 was waiting on)

`reports/context-efficiency.md` measured a ~78K-88K input-token floor on
every invocation across a real campaign (18/18 calls, four different roles,
essentially uniform regardless of task complexity) and left the cause
`UNKNOWN_OR_GAP`: "the exact assembler code path that selects this canonical
scope was not traced to a single call site within this audit's model-call
budget." `what-not-to-change.md` item 7 makes this floor a **provisional
hold** specifically pending that trace, because Grok's adversarial review
raised the possibility it is load-bearing grounding context, not waste.

**This document performs the trace. It does not shrink anything, and it
does not resolve whether the floor is load-bearing for any specific
purpose — see Conclusion.**

## What the trace found

`internal/contextengine/assembler.go`'s `DeterministicAssembler.Assemble` is
not where scope is decided — it only orders, trims, and enforces byte/count
limits on a `[]SourceRecord` slice it is *given*. The real scope decision is
one layer up, in `internal/contextcompiler`.

`internal/contextcompiler/contextcompiler_profiles.go` defines exactly
**one** narrowed `ContextProfile`: `ResearchCorpusCurateV1`, scoped to the
single task class `research.corpus_curate` and the single role
`investigacion/research_worker_hourly`. Its own doc comment is explicit
about what this means for everything else:

> "executive.ceo, department.leader, code-runner, QA, visual agents are all
> explicitly untouched -- Compile falls back to the canonical snapshot
> unchanged for any selector this doesn't match."

`SelectorRegistry.Select` (`contextcompiler_selector.go`) confirms the
mechanism: a selector that matches no registered profile entry falls back to
`Compile`'s canonical-snapshot path — the **full, unminimized** canonical
context — not a smaller default.

**This is the answer to why the floor is "remarkably uniform... regardless
of role":** it is not uniform because every role coincidentally needs the
same ~80-125KB. It is uniform because none of CEO, department-leader,
department-worker, department-review, code-runner, or any other purpose the
audit's campaign exercised has ever had a `ContextProfile` defined for it —
every one of them takes the identical unminimized fallback path
`ResearchCorpusCurateV1` was built specifically to avoid for its own one
narrow case.

## Conclusion — this closes the TRACE gap, not the FLOOR question

Grok's concern (is the floor load-bearing grounding context, or accidental
duplication?) is **not answered by this trace**, and this document does not
claim it is. What this trace establishes is *why no one knows yet*: nothing
has ever been built to distinguish "needed" from "unneeded" tiers for any
purpose except `research.corpus_curate` — the floor persists everywhere
else purely because minimization was never attempted there, not because it
was attempted and found necessary.

The mechanism to answer it already exists and is proven (`ResearchCorpusCurateV1`
itself, R10_DESIGN_AUDIT.md). The real next step is the same shape of work
that produced it: for each purpose that currently falls back to the full
canonical snapshot (starting with the ones the audit's own campaign
exercised — CEO plan, department plan/review/worker), determine which
`AuthorityTier`s and projections that purpose's actual output schema and
evidence requirements genuinely need, and register a new `ContextProfile`
following `ResearchCorpusCurateV1`'s exact pattern. That determination is a
domain judgment call about what each purpose's grounding requirements
actually are — not something this document decides, and not something to
guess at without the same section-by-section evidence
`R10_DESIGN_AUDIT.md` used for the one profile that already exists.

## What would need to be true before any profile is added for a new purpose

1. The specific tiers/projections that purpose's real output schema and
   evidence-requirement contract depend on are enumerated with evidence,
   the way `R10_DESIGN_AUDIT.md` sections B/D did for
   `research.corpus_curate` — not assumed from this trace alone.
2. A real before/after token-count comparison on a real campaign, the same
   empirical bar `reports/context-efficiency.md` itself used.
3. `what-not-to-change.md` item 7's hold is lifted only for the specific
   purpose being minimized, not as a blanket unlock -- each purpose is its
   own decision.
