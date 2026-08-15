# Q3-OPERATIONAL-DEFINITION-001

Mission: `ORGANIZATION-REDESIGN-001`  
Phase: `Q3-OPERATIONAL-DEFINITION-001`  
Base SHA: `4c847eb590e50a16fc4ee4bce82a4e5c771d1bf0`  
Ontology version: `Q3_ONTOLOGY_V1`  
Term: `ORGANIZATIONAL_CAPABILITY`  
Ontology SHA-256: `0da2f727ceff769469c0ffad32c165690b3f4c1e5deac31d6e099117f9f1f974`

This phase defines and validates an inventory ontology. It does not answer Q3,
does not count the repository's capabilities, and did not run
`REFORMULATED-Q3-002`, question expansion, V2, a model, or a database write.

## 1. Preflight and isolation

The canonical checkout at `/opt/explorarte/organization` was inspected only to
locate the requested commit. It was at a different SHA and had untracked files,
so it was not modified. A detached worktree was created directly from the
canonical object at:

`/home/ubuntu/q3-operational-definition-001`

The binding preflight inside that isolated worktree was:

```text
$ git rev-parse HEAD
4c847eb590e50a16fc4ee4bce82a4e5c771d1bf0
$ git status --porcelain
<empty>
```

All later repository writes are uncommitted documentation in that worktree.
No service, deployment, production configuration, or database was mutated.

## 2. Historical reason for this phase

`REFORMULATED-Q3-001` ended with `Q3_UNMEASURABLE_AS_SPECIFIED` plus
`MEASUREMENT_TARGET_DRIFT`. Its package audit was valid, but the measured
proposition was not the requested question. Refinements had replaced an
undefined behavioral unit with the easier literal proposition "does an exact-N
mechanism phrase exist?".

The historical values 45 and 5 are annulled. This phase had no expected count.
It optimizes reproducibility, causal clarity, capability/evidence separation,
and representation-stable identity only.

`Q3-PRE-DEF-001` remains `PRE_DEFINITION_OBSERVATIONAL_EVIDENCE`: its eight
provisional groupings and its accounting of 20 surviving outbox event types
were not read or used to derive, tune, select, merge, split, or validate V1.

## 3. Operational terminology and frozen ontology

The ambiguous term `custom mechanism` is replaced by
`ORGANIZATIONAL_CAPABILITY`.

In compact form, it is repository-owned behavior with one stable operational
responsibility, a verifiable entry/trigger/enforcement/transition contract, an
observable system effect or restriction, and an identity that survives changes
to persistence and package layout. Configuration, declarations, storage,
migrations, events, tests, and helpers are evidence roles and never become the
unit by artifact identity alone.

The complete normative definition, ordered Q1-Q6 procedure, artifact-role
taxonomy, merge/split rules, required positive-record schema, and explicit
exclusions are frozen in
`docs/canonical/q3-organizational-capability-ontology-v1.md`.

Freeze chronology:

1. Preflight established the clean detached base.
2. Only the closed eight-item derivation set was inspected.
3. The complete ontology file was written and frozen at
   `2026-08-15T21:37:59Z`.
4. SHA-256 over its complete bytes produced
   `0da2f727ceff769469c0ffad32c165690b3f4c1e5deac31d6e099117f9f1f974`.
5. Only after that hash was recorded was the repository holdout generated.
6. Re-hashing after both reviews produced the identical hash.

No ontology byte changed during holdout generation, review, or agreement
measurement.

## 4. Decision procedure summary

Every candidate follows the frozen order:

1. Q1: repository-owned executable/enforceable behavior? `NO` ->
   `NOT_A_CAPABILITY`; unknown -> `UNRESOLVED`.
2. Q2: one stable operational responsibility? `NO` -> `NOT_A_CAPABILITY`;
   unknown -> `UNRESOLVED`.
3. Q3: verifiable entry, trigger, rule, enforcement, or transition? `NO` ->
   `NOT_A_CAPABILITY`; unknown -> `UNRESOLVED`.
4. Q4: specifiable observable change/restriction? `NO/UNKNOWN` -> `UNRESOLVED`.
5. Q5: only support/representation/configuration/evidence for an identified
   capability? `YES` -> `SUPPORTING_ARTIFACT_OF:<capability_id>`; unknown ->
   `UNRESOLVED`.
6. Q6: identity depends only on file/package grouping? `YES/UNKNOWN` ->
   `UNRESOLVED`; `NO` -> `ORGANIZATIONAL_CAPABILITY`.

Positive classification additionally requires the full capability record and a
verifiable behavioral contract. Artifact role and candidate classification are
orthogonal fields.

## 5. Artifact-role taxonomy and granularity

Frozen roles are:

- `BEHAVIOR_IMPLEMENTATION`
- `DECLARATION`
- `CONFIGURATION`
- `PERSISTENCE_REPRESENTATION`
- `RUNTIME_EVIDENCE`
- `TEST_ONLY`
- `SUPPORTING_INFRASTRUCTURE`
- `EXTERNAL_DEPENDENCY`
- `UNRESOLVED`

Multiple roles may be justified for one artifact. A table, index, constraint,
migration, role, event type, struct, interface, package, endpoint, helper,
fixture, constant, or configuration row is never positive by identity alone.

Implementations merge only when behavioral responsibility, causal purpose, and
entry/transition contract are all shared. They split only on an evidenced,
independently operable responsibility—not on folders, files, tables, endpoints,
or event types. Thus one package can hold multiple capabilities and multiple
packages can implement one capability.

## 6. ONTOLOGY_DERIVATION_SET

