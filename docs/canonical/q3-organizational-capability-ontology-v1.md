# Q3_ONTOLOGY_V1 — organizational capability ontology

Status: FROZEN  
Freeze timestamp: 2026-08-15T21:37:59Z  
Base SHA: `4c847eb590e50a16fc4ee4bce82a4e5c771d1bf0`  
Canonical hash scope: the complete byte sequence of this file at freeze time  
Term: `ORGANIZATIONAL_CAPABILITY`

This ontology defines an inventory unit. It does not answer Q3, count the
repository's capabilities, or create an expected count.

## 1. Operational definition

An `ORGANIZATIONAL_CAPABILITY` is a repository-owned behavior with all of the
following properties:

1. It has one stable, nameable operational responsibility.
2. It produces, restricts, transforms, selects, authorizes, routes, persists,
   evaluates, or observes a system transition relevant to the organization.
3. At least one repository-verifiable entrypoint, trigger, enforcement point,
   transition rule, or behavioral contract invokes or enforces it.
4. Its effect or restriction is observable at an interface, returned result,
   durable state transition, emitted signal, authorization decision, rejection,
   or invariant.
5. The behavior is implemented by the repository. Merely invoking behavior
   wholly supplied by an external dependency is insufficient.
6. Its conceptual identity survives a change in persistent representation or
   file/package layout, provided its responsibility, causal purpose, and
   compatible entry/transition contract remain.
7. It is distinguishable from artifacts that only store data, configure or
   declare behavior, migrate schema, record execution evidence, support tests,
   or provide a helper without autonomous operational responsibility.

Operational tests for the terms above:

- **repository-owned behavior**: an executable path or enforceable rule whose
  branching, transition, invariant, orchestration, or policy is expressed in
  repository source. A wrapper that only forwards to an external dependency
  without adding such behavior fails this test.
- **stable responsibility**: one sentence of the form "When `<entry/trigger>`
  occurs, the system `<effect/restriction>` so that `<causal purpose>`" remains
  true across representation-only refactors.
- **verifiable**: provenance identifies repository path plus symbol, rule, or
  line locator sufficient for another reviewer to inspect the contract.
- **observable**: a caller, downstream component, persisted record, emitted
  event, audit surface, or rejected operation can distinguish the behavior from
  its absence. Tests may demonstrate this but test evidence alone cannot create
  the behavior.
- **autonomous operational responsibility**: removing the candidate changes a
  named system behavior or invariant, not merely code reuse, formatting,
  storage shape, wiring values, or test convenience.

The unit of inventory is the behavior satisfying this definition, never an
artifact solely because of its artifact type.

## 2. Decision procedure

Evaluate one candidate at a time and record every answer in order. Do not skip
questions. `YES` and `NO` require provenance; where the available candidate
packet cannot support either, use `UNKNOWN` and the terminal result prescribed
below.

### Q1 — repository-owned executable/enforceable behavior

Does repository source express executable behavior or an enforceable rule,
rather than only delegate unchanged external behavior?

- `NO` -> `NOT_A_CAPABILITY`; stop.
- `UNKNOWN` -> `UNRESOLVED`; stop.
- `YES` -> Q2.

### Q2 — stable operational responsibility

Can the responsibility be stated as one stable behavioral sentence with a
named effect/restriction and causal purpose?

- `NO` -> `NOT_A_CAPABILITY`; stop.
- `UNKNOWN` -> `UNRESOLVED`; stop.
- `YES` -> Q3.

### Q3 — verifiable entry or enforcement

Is at least one entrypoint, trigger, rule, enforcement point, transition, or
behavioral contract located in the supplied repository evidence?

- `NO` -> `NOT_A_CAPABILITY`; stop.
- `UNKNOWN` -> `UNRESOLVED`; stop.
- `YES` -> Q4.

### Q4 — specifiable effect or restriction

Can the reviewer state the externally distinguishable change or restriction,
including the relevant input/precondition and result, rejection, state change,
or emitted observation?

- `NO` or `UNKNOWN` -> `UNRESOLVED`; stop.
- `YES` -> Q5.

### Q5 — supporting artifact test

Is this candidate only representation, declaration, configuration, runtime
evidence, test material, or infrastructure supporting an already identified
capability in the candidate packet?

- `YES` -> `SUPPORTING_ARTIFACT_OF:<capability_id>`; stop.
- `UNKNOWN` -> `UNRESOLVED`; stop.
- `NO` -> Q6.

The target ID is permitted only when the packet contains provenance for the
supported capability. Otherwise Q5 is `UNKNOWN`.

