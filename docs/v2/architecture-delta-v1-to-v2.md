# V1 → V2 minimal architecture delta

Status: **authorized design; implementation started**  
Scientific input: `REFORMULATED-Q3-002`, commit `c81dc9958f279b7f94c64b49ae32b8f29cc6531c`  
Scientific base: `588db11599d701fb1e2ecbae19aa00828663dc2b`  
Ontology: `Q3_ONTOLOGY_V1` (`cbf4b72975b4a8d8c2650969181f470d7a91fbf5f35beeabd23eb66a03f6e11c`)  
Human disposition: `ACCEPTED_PARTIAL`; `V2_DESIGN_ALLOWED=true`

This is an architectural decision over the derived V1 inventory. The number
of capabilities is not a target. Seventeen unresolved source units, 67 empty
runtime tables, and the six challenged boundaries remain measurement
limitations; they are not converted into evidence of absence.

## Minimal V2 shape

V2 remains a modular Go kernel with PostgreSQL durability and an independent
clinical-cell boundary. It replaces the V1 accumulation of individually
bootstrapped features with explicit composition around these responsibilities:

1. **Canonical control** — organization registry and capability authorization.
2. **Workflow runtime** — task state, obligations, decision branches,
   cross-role coordination, and owner-goal decomposition behind one execution
   contract.
3. **Model execution boundary** — assignment, principal identity, egress
   policy, invocation, and provider dispatch in one pre-call pipeline.
4. **Resource accounting** — hierarchical task budgets, deterministic pricing,
   and provider-wallet settlement, retaining distinct ledgers but one gate.
5. **Context assembly** — one durable canonical snapshot plus optional task
   projection; no second source-of-truth context.
6. **Governed knowledge** — one publication lifecycle for memory and approved
   RAG knowledge, with namespace-specific read projections.
7. **Evidence ingestion** — corpus and web acquisition through typed adapters;
   object storage is supporting infrastructure, not an autonomous capability.
8. **Content policy** — one deterministic credential detector inventory; each
   organizational boundary owns reject, skip-provider, or redact disposition.
   Clinical-data classification remains outside this heuristic kernel.
9. **Change assurance** — evaluation, shadow comparison, staging, and promotion
   form one evidence-to-promotion chain.
10. **Skill lifecycle** — retained separately because executable skill adoption
    has a different causal boundary from knowledge publication.

`QUESTION_IDENTITY_GATE` remains versioned and maintained, but outside the
production kernel: it protects scientific/campaign refinements, not ordinary
organizational work execution.

## Complete V1 disposition