The derivation set was closed before freeze and contained exactly eight items:

| ID | Fixed candidate | Derivation result |
|---|---|---|
| D01 | `internal/agentbudget/types.go:65` — `Reserve` | `ORGANIZATIONAL_CAPABILITY` (`OC-BUDGET-USAGE-ENFORCEMENT`) |
| D02 | `internal/agentmessaging/topology.go:32` — `ValidateEdge` | `ORGANIZATIONAL_CAPABILITY` (`OC-MESSAGE-TOPOLOGY-ENFORCEMENT`) |
| D03 | `internal/contextengine/service.go:71` — `Build` | `ORGANIZATIONAL_CAPABILITY` (`OC-CONTEXT-SNAPSHOT-BUILD`) |
| D04 | `migrations/000006_create_context_engine.up.sql:1` | `SUPPORTING_ARTIFACT_OF:OC-CONTEXT-SNAPSHOT-BUILD` |
| D05 | `ingenieria_ia/orquestador/PERFIL.md:1` | `SUPPORTING_ARTIFACT_OF:OC-CONTEXT-SNAPSHOT-BUILD` |
| D06 | `context.snapshot_created`, `internal/contextengine/postgres/store.go:80` | `SUPPORTING_ARTIFACT_OF:OC-CONTEXT-SNAPSHOT-BUILD` |
| D07 | `internal/agentmessagingfixtures/activate.go:13` — `Activate` | `NOT_A_CAPABILITY` |
| D08 | `internal/contextengine/hashing.go:14` — `digest` | `SUPPORTING_ARTIFACT_OF:OC-CONTEXT-SNAPSHOT-BUILD` |

These examples tested package granularity, role/configuration, migration and
storage representation, event evidence, test-only behavior, and helpers. They
are examples, not an inventory or an expected answer. Their complete records
and tested ambiguities are preserved in the frozen ontology.

## 7. Holdout generation

Generation occurred only after the V1 hash was frozen.

Parameters:

```text
BASE_SHA=4c847eb590e50a16fc4ee4bce82a4e5c771d1bf0
SEED=Q3-OPERATIONAL-DEFINITION-001|Q3_ONTOLOGY_V1|4c847eb590e50a16fc4ee4bce82a4e5c771d1bf0|HOLDOUT-01
PER_STRATUM=3
```

Algorithm:

1. Enumerate tracked paths with `git ls-files -z` at the base SHA.
2. Exclude the complete derivation package prefixes
   `internal/agentbudget/`, `internal/agentmessaging/`, and
   `internal/contextengine/`, plus the exact D04, D05, and D07 paths.
3. Assign each eligible path once, by fixed predicates and precedence, to one
   of eight strata: internal package production Go, runtime-facing command Go,
   configuration/profile, migration SQL, test-only/fixture, persistence Go,
   event-producing Go, or supporting infrastructure.
4. Score every path as
   `SHA256(seed + NUL + stratum + NUL + path)`.
5. Sort each stratum by `(score,path)` and take the first three.
6. For production Go candidates, mechanically select the first function; for
   event-producing code select the nearest preceding function to the first
   event pattern. Other strata remain whole-artifact candidates.
7. Seal each packet with the complete numbered file content and content
   SHA-256. No manual substitution was allowed.

The mutually exclusive stratum predicates were applied in this exact order:

```text
1 migrations:
  path starts "migrations/" AND suffix ".sql"
2 test_only_artifacts:
  suffix "_test.go" OR path matches (^|/)internal/[^/]*fixtures/
3 event_producing_code:
  production .go AND content matches case-insensitive
  outbox_events|append[A-Za-z]*event|record[A-Za-z]*event|publish\(|emit\(
4 persistence_artifacts:
  production .go AND (path contains "/postgres/" OR basename is
  store.go|repository.go|persistence.go)
5 runtime_facing_code:
  path starts "cmd/" AND production .go
6 configuration:
  path starts "config/" OR basename is PERFIL.md|AGENT.md OR suffix is
  .yaml|.yml|.json|.toml outside .github/ and deployments/
7 supporting_infrastructure:
  path starts .github/|deployments/|scripts/ OR basename is
  Dockerfile|Makefile|docker-compose.yml|docker-compose.yaml
8 internal_packages:
  path starts "internal/" AND production .go
```

`production .go` means suffix `.go` and not `_test.go`. Paths failing every
predicate were ineligible. The function locator regex was
`^func\s+(?:\([^)]*\)\s*)?([A-Za-z_][A-Za-z0-9_]*)\s*\(` in multiline mode.

Eligible universe sizes were 324 internal, 36 runtime-facing, 79 configuration,
95 migration, 234 test-only, 53 persistence, 20 event-producing, and 47
supporting-infrastructure artifacts. Exactly 24 candidates were selected.

### Exact holdout