### Q6 — layout-dependent identity test

Would the proposed identity cease to exist, merge, or split solely because
files or packages were regrouped, with no change to responsibility, causal
purpose, or entry/transition contract?

- `YES` or `UNKNOWN` -> `UNRESOLVED`; stop.
- `NO` -> `ORGANIZATIONAL_CAPABILITY`.

### Terminal labels and boundary mapping

Exact classification is one of:

- `ORGANIZATIONAL_CAPABILITY`
- `NOT_A_CAPABILITY`
- `SUPPORTING_ARTIFACT_OF:<capability_id>`
- `UNRESOLVED`

For capability-boundary agreement only,
`ORGANIZATIONAL_CAPABILITY = CAPABILITY`; all other terminal labels equal
`NOT_CAPABILITY_OR_UNRESOLVED`. Artifact roles are measured separately and do
not change the terminal label.

A positive classification is invalid unless its complete required capability
record is supplied. In particular, an unverifiable `behavioral_contract`
forces `UNRESOLVED`, regardless of reviewer intuition.

## 3. Artifact-role taxonomy

Artifact roles describe evidence, not inventory units. One artifact may have
multiple roles when each is justified.

- `BEHAVIOR_IMPLEMENTATION`: repository source that implements branching,
  transformation, enforcement, orchestration, invariant, or transition logic.
- `DECLARATION`: schema, type, interface, manifest, constant, or document that
  declares a vocabulary, contract surface, or allowed shape without by itself
  executing the behavior.
- `CONFIGURATION`: values or mappings consumed by behavior to choose policy,
  wiring, limits, routing, roles, or operating parameters.
- `PERSISTENCE_REPRESENTATION`: tables, columns, indexes, constraints,
  migrations, serialization shapes, or store mappings representing durable
  state. A database trigger can additionally be `BEHAVIOR_IMPLEMENTATION` only
  when its enforceable transition is itself under evaluation.
- `RUNTIME_EVIDENCE`: surviving records, event types, audit entries, logs,
  metrics, or other observations showing that behavior may have executed.
- `TEST_ONLY`: fixtures, fakes, test cases, test-only runners, or assertions not
  reachable as production behavior.
- `SUPPORTING_INFRASTRUCTURE`: generic transport, storage, scheduling, hashing,
  adapters, or utilities supporting another behavior without owning its causal
  purpose.
- `EXTERNAL_DEPENDENCY`: behavior supplied outside this repository. A local
  adapter may also have another role if it adds repository-owned rules.
- `UNRESOLVED`: the supplied evidence cannot justify a more specific role.

Forbidden identity shortcuts:

`table != capability`, `migration != capability`, `role != capability`,
`event_type != capability`, and `package != capability` by artifact identity
alone. The same applies to every artifact named in the exclusion rules.

## 4. Granularity and identity rules

### Candidate formation

A candidate must name a proposed behavior and cite at least one entry, trigger,
enforcement, or transition locator. An artifact-only candidate is allowed so
that exclusions can be tested, but it is not rewritten into a behavior during
classification.

### Merge rule

Two implementations are one capability only when all three are demonstrated:

1. the same behavioral responsibility;
2. the same causal purpose; and
3. compatible entry/transition contracts, meaning their preconditions,
   principal effects/restrictions, and success/failure semantics do not express
   independently operable responsibilities.

Multiple packages may therefore implement one capability.

### Split rule

Split candidates when repository evidence demonstrates an independently
invocable or enforceable operational responsibility with a distinct causal
purpose or incompatible contract. Separate files, functions, tables, event
types, endpoints, or packages are not sufficient reasons to split. One package
may therefore contain multiple capabilities.

### Representation stability test

Ask whether changing table names, schemas, serialization, file paths, package
boundaries, or deployment wiring while preserving behavior would change the
candidate identity. If yes, the proposed identity is representation-bound and
Q6 cannot pass.

### Adjacent-boundary requirement

Every positive record names its nearest plausible adjacent capability and
states the behavioral distinction. If the distinction cannot be evidenced,
classify `UNRESOLVED` rather than merge or split by intuition.

Capability IDs use `OC-<normalized-responsibility>` and are stable identifiers,
not sequence numbers. They must not encode package names or an expected count.

## 5. Required positive capability record

Every positive decision must materialize all fields below. Arrays may be empty
only for declaration/configuration/runtime-evidence provenance when the
reviewer explicitly states that the evidence class was not present in the
candidate packet. All implementation and boundary fields are mandatory.