| V1 capability | Decision | V2 destination and reason |
| --- | --- | --- |
| `OC-agent-budgeting` | SIMPLIFY | Resource accounting; preserve hierarchical limits but expose one pre-spend gate with cost settlement. |
| `OC-agent-messaging` | MERGE | Workflow runtime; cross-role messages become durable coordination events, while same-role steps remain task transitions. |
| `OC-capability-authorization` | KEEP | Canonical control; default-deny request/decide/consume remains an independent security boundary. |
| `OC-completion-verification` | MERGE | Workflow runtime; obligations gate the task terminal transition instead of forming a parallel lifecycle. |
| `OC-context-compilation` | SIMPLIFY | Context assembly; retain durable snapshot identity and drift checks, absorb optional `contextcompiler` projection behind the same contract. |
| `OC-corpus-census` | MERGE | Evidence ingestion; repository-owned orchestration/dedup/resume rules remain, while Poppler stays an external typed adapter. |
| `OC-cost-ledger` | KEEP | Resource accounting; real-money reserve/commit/release remains distinct from task quotas. |
| `OC-decision-graph-execution` | MERGE | Workflow runtime; DAG branches are an execution strategy under one workflow identity. |
| `OC-evaluation-comparison` | MERGE | Change assurance; comparison has operational effect only through the promotion gate. Metrics remain replaceable adapters. |
| `OC-evidence-object-storage` | REPLACE | A signed immutable-storage port under evidence ingestion. It is supporting infrastructure, not a V2 capability boundary. |
| `OC-executive-orchestration` | REPLACE | Workflow runtime coordinator expressed in behavioral stages, not CEO/leader task subtype names. |
| `OC-improvement-promotion` | MERGE | Change assurance; consumes evaluation/shadow evidence and controls staged activation. |
| `OC-memory-lifecycle` | MERGE | Governed knowledge; share propose/review/publish/deprecate semantics with approved RAG knowledge. |
| `OC-model-dispatch-assignment` | MERGE | Model execution boundary; assignment is a required pre-call transition. |
| `OC-model-egress-policy` | KEEP | Model execution boundary; explicit deny/allow policy remains independently inspectable. |
| `OC-model-execution-identity` | KEEP | Model execution boundary; cryptographic principal binding remains independently enforceable. |
| `OC-model-invocation-dispatch` | KEEP | Model execution boundary's central auditable invocation contract. |
| `OC-model-pricing-resolution` | MERGE | Resource accounting; pricing is deterministic input to reservation/settlement, not a separate workflow. |
| `OC-organization-registry` | KEEP | Canonical control and revision identity for all other boundaries. |
| `OC-question-identity-gate` | KEEP | Campaign instrument only; identity is its typed semantic gate, not its CLI layout. |
| `OC-rag-knowledge-lifecycle` | MERGE | Governed knowledge with namespace-scoped retrieval projection. |
| `OC-secret-clinical-detection` | DELETE | Clinical vocabulary does not prove clinical data. Explicit upstream `DataClinical` sentinels remain enforceable at organization/cell boundaries. |
| `OC-secret-scanning` | SIMPLIFY | Content policy; one precise credential detector supports reject, skip-provider, and redact dispositions. |
| `OC-shadow-verification` | SIMPLIFY | Change assurance; independent parity result remains non-authoritative until promotion. |
| `OC-skill-lifecycle` | KEEP | Separate executable-artifact governance boundary. |
| `OC-staging-workspace` | MERGE | Change assurance; isolated workspace/seal is the artifact phase before promotion. |
| `OC-task-lifecycle-management` | KEEP | Workflow runtime's durable source of truth. |
| `OC-web-evidence-ingest` | MERGE | Evidence ingestion; acquisition type differs, admission/sanitize/rank contract is shared. |

## Challenged-boundary handling

- Context compilation remains a responsibility; its projection neighbor is
  absorbed only behind the snapshot contract, so folder layout cannot create a
  second capability.
- Corpus census is retained as repository-owned orchestration, not as Poppler
  behavior. Poppler is an external adapter.
- Evaluation comparison is not treated as an independent production goal; its
  observable effect is the change-assurance decision.
- Object storage is deliberately demoted to supporting infrastructure.
- Executive orchestration is preserved by causal purpose and replaced at the
  representation boundary; CEO/leader task subtype names are not V2 identity.
- Question identity is isolated from the kernel and anchored in the typed gate,
  not the current CLI.

These decisions use the challenged cases only where the V2 boundary requires a
choice. They do not rewrite the accepted Q3-002 measurement.

## Implementation order

1. Unify content detection and prove surface-specific dispositions.
2. Establish a single workflow composition root; fold completion, decision
   branches, and cross-role coordination into the task execution contract.
3. Collapse model assignment/identity/egress/invocation into a single pre-call
   pipeline with zero provider calls on any failed gate.
4. Present task budget, price resolution, and wallet settlement as one
   resource-accounting transaction boundary.
5. Consolidate governed knowledge and evidence ingestion behind typed ports.
6. Join evaluation, shadow evidence, staging, and promotion; then remove
   superseded bootstraps and persistence representations only after consumers
   migrate and compatibility tests pass.
7. Prune the final daemon composition to V2 components and record any retained
   CLI-only operator surfaces explicitly.

Any discovered P0 safety defect interrupts this order. Otherwise, unresolved
measurement units do not block implementation unless a concrete change touches
their boundary.

## First V2 slice

Branch: `v2/content-policy-kernel-001`

This slice replaces `internal/dataclassifier` and `internal/secretscan` with
`internal/contentpolicy` and migrates all production consumers. Detection emits
audit-safe credential findings only. Task ingress rejects credential findings;
memory enforces credential declarations while retaining explicit upstream
`DataClinical` rejection; RAG rejects credentials but permits ordinary
healthcare/research vocabulary; and redaction removes credentials without
changing surrounding content.

Acceptance invariants:

- one detector inventory and no remaining imports of the two V1 packages;
- ordinary healthcare vocabulary produces no inferred data classification;
- findings never retain or render matched credential values;
- placeholders remain accepted;
- task, memory, RAG, and CLI boundary behavior stays covered by tests;
- no database, migration, deployment, provider, or production change.