| ID | Stratum | Path | Locator | Content SHA-256 |
|---|---|---|---|---|
| H01 | internal_packages | `internal/modelruntime/adapter/mimo/adapter.go` | `:110:cacheTokens` | `d3d04455c7df1e8a8517480d4351d8c77052cc24b1539b035fc462c291359fbb` |
| H02 | internal_packages | `internal/executive/schemas.go` | `WHOLE_ARTIFACT` | `394c5feec5722ba2d10140eb25f2890e1c314aecd49ac7674d0a34451f3cefc7` |
| H03 | internal_packages | `internal/decisiongraph/doc.go` | `WHOLE_ARTIFACT` | `6c0fd158650d49daf253d0dcf79237d82783a9ae9095d00865b275f20adfea21` |
| H04 | runtime_facing_code | `cmd/orgctl/budget.go` | `:19:runBudget` | `afb50bb232d45392bd0c6ff3757a94fb4cd570bb19ba50b921d31853026c1a68` |
| H05 | runtime_facing_code | `cmd/orgctl/model_egress.go` | `:17:openModelEgressRuntime` | `4f68380a8af94ede4d51f7ba9534d652214c268982fd04884c55bfafb6dce9f4` |
| H06 | runtime_facing_code | `cmd/orgctl/skill.go` | `:93:runSkill` | `074258e13f259f5ad094810e5a2c773f80e1190850a3ea0ebc7f85855b674e04` |
| H07 | configuration | `ingenieria_ia/arquitecto_software/PERFIL.md` | `WHOLE_ARTIFACT` | `28ef80259795a77fd2fbee31f376c57fce996b922ffd9f4b9af2165240980b5b` |
| H08 | configuration | `negocio/analista_kpis/PERFIL.md` | `WHOLE_ARTIFACT` | `9c44d7bdc55a69b0c5eabf9d7b6c32fb84ece16005d08a2d5ee42c972b4e14e1` |
| H09 | configuration | `negocio/rrpp_alianzas/PERFIL.md` | `WHOLE_ARTIFACT` | `9643eea15591738d44907a5a6695356de0ddc02cb49c8fab4bdee25886713c5c` |
| H10 | migrations | `migrations/000005_create_capability_policy_engine.up.sql` | `WHOLE_ARTIFACT` | `fbc9bb4dfe3926f1309d976b450b28417163921210b12fce5da1722a83282ff1` |
| H11 | migrations | `migrations/000021_create_provider_wallets.down.sql` | `WHOLE_ARTIFACT` | `5714d32fe54180513616f70816842813d1100ea511dd6024047e223ef600bdef` |
| H12 | migrations | `migrations/000047_seed_openai_responses_pricing_and_wallet.down.sql` | `WHOLE_ARTIFACT` | `a794179888eaeebdbe19e534c77223f2de77d0254aeb8d8a02fa1dde8f87d641` |
| H13 | test_only_artifacts | `internal/cellworker/config_test.go` | `:9:validConfig` | `54d22f127c616c8813fff692f19a2d891889a8652022fdb3662f110ba14841d8` |
| H14 | test_only_artifacts | `internal/organization/registry/service_test.go` | `:14:GetOrganization` | `09f92eaadd54fa187b69d584f9ba31e4ea7124b77c22f401e428adad1eb37177` |
| H15 | test_only_artifacts | `internal/memory/authz/gate_test.go` | `:19:Evaluate` | `b5d177318120b4fe4c38143821c4d82b15e5b030f4fed6864d112226d823687e` |
| H16 | persistence_artifacts | `internal/platform/postgres/unit_of_work.go` | `:23:NewUnitOfWork` | `a1584a796d82c7e7aa3840966172be90bee078895636bfb2c572959b3dad0e7e` |
| H17 | persistence_artifacts | `internal/memory/postgres/search.go` | `:23:vectorChannelClause` | `8dd1d261111ec7352bd5e8a469f8918df153e62ed8a98a4d1f431562636c1a70` |
| H18 | persistence_artifacts | `internal/costledger/postgres/embeddings.go` | `:15:CreateEmbeddingInvocation` | `0577fa2ec28e296ac54bd37abdf02509504aede8673dd408b7cd7a5eddc9eb1c` |
| H19 | event_producing_code | `internal/modelruntime/postgres/reconcile.go` | `:10:Reconcile` | `5396f6db368441d0b4d47cebaacae038335b5e3a4804cc44406d538710b5d731` |
| H20 | event_producing_code | `internal/modelruntime/postgres/invocations.go` | `:13:CreateInvocation` | `7f8926d3e522ff525ce69f140938343a3e0c727a68864f61664b3f55b38cf37c` |
| H21 | event_producing_code | `internal/tasks/postgres/mutate.go` | `:14:AddDependency` | `6aa5438f4052460006b342032b1ed48b015f1717df960f88fc086e057782dba5` |
| H22 | supporting_infrastructure | `scripts/check-model-identity-fitness.sh` | `WHOLE_ARTIFACT` | `3c768d24599db56899e6369d30eac2bafedb9a5c4efda75fbfe06249e7fcaf46` |
| H23 | supporting_infrastructure | `deployments/postgres/RUNBOOK-restore-against-existing-database.md` | `WHOLE_ARTIFACT` | `d2e963bc4cda1e7ee6e72613c2896ab56e63f9698cbe550e0b9e547d51f99ce2` |
| H24 | supporting_infrastructure | `scripts/check-improvement-fitness.sh` | `WHOLE_ARTIFACT` | `7340edef792525a49e50247db6c4a88e58f04938cc19d4c9eb91c8f15cd16cc7` |

No derivation-set element appears in this list.

## 8. Independent-review protocol

Two non-model rule-application passes received only the frozen ontology and the
same sealed candidate packets. Reviewer A processed H01->H24 and sealed its
output before the Reviewer B pass. Reviewer B processed H24->H01 and was not
provided A's classifications. Neither output was reconciled or modified before
the agreement calculation. Model spend was zero.

Each result below includes terminal classification, capability ID where
positive, artifact roles, Q1-Q6 path, reason, and provenance.

### Reviewer A