```yaml
capability_id: OC-<normalized-responsibility>
name: <human-readable responsibility>
behavioral_contract: >-
  When <entry/trigger and preconditions>, the system <observable effect or
  restriction and failure behavior>.
causal_purpose: <why the organization needs this transition/restriction>
entrypoints_or_triggers:
  - <path:symbol-or-line>
enforcement_or_transition_points:
  - <path:symbol-or-line>
reads_or_inputs:
  - <input/precondition>
writes_or_effects:
  - <returned result, rejection, durable transition, or emitted observation>
implementation_provenance:
  - <path:symbol-or-line>
declaration_provenance: []
configuration_provenance: []
possible_runtime_evidence: []
boundary_with_adjacent_capabilities: >-
  <nearest adjacent responsibility and the evidenced merge/split boundary>
classification_basis: >-
  <Q1-Q6 answers with concise evidence>
```

The `behavioral_contract` must name trigger/preconditions and observable
success or restriction/failure semantics. A label, noun phrase, package
description, or restatement of a type name is not a behavioral contract.

## 6. Explicit exclusion rules

None of the following counts by itself: table, index, constraint, migration,
role, event type, struct, interface, package, endpoint, helper function,
fixture, constant, or configuration row.

Apply these mechanical rules:

1. If the candidate packet contains only one of those artifacts and no
   repository-owned executable/enforceable behavior, Q1=`NO` and
   `NOT_A_CAPABILITY`.
2. If the artifact is only evidence/configuration/representation for a
   capability identified in the same packet, Q5=`YES` and
   `SUPPORTING_ARTIFACT_OF:<capability_id>`.
3. An endpoint is only an entry locator. Its routed behavior must still pass
   Q1-Q6.
4. A database constraint or trigger may pass Q1 only when evaluated as an
   enforceable repository-authored rule with a specifiable transition
   restriction; the table, migration file, or constraint name remains merely
   provenance.
5. A helper passes only if it owns an independently nameable operational
   responsibility and causal purpose. Reuse or technical transformation alone
   is insufficient.
6. A declaration plus runtime evidence does not prove implementation. Missing
   implementation provenance yields `UNRESOLVED`, not a positive decision.
7. Absence from a collector's output is never proof of irrelevance.

## Appendix A — ontology derivation set (closed before freeze)

`ONTOLOGY_DERIVATION_SET` contains exactly eight candidates. It was used only
to expose ambiguity before freeze and is excluded from every V1 holdout.

| ID | Candidate and fixed provenance | Result under V1 | Artifact role(s) | Ambiguity tested |
|---|---|---|---|---|
| D01 | Budget reservation rule, `internal/agentbudget/types.go:65` (`Reserve`) | `ORGANIZATIONAL_CAPABILITY`, `OC-BUDGET-USAGE-ENFORCEMENT` | `BEHAVIOR_IMPLEMENTATION`, `DECLARATION` | A package may contain behavior, but the function's atomic multi-limit restriction—not the package—is the unit. |
| D02 | Message topology rule, `internal/agentmessaging/topology.go:32` (`ValidateEdge`) | `ORGANIZATIONAL_CAPABILITY`, `OC-MESSAGE-TOPOLOGY-ENFORCEMENT` | `BEHAVIOR_IMPLEMENTATION` | A role is input/configuration; the enforced sender-recipient restriction is the behavior. |
| D03 | Context snapshot build, `internal/contextengine/service.go:71` (`Build`) | `ORGANIZATIONAL_CAPABILITY`, `OC-CONTEXT-SNAPSHOT-BUILD` | `BEHAVIOR_IMPLEMENTATION` | Multiple sources and stores participate in one entry contract; package identity is not used. |
| D04 | `migrations/000006_create_context_engine.up.sql:1` | `SUPPORTING_ARTIFACT_OF:OC-CONTEXT-SNAPSHOT-BUILD` | `DECLARATION`, `PERSISTENCE_REPRESENTATION` | Schema and migration do not become a capability. |
| D05 | `ingenieria_ia/orquestador/PERFIL.md:1` | `SUPPORTING_ARTIFACT_OF:OC-CONTEXT-SNAPSHOT-BUILD` | `DECLARATION`, `CONFIGURATION` | A role/profile configures behavior but is not the behavior. |
| D06 | `context.snapshot_created` at `internal/contextengine/postgres/store.go:80` | `SUPPORTING_ARTIFACT_OF:OC-CONTEXT-SNAPSHOT-BUILD` | `RUNTIME_EVIDENCE`, `DECLARATION` | An event type is possible execution evidence, never automatically the unit. |
| D07 | Fixture activation, `internal/agentmessagingfixtures/activate.go:13` | `NOT_A_CAPABILITY` | `TEST_ONLY` | Test reachability cannot create production organizational behavior. |
| D08 | SHA-256 helper, `internal/contextengine/hashing.go:14` (`digest`) | `SUPPORTING_ARTIFACT_OF:OC-CONTEXT-SNAPSHOT-BUILD` | `SUPPORTING_INFRASTRUCTURE` | A technical helper lacks autonomous operational responsibility. |