| ID | Classification / capability | Artifact roles | Q1-Q6 | Reason and provenance |
|---|---|---|---|---|
| H01 | `NOT_A_CAPABILITY` | SUPPORTING_INFRASTRUCTURE | Y,N,—,—,—,— | Adapter arithmetic lacks autonomous responsibility; `adapter.go:110-123`. |
| H02 | `NOT_A_CAPABILITY` | DECLARATION | N,—,—,—,—,— | JSON-shape structs only; `schemas.go:1-11`. |
| H03 | `NOT_A_CAPABILITY` | DECLARATION | N,—,—,—,—,— | Package documentation only; `doc.go:1-7`. |
| H04 | `NOT_A_CAPABILITY` | SUPPORTING_INFRASTRUCTURE | Y,N,—,—,—,— | CLI routing does not own budget behavior; `budget.go:19-38`. |
| H05 | `NOT_A_CAPABILITY` | SUPPORTING_INFRASTRUCTURE, CONFIGURATION | Y,N,—,—,—,— | Runtime bootstrap/wiring; `model_egress.go:17-52`. |
| H06 | `UNRESOLVED` | SUPPORTING_INFRASTRUCTURE, BEHAVIOR_IMPLEMENTATION | Y,Y,Y,Y,U,— | Q5 cannot distinguish adapter from lifecycle responsibility; `skill.go:93-381`. |
| H07 | `NOT_A_CAPABILITY` | DECLARATION, CONFIGURATION | N,—,—,—,—,— | Role profile only; `PERFIL.md:1-40`. |
| H08 | `NOT_A_CAPABILITY` | DECLARATION, CONFIGURATION | N,—,—,—,—,— | Role profile only; `PERFIL.md:1-46`. |
| H09 | `NOT_A_CAPABILITY` | DECLARATION, CONFIGURATION | N,—,—,—,—,— | Role profile only; `PERFIL.md:1-46`. |
| H10 | `NOT_A_CAPABILITY` | DECLARATION, PERSISTENCE_REPRESENTATION, BEHAVIOR_IMPLEMENTATION | Y,N,—,—,—,— | Whole migration mixes schema/guards and has no single unit; SQL `:1-112`. |
| H11 | `NOT_A_CAPABILITY` | DECLARATION, PERSISTENCE_REPRESENTATION | N,—,—,—,—,— | Down-migration representation; SQL `:1-8`. |
| H12 | `NOT_A_CAPABILITY` | DECLARATION, PERSISTENCE_REPRESENTATION | Y,N,—,—,—,— | Rollback has no continuing autonomous responsibility; SQL `:1-19`. |
| H13 | `NOT_A_CAPABILITY` | TEST_ONLY | Y,N,—,—,—,— | Test config constructor; `config_test.go:9-24`. |
| H14 | `NOT_A_CAPABILITY` | TEST_ONLY | Y,N,—,—,—,— | Fake registry method; `service_test.go:14-19`. |
| H15 | `NOT_A_CAPABILITY` | TEST_ONLY | Y,N,—,—,—,— | Fake gate evaluator; `gate_test.go:19-29`. |
| H16 | `NOT_A_CAPABILITY` | SUPPORTING_INFRASTRUCTURE, PERSISTENCE_REPRESENTATION | Y,N,—,—,—,— | Selected constructor only wraps a pool; `unit_of_work.go:23-25`. |
| H17 | `SUPPORTING_ARTIFACT_OF:OC-ORGANIZATIONAL-MEMORY-SEARCH` | SUPPORTING_INFRASTRUCTURE, PERSISTENCE_REPRESENTATION | Y,Y,Y,Y,Y,— | SQL-channel helper supports Search in same packet; `search.go:23-60,71-164`. |
| H18 | `SUPPORTING_ARTIFACT_OF:OC-EMBEDDING-COST-ACCOUNTING` | PERSISTENCE_REPRESENTATION, SUPPORTING_INFRASTRUCTURE | Y,Y,Y,Y,Y,— | Registration supports adjacent reserve/reconcile/release behavior; `embeddings.go:15-180`. |
| H19 | `ORGANIZATIONAL_CAPABILITY` / `OC-MODEL-DISPATCH-EXPIRY-RECONCILIATION` | BEHAVIOR_IMPLEMENTATION, PERSISTENCE_REPRESENTATION, RUNTIME_EVIDENCE | Y,Y,Y,Y,N,N | State-dependent expired-claim recovery; `reconcile.go:10-172`. |
| H20 | `ORGANIZATIONAL_CAPABILITY` / `OC-MODEL-INVOCATION-REGISTRATION` | BEHAVIOR_IMPLEMENTATION, PERSISTENCE_REPRESENTATION, RUNTIME_EVIDENCE | Y,Y,Y,Y,N,N | Idempotent durable registration/event; `invocations.go:13-43`. |
| H21 | `ORGANIZATIONAL_CAPABILITY` / `OC-TASK-DEPENDENCY-ENFORCEMENT` | BEHAVIOR_IMPLEMENTATION, PERSISTENCE_REPRESENTATION, RUNTIME_EVIDENCE | Y,Y,Y,Y,N,N | State/org/cycle edge enforcement; `mutate.go:14-69`. |
| H22 | `ORGANIZATIONAL_CAPABILITY` / `OC-MODEL-IDENTITY-FITNESS-GATE` | BEHAVIOR_IMPLEMENTATION, SUPPORTING_INFRASTRUCTURE | Y,Y,Y,Y,N,N | Executable default-deny source gate; script `:1-70`. |
| H23 | `NOT_A_CAPABILITY` | DECLARATION, SUPPORTING_INFRASTRUCTURE | N,—,—,—,—,— | Human runbook, no executed entry; markdown `:1-117`. |
| H24 | `ORGANIZATIONAL_CAPABILITY` / `OC-IMPROVEMENT-FITNESS-GATE` | BEHAVIOR_IMPLEMENTATION, SUPPORTING_INFRASTRUCTURE | Y,Y,Y,Y,N,N | Executable improvement source gate; script `:1-131`. |

### Reviewer B

| ID | Classification / capability | Artifact roles | Q1-Q6 | Reason and provenance |
|---|---|---|---|---|
| H01 | `NOT_A_CAPABILITY` | SUPPORTING_INFRASTRUCTURE | Y,N,—,—,—,— | Non-autonomous adapter helper; `adapter.go:110-123`. |
| H02 | `NOT_A_CAPABILITY` | DECLARATION | N,—,—,—,—,— | Shape declarations only; `schemas.go:1-11`. |
| H03 | `NOT_A_CAPABILITY` | DECLARATION | N,—,—,—,—,— | Package comment only; `doc.go:1-7`. |
| H04 | `NOT_A_CAPABILITY` | SUPPORTING_INFRASTRUCTURE | Y,N,—,—,—,— | Interface adapter, not called responsibility; `budget.go:19-38`. |
| H05 | `NOT_A_CAPABILITY` | SUPPORTING_INFRASTRUCTURE, CONFIGURATION | Y,N,—,—,—,— | Bootstrap support; `model_egress.go:17-52`. |
| H06 | `UNRESOLVED` | SUPPORTING_INFRASTRUCTURE, BEHAVIOR_IMPLEMENTATION | Y,Y,Y,Y,U,— | Q5 support/autonomy unresolved; `skill.go:93-381`. |
| H07 | `NOT_A_CAPABILITY` | DECLARATION, CONFIGURATION | N,—,—,—,—,— | Role configuration only; `PERFIL.md:1-40`. |
| H08 | `NOT_A_CAPABILITY` | DECLARATION, CONFIGURATION | N,—,—,—,—,— | Role configuration only; `PERFIL.md:1-46`. |
| H09 | `NOT_A_CAPABILITY` | DECLARATION, CONFIGURATION | N,—,—,—,—,— | Role configuration only; `PERFIL.md:1-46`. |
| H10 | `NOT_A_CAPABILITY` | DECLARATION, PERSISTENCE_REPRESENTATION, BEHAVIOR_IMPLEMENTATION | Y,N,—,—,—,— | Whole migration fails stable single responsibility; SQL `:1-112`. |
| H11 | `NOT_A_CAPABILITY` | DECLARATION, PERSISTENCE_REPRESENTATION | N,—,—,—,—,— | Drop representation only; SQL `:1-8`. |
| H12 | `NOT_A_CAPABILITY` | DECLARATION, PERSISTENCE_REPRESENTATION | Y,N,—,—,—,— | Migration rollback lacks runtime responsibility; SQL `:1-19`. |
| H13 | `NOT_A_CAPABILITY` | TEST_ONLY | Y,N,—,—,—,— | Fixture helper; `config_test.go:9-24`. |
| H14 | `NOT_A_CAPABILITY` | TEST_ONLY | Y,N,—,—,—,— | Fake reader; `service_test.go:14-19`. |
| H15 | `NOT_A_CAPABILITY` | TEST_ONLY | Y,N,—,—,—,— | Fake evaluator; `gate_test.go:19-29`. |
| H16 | `NOT_A_CAPABILITY` | SUPPORTING_INFRASTRUCTURE, PERSISTENCE_REPRESENTATION | Y,N,—,—,—,— | Constructor, not transaction behavior; `unit_of_work.go:23-25`. |
| H17 | `SUPPORTING_ARTIFACT_OF:OC-ORGANIZATIONAL-MEMORY-SEARCH` | SUPPORTING_INFRASTRUCTURE, PERSISTENCE_REPRESENTATION | Y,Y,Y,Y,Y,— | Helper subordinate to Search; `search.go:23-60,71-164`. |
| H18 | `ORGANIZATIONAL_CAPABILITY` / `OC-EMBEDDING-INVOCATION-REGISTRATION` | BEHAVIOR_IMPLEMENTATION, PERSISTENCE_REPRESENTATION | Y,Y,Y,Y,N,N | Independently invocable validation/registration contract; `embeddings.go:15-40`. |
| H19 | `ORGANIZATIONAL_CAPABILITY` / `OC-MODEL-DISPATCH-EXPIRY-RECONCILIATION` | BEHAVIOR_IMPLEMENTATION, PERSISTENCE_REPRESENTATION, RUNTIME_EVIDENCE | Y,Y,Y,Y,N,N | State-sensitive expired-claim recovery; `reconcile.go:10-172`. |
| H20 | `ORGANIZATIONAL_CAPABILITY` / `OC-MODEL-INVOCATION-REGISTRATION` | BEHAVIOR_IMPLEMENTATION, PERSISTENCE_REPRESENTATION, RUNTIME_EVIDENCE | Y,Y,Y,Y,N,N | Requested/reused/conflict registration; `invocations.go:13-43`. |
| H21 | `ORGANIZATIONAL_CAPABILITY` / `OC-TASK-DEPENDENCY-ENFORCEMENT` | BEHAVIOR_IMPLEMENTATION, PERSISTENCE_REPRESENTATION, RUNTIME_EVIDENCE | Y,Y,Y,Y,N,N | Atomic edge admission and transition; `mutate.go:14-69`. |
| H22 | `ORGANIZATIONAL_CAPABILITY` / `OC-MODEL-IDENTITY-FITNESS-GATE` | BEHAVIOR_IMPLEMENTATION, SUPPORTING_INFRASTRUCTURE | Y,Y,Y,Y,N,N | Cohesive executable source gate; script `:1-70`. |
| H23 | `NOT_A_CAPABILITY` | DECLARATION, SUPPORTING_INFRASTRUCTURE | N,—,—,—,—,— | Human instructions only; markdown `:1-117`. |
| H24 | `ORGANIZATIONAL_CAPABILITY` / `OC-IMPROVEMENT-FITNESS-GATE` | BEHAVIOR_IMPLEMENTATION, SUPPORTING_INFRASTRUCTURE | Y,Y,Y,Y,N,N | Cohesive executable source gate; script `:1-131`. |