Derivation-set positive records were required to satisfy the schema during
rule derivation. They are examples, not an inventory and not an expected count.
No Q3-PRE-DEF-001 grouping, historical count, package count, migration count,
or event-type count was consulted to add, merge, split, or tune these rules.

## Appendix B — derivation positive-record checks

### OC-BUDGET-USAGE-ENFORCEMENT

- Behavioral contract: when `Reserve` receives current usage, a non-negative
  delta, and positive limits, it either returns the all-dimensions next usage or
  rejects the operation without a partial result if a dimension is invalid,
  overflows, or exceeds its ceiling.
- Causal purpose: prevent an agent execution tree from consuming beyond its
  authorized multi-dimensional budget.
- Entry/enforcement: `internal/agentbudget/types.go:65` and its ordered reject
  branches.
- Inputs/effects: `Usage` plus `Limits`; next `Usage` or typed rejection.
- Implementation provenance: `internal/agentbudget/types.go:65`.
- Declaration/configuration provenance: `internal/agentbudget/types.go:11-25`.
- Possible runtime evidence: budget event/usage records, if separately proven.
- Adjacent boundary: durable budget creation/inheritance/consumption may be
  adjacent capabilities; this record covers only the pure atomic reservation
  rule and does not merge them by package.
- Basis: Q1=YES, Q2=YES, Q3=YES, Q4=YES, Q5=NO, Q6=NO.

### OC-MESSAGE-TOPOLOGY-ENFORCEMENT

- Behavioral contract: when a sender requests a recipient edge, `ValidateEdge`
  resolves current organization roles and either permits only the canonical
  CEO/leader/worker topology or returns a topology violation for disabled,
  foreign, self, peer, or otherwise forbidden edges.
- Causal purpose: prevent organizational messages from bypassing the canonical
  authority/reporting topology.
- Entry/enforcement: `internal/agentmessaging/topology.go:32`.
- Inputs/effects: organization registry state and role IDs; permit (`nil`) or
  rejection.
- Implementation provenance: `internal/agentmessaging/topology.go:32`.
- Declaration provenance: `internal/agentmessaging/topology.go:15-18`.
- Configuration provenance: registry role/unit/leader data read by the method.
- Possible runtime evidence: accepted/rejected message-send outcomes, if proven.
- Adjacent boundary: durable message enqueue/delivery is distinct; this record
  owns permission of an edge, not transport or persistence.
- Basis: Q1=YES, Q2=YES, Q3=YES, Q4=YES, Q5=NO, Q6=NO.

### OC-CONTEXT-SNAPSHOT-BUILD

- Behavioral contract: when `Build` receives a valid request for the active
  organization revision and actor, it resolves and validates permitted sources,
  applies bounded assembly/precedence, renders and persists an integrity-bound
  snapshot, reuses an idempotent match, or rejects invalid/forbidden input.
- Causal purpose: create reproducible, authority-ordered execution context for
  an organizational actor without allowing lower-trust inputs to redefine
  authority.
- Entry/enforcement: `internal/contextengine/service.go:71` plus resolution,
  revalidation, bounded assembly, idempotency, and integrity checks invoked
  there.
- Inputs/effects: build request, registry/policy/documents/memory/skills/project/
  task/RAG sources; ready persisted snapshot or rejection.
- Implementation provenance: `internal/contextengine/service.go:71`.
- Declaration provenance: `internal/contextengine/domain.go` and interfaces
  invoked by `Build`.
- Configuration provenance: `ServiceConfig` and canonical provider inputs.
- Possible runtime evidence: `context.snapshot_created`, validation/rejection,
  and invalidation events, subject to future evidence accounting.
- Adjacent boundary: source-provider retrieval and snapshot invalidation can be
  independently operable; this record owns the Build entry contract and does
  not inventory every participating provider as a capability.
- Basis: Q1=YES, Q2=YES, Q3=YES, Q4=YES, Q5=NO, Q6=NO.