## 9. Positive capability records

The following union materializes every positive decision. `reviewers` records
which sealed result emitted it. Empty provenance arrays explicitly mean that
the evidence class was not present in the candidate packet.

```yaml
- capability_id: OC-MODEL-DISPATCH-EXPIRY-RECONCILIATION
  reviewers: [A, B]
  name: Expired model-dispatch reconciliation
  behavioral_contract: >-
    When Reconcile inspects expired claims for one organization, it atomically
    releases pre-send claims, marks post-send outcomes ambiguous, or fails a
    response-received attempt according to locked current state; nonexpired or
    incompatible rows remain unchanged and resulting events are recorded.
  causal_purpose: Recover abandoned dispatch work without unsafe duplicate external sends.
  entrypoints_or_triggers: ["internal/modelruntime/postgres/reconcile.go:10"]
  enforcement_or_transition_points: ["internal/modelruntime/postgres/reconcile.go:70-168"]
  reads_or_inputs: [organization ID, batch limit, attempt/invocation status and expiry]
  writes_or_effects: [state transitions, audit/outbox events, reconciliation counters]
  implementation_provenance: ["internal/modelruntime/postgres/reconcile.go:10-172"]
  declaration_provenance: [] # not present in candidate packet
  configuration_provenance: [batch and outboxMax inputs]
  possible_runtime_evidence: [reconciled, ambiguous, and failed invocation events]
  boundary_with_adjacent_capabilities: Active provider sending is separate; this contract begins only at expiry.
  classification_basis: Q1=Y,Q2=Y,Q3=Y,Q4=Y,Q5=N,Q6=N.

- capability_id: OC-MODEL-INVOCATION-REGISTRATION
  reviewers: [A, B]
  name: Idempotent model-invocation registration
  behavioral_contract: >-
    When CreateInvocation receives a prepared request, it atomically creates a
    requested invocation and event, reuses an equal idempotency request with a
    reuse event, or rejects reuse with a different request hash.
  causal_purpose: Establish one durable, auditable invocation per organizational model request.
  entrypoints_or_triggers: ["internal/modelruntime/postgres/invocations.go:13"]
  enforcement_or_transition_points: ["internal/modelruntime/postgres/invocations.go:20-41"]
  reads_or_inputs: [PreparedInvocation, organization/idempotency key, request hash]
  writes_or_effects: [invocation row, requested/reused event, or conflict]
  implementation_provenance: ["internal/modelruntime/postgres/invocations.go:13-43"]
  declaration_provenance: [] # not present in candidate packet
  configuration_provenance: [outboxMax]
  possible_runtime_evidence: [AuditInvocationRequested, AuditInvocationReused]
  boundary_with_adjacent_capabilities: Provider dispatch begins after registration and has a separate trigger/effect contract.
  classification_basis: Q1=Y,Q2=Y,Q3=Y,Q4=Y,Q5=N,Q6=N.

- capability_id: OC-TASK-DEPENDENCY-ENFORCEMENT
  reviewers: [A, B]
  name: Task dependency enforcement
  behavioral_contract: >-
    When AddDependency is requested, it atomically rejects forbidden task
    states, cross-organization edges, and cycles; otherwise it adds the edge
    idempotently and blocks a ready task whose dependency is incomplete,
    recording the applicable event.
  causal_purpose: Preserve executable task ordering without cyclic or cross-organization dependencies.
  entrypoints_or_triggers: ["internal/tasks/postgres/mutate.go:14"]
  enforcement_or_transition_points: ["internal/tasks/postgres/mutate.go:20-64"]
  reads_or_inputs: [task IDs/states, organization IDs, dependency graph]
  writes_or_effects: [dependency edge, optional blocked transition, task event, or rejection]
  implementation_provenance: ["internal/tasks/postgres/mutate.go:14-69"]
  declaration_provenance: [] # not present in candidate packet
  configuration_provenance: [outboxMaxAttempts]
  possible_runtime_evidence: [task.dependency_added, task.blocked]
  boundary_with_adjacent_capabilities: Requirement/evidence mutation has separate entries and causal purposes.
  classification_basis: Q1=Y,Q2=Y,Q3=Y,Q4=Y,Q5=N,Q6=N.

- capability_id: OC-MODEL-IDENTITY-FITNESS-GATE
  reviewers: [A, B]
  name: Model execution identity source-fitness gate
  behavioral_contract: >-
    When the fitness script runs against its pinned base and source, it exits
    nonzero on unauthorized canonical changes, weakened default-deny/Ed25519
    enforcement, forbidden credential/process surfaces, or missing immutable
    and one-use guards, and succeeds only after all checks pass.
  causal_purpose: Prevent source changes from weakening canonical model-execution identity controls.
  entrypoints_or_triggers: ["scripts/check-model-identity-fitness.sh:1"]
  enforcement_or_transition_points: ["scripts/check-model-identity-fitness.sh:8-69"]
  reads_or_inputs: [pinned base, source, canonical policy, migration files]
  writes_or_effects: [zero/nonzero exit and diagnostic output]
  implementation_provenance: ["scripts/check-model-identity-fitness.sh:1-70"]
  declaration_provenance: [] # not present as candidate evidence
  configuration_provenance: [MODEL_IDENTITY_BASE_SHA]
  possible_runtime_evidence: [CI/process exit status and diagnostics]
  boundary_with_adjacent_capabilities: Runtime identity authentication is separate; this gate only checks source fitness.
  classification_basis: Q1=Y,Q2=Y,Q3=Y,Q4=Y,Q5=N,Q6=N.

- capability_id: OC-IMPROVEMENT-FITNESS-GATE
  reviewers: [A, B]
  name: Improvement subsystem source-fitness gate
  behavioral_contract: >-
    When the fitness script runs against pinned history/source, it exits
    nonzero if required artifacts, isolation boundaries, sensitive-data bans,
    lifecycle guards, approval gates, integrity checks, or named tests are
    absent or violated, and succeeds only after every check passes.
  causal_purpose: Prevent improvement changes from bypassing lifecycle, security, approval, or integrity invariants.
  entrypoints_or_triggers: ["scripts/check-improvement-fitness.sh:1"]
  enforcement_or_transition_points: ["scripts/check-improvement-fitness.sh:8-130"]
  reads_or_inputs: [pinned base/tip commits and repository source]
  writes_or_effects: [zero/nonzero exit and diagnostics]
  implementation_provenance: ["scripts/check-improvement-fitness.sh:1-131"]
  declaration_provenance: [] # not present as candidate evidence
  configuration_provenance: [IMPROVEMENT_BASE_SHA, IMPROVEMENT_TIP_SHA]
  possible_runtime_evidence: [CI/process exit status and diagnostics]
  boundary_with_adjacent_capabilities: Runtime improvement lifecycle behavior is separate; this gate checks source conformance.
  classification_basis: Q1=Y,Q2=Y,Q3=Y,Q4=Y,Q5=N,Q6=N.

- capability_id: OC-EMBEDDING-INVOCATION-REGISTRATION
  reviewers: [B]
  name: Embedding invocation registration
  behavioral_contract: >-
    When CreateEmbeddingInvocation receives complete organization, actor,
    provider/model, billing-mode, and operation identity, it assigns a UTC time
    if absent, inserts and returns a durable invocation ID; invalid identity or
    persistence failure produces no positive result.
  causal_purpose: Create an attributable durable identity for each embedding operation before financial accounting.
  entrypoints_or_triggers: ["internal/costledger/postgres/embeddings.go:15"]
  enforcement_or_transition_points: ["internal/costledger/postgres/embeddings.go:16-39"]
  reads_or_inputs: [EmbeddingInvocation identity, optional task/time]
  writes_or_effects: [embedding_invocations row and ID, or error]
  implementation_provenance: ["internal/costledger/postgres/embeddings.go:15-40"]
  declaration_provenance: [] # not present in candidate packet
  configuration_provenance: [] # not present in candidate packet
  possible_runtime_evidence: [embedding_invocations row; no event supplied]
  boundary_with_adjacent_capabilities: Reserve/reconcile/release use separate entries and alter wallet balances; registration does not.
  classification_basis: Q1=Y,Q2=Y,Q3=Y,Q4=Y,Q5=N,Q6=N.
```

No positive decision lacks a behavioral contract.

## 10. Agreement metrics

The exact label includes a `SUPPORTING_ARTIFACT_OF` target. Boundary mapping is
`ORGANIZATIONAL_CAPABILITY` versus every other terminal result.

```text
N = 24
Reviewer A capabilities = 5
Reviewer B capabilities = 6
Exact classification agreements = 23
Exact classification agreement = 23/24 = 0.958333 (95.83%)
Capability-boundary agreements = 23
Capability-boundary agreement = 23/24 = 0.958333 (95.83%)
Binary expected agreement = (5/24 * 6/24) + (19/24 * 18/24)
                          = 0.645833
Cohen kappa = (0.958333 - 0.645833) / (1 - 0.645833)
            = 0.882353
Disagreement count = 1
```

Kappa is mathematically defined because both boundary categories occur. V1
passes the preregistered 90% boundary-agreement and 0.80 kappa thresholds.

## 11. Disagreement analysis

| Candidate | Reviewer A | Reviewer B | Primary class | Rule that allowed divergence |
|---|---|---|---|---|
| H18 | `SUPPORTING_ARTIFACT_OF:OC-EMBEDDING-COST-ACCOUNTING` | `ORGANIZATIONAL_CAPABILITY` / `OC-EMBEDDING-INVOCATION-REGISTRATION` | `ARTIFACT_VS_CAPABILITY_CONFUSION` | Q5 plus the split rule: the packet permits registration to be read either as a persistence step subordinate to the adjacent reserve/reconcile/release responsibility or as an independently invocable validation-and-registration contract with a distinct causal purpose. |

The disagreement was not voted away. No ontology text was modified and no V2
was designed. The case remains recorded for future application, but V1 remains
reproducible under the preregistered thresholds.

## 12. Reproducibility and separation checks

- Operational definition frozen before holdout: PASS.
- Holdout excluded all derivation elements: PASS.
- Two sealed, non-model review passes without cross-fed results: PASS.
- Capability-boundary agreement >=90%: PASS (95.83%).
- Cohen kappa >=0.80: PASS (0.882353).
- Every positive decision has a verifiable behavioral contract: PASS.
- Artifact role remains separate from capability classification: PASS.
- Expected mechanism/capability count used: false.
- Q3-PRE-DEF-001 used for tuning: false.
- Ontology revisions: none; V2 was neither needed nor designed.

Result: `Q3_ONTOLOGY_V1` is reproducible for the preregistered validation.

## 13. Future source-space accounting contract

`REFORMULATED-Q3-002`—not executed here—must first register a deterministic,
accessible source universe with stable source IDs, paths/locators, content
hashes, eligibility rule, and access limitation. Every registered source unit
must end in exactly one accounting state:

```text
SOURCE
  -> RELEVANT_CAPABILITY_EVIDENCE(capability_id, artifact_role, provenance)
  -> IRRELEVANT_WITH_PROOF(decision_path, reason, provenance)
  -> UNRESOLVED(decision_path, missing_evidence, provenance)
```

Every surviving runtime-evidence unit must likewise map to a capability as
runtime evidence/support, receive an evidenced irrelevant classification, or
remain unresolved. Completeness equations are:

```text
registered_source_total
= relevant_source_total + irrelevant_with_proof_total + unresolved_source_total

surviving_runtime_evidence_total
= mapped_runtime_evidence_total
 + irrelevant_runtime_with_proof_total
 + unresolved_runtime_evidence_total
```

`not mentioned by collector -> irrelevant` is forbidden. The future run must
apply the frozen ontology to relevant evidence; it must not classify all 52
packages or 48 migrations by artifact identity, and it must not use the eight
provisional PRE-DEF groupings or 20 event types as an expected result.

## 14. Frozen future question-identity contract

Canonical JSON (UTF-8, no trailing newline for contract hashing):

```json
{"measurement_universe":["deterministically_registered_accessible_source_space","surviving_runtime_evidence_universe"],"requested_relation":"declared_or_configured_capability<->surviving_runtime_evidence","required_output_schema":["capability_inventory_under_frozen_ontology","declaration_configuration_provenance","runtime_evidence","observation_limitations","completeness_accounting"],"subject":"organizational_capabilities_implemented_by_this_repository"}
```

Question-identity contract SHA-256:
`5d14179555b1634238ffd197bc56afa42bc5b1dc93e5605f5d2e58f368e48f9c`

Semantics:

- Subject: organizational capabilities implemented by this repository.
- Requested relation: declared/configured capability `<->` surviving runtime
  evidence.
- Measurement universe: deterministically registered accessible source space
  plus the surviving runtime-evidence universe.
- Required output: capability inventory under the frozen ontology, declaration/
  configuration provenance, runtime evidence, observation limitations, and
  completeness accounting.

A future refinement may reduce uncertainty inside these fields. It cannot
remove, replace, or change any field.

## 15. QUESTION_IDENTITY_GATE specification for INSTRUMENT_V4

This is a verifiable specification, not an implementation.

Input must be a structured `RefinementContract` with the four canonical fields
above plus optional narrowing predicates. Free-text refinements must first be
parsed into that structure and are rejected if any field cannot be determined.

Mechanical gate:

1. Load the canonical JSON and verify its pinned SHA-256.
2. Require `subject` byte-equal to the canonical enum value.
3. Require `requested_relation` byte-equal to the canonical relation value;
   direction and both endpoints are mandatory.
4. Require both canonical measurement-universe members. Additional sources are
   accepted only as explicitly labeled observation supplements and cannot
   replace either member.
5. Require all five output-schema members. Additional output fields are
   allowed; removal, renaming, substitution, or conversion to a literal-existence
   proposition is forbidden.
6. Validate each narrowing predicate: it must constrain uncertainty inside one
   preserved field and must not change the population, relation, or output
   obligation outside that field.
7. If every check passes, return `ACCEPT_IDENTITY_PRESERVED` plus the normalized
   contract hash. If any check fails, return `QUESTION_TARGET_DRIFT` and
   `REJECT_TARGET_DRIFT`, naming every changed field. The refinement must not be
   executed.

Mandatory fixtures:

| Fixture | Input proposition | Expected |
|---|---|---|
| NEGATIVE-LITERAL-N | `¿existe literalmente la frase N custom mechanisms?` | `QUESTION_TARGET_DRIFT`, `REJECT_TARGET_DRIFT`; changes subject, relation, universe, and output schema. |
| POSITIVE-NARROW-EVIDENCE | Preserve all four fields; require runtime evidence to include observation timestamps where present. | `ACCEPT_IDENTITY_PRESERVED`; narrows evidence detail only. |
| NEGATIVE-DROP-COMPLETENESS | Preserve inventory but omit completeness accounting. | `QUESTION_TARGET_DRIFT`, `REJECT_TARGET_DRIFT`; required output changed. |
| NEGATIVE-RUNTIME-ONLY | Search surviving events only and omit registered source space. | `QUESTION_TARGET_DRIFT`, `REJECT_TARGET_DRIFT`; measurement universe changed. |

The gate must run before collectors, models, queries, or refinements can spend
resources or mutate any state.

## 16. Final controls

```text
MODEL_SPEND=$0.00
PRODUCTION_MUTATED=false
DATABASE_WRITES=0
MODEL_CALLS=0
EXPECTED_COUNT_USED=false
Q3_PRE_DEF_USED_FOR_TUNING=false
REFORMULATED_Q3_002_RUN=false
QUESTION_EXPANSION_RUN=false
ONTOLOGY_V2_DESIGNED=false
```

`Q3_OPERATIONAL_DEFINITION_VALIDATED`

STOP. Await human review.
