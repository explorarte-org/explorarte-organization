# REFORMULATED-Q3-002 — measurement 001

Mission: `ORGANIZATION-REDESIGN-001`  
Question: `REFORMULATED-Q3-002`  
Scientific base: `588db11599d701fb1e2ecbae19aa00828663dc2b`  
Ontology: `Q3_ONTOLOGY_V1`  
Instrument: `INSTRUMENT_V4`  
Final measurement disposition: `PARTIAL`  
Luna disposition: `ACCEPT`

This report records the first measurement of Q3 under the frozen organizational
capability ontology and the active question-identity gate. The derived inventory
size is not an expected count and was never used to select, merge, split, or tune
a capability.

## 1. Frozen identity and preflight

The measurement preserved exactly:

- subject: `organizational_capabilities_implemented_by_this_repository`;
- relation: `declared_or_configured_capability <-> surviving_runtime_evidence`;
- universe: `deterministically_registered_accessible_source_space + surviving_runtime_evidence_universe`;
- required outputs: capability inventory, declaration/configuration provenance,
  runtime evidence, observation limitations, and completeness accounting.

Identity pins:

- ontology whole-file SHA-256: `0da2f727ceff769469c0ffad32c165690b3f4c1e5deac31d6e099117f9f1f974`;
- ontology normative SHA-256: `cbf4b72975b4a8d8c2650969181f470d7a91fbf5f35beeabd23eb66a03f6e11c`;
- question contract SHA-256: `5d14179555b1634238ffd197bc56afa42bc5b1dc93e5605f5d2e58f368e48f9c`;
- controller SHA-256: `3547b61eba43b3aad6d14eec7313fdab1b480483c7d5d55b2368aba558066c8f`;
- gate binary SHA-256: `43b25561ed645231d4c74b499a99fc8c375aeb7ee947dcd86a9470e2a467f0cd`.

The full entry-gate artifacts were frozen laterally, without merge, at commit
`1da6b8a4526bee015ff558c8b7e23117821424e3` on
`docs/q3-002-full-entry-gate-001`. The scientific root remained detached,
clean, and byte-identical at `/home/ubuntu/campaign-588db11` throughout.

Production DB verification used `redesign_audit_readonly` against
`explorarte_org`: both transaction read-only settings were `on`, timeout was
15 seconds, database/schema CREATE was false, and all 103 table grants were
exclusively SELECT. No temporary object was created.

## 2. Deterministic universe registration

Registration preceded every model call.

Source:

- 1,060 of 1,060 tracked artifacts registered from `git ls-files -z`;
- 4,576 Go function/method locators indexed mechanically;
- source universe SHA-256:
  `a4a7bacbefb1d8f040f582e046a1ccdc0d2088b2c3ee2a42df65ec9d2ddfca43`;
- symbol universe SHA-256:
  `2d5a15df73f8b62a8fd67ad53dcc7ce45c91fce5cb051cd24e339ba53ec58b1f`.

Runtime:

- 103 of 103 accessible tables/views registered;
- all row counts observed in one `REPEATABLE READ READ ONLY` transaction;
- 46 allowlisted categorical columns and 141 surviving categorical values
  registered;
- runtime universe SHA-256:
  `b22d3801be5d8989d1a0025a2bedab7c5cb7656e12f912f12e2ffbc631f406e7`;
- runtime observation SHA-256:
  `a73dfe6cbe73c7ff5da52cae44051cc7cb73b3e726ed67508e9c2aacc5b7b78e`.

Every registered unit has a final category. There is no implicit `omitted`
category.

## 3. Model-assisted classification and audit

The initial DeepSeek packet proposed 21 positive capability records, accounted
for all 70 navigation groups, set `coverage_complete=false`, and supplied 171
resolvable repository citations. It emitted only 36 runtime relations and
incorrectly described the other 67 units as unenumerated. The external registry
proved that all 103 were enumerated and that those 67 units had zero rows.

Grok classified the packet `VALID_PARTIAL` with confidence `0.62`, accepted the
21 records, challenged four boundaries, rejected the 36/103 phrasing, and
recommended `PARTIAL`.

One typed narrowing was then admitted before its provider call:

- profile: `resolve_unclassified_source_entries_against_proposed_capabilities`;
- gate decision: `ACCEPT_IDENTITY_PRESERVED`;
- normalized contract SHA-256:
  `9de292c72799689b1a8d42a52357cd4f7136f5e4080684a8a965cdf32a608abb`;
- provider call allowed: true.

The narrowing addressed exactly the 23 previously unresolved groups. It proposed
14 further candidates. The second Grok audit accepted 7, reclassified 7 under
Q5 as supporting artifacts of adjacent capabilities, challenged 2 accepted
boundaries, and again returned `VALID_PARTIAL` / `PARTIAL` (confidence `0.55`).

No controlled question expansion occurred. No model was allowed to attest
enumeration completeness.

## 4. Derived capability inventory

The final derived inventory contains 28 positive records. Full behavioral
contracts, Q1-Q6 bases, implementation/declaration/configuration provenance,
runtime relations, and adjacent boundaries are in
`final-accounting/capability-inventory.yaml`.

Accepted:

- `OC-agent-budgeting`
- `OC-agent-messaging`
- `OC-capability-authorization`
- `OC-completion-verification`
- `OC-cost-ledger`
- `OC-decision-graph-execution`
- `OC-improvement-promotion`
- `OC-memory-lifecycle`
- `OC-model-dispatch-assignment`
- `OC-model-egress-policy`
- `OC-model-execution-identity`
- `OC-model-invocation-dispatch`
- `OC-model-pricing-resolution`
- `OC-organization-registry`
- `OC-rag-knowledge-lifecycle`
- `OC-secret-clinical-detection`
- `OC-secret-scanning`
- `OC-shadow-verification`
- `OC-skill-lifecycle`
- `OC-staging-workspace`
- `OC-task-lifecycle-management`
- `OC-web-evidence-ingest`

Accepted with an explicit audit challenge:

- `OC-context-compilation`
- `OC-corpus-census`
- `OC-evaluation-comparison`
- `OC-evidence-object-storage`
- `OC-executive-orchestration`
- `OC-question-identity-gate`

The derived inventory SHA-256 is
`b81460712888bfb206bc1c60f9ed73bad003dff3606ad09848d448e5ddf71f98`.

The seven non-positive refinement candidates remain recorded with their exact
`SUPPORTING_ARTIFACT_OF` classifications. They were not counted as capabilities.

## 5. External completeness accounting

Source accounting:

- `CAPABILITY_EVIDENCE`: 701;
- `IRRELEVANT_WITH_PROOF`: 342;
- `UNRESOLVED`: 17;
- omitted: 0;
- accounting SHA-256:
  `858665f589aeeb02e9040fe00d4507cff6a58676e97fec06bc17e05c36662e42`.

Runtime accounting:

- `SURVIVING_EVIDENCE`: 29;
- `DECLARATION_OR_CONFIGURATION_ONLY`: 6;
- `IRRELEVANT_WITH_PROOF`: 1;
- `UNRESOLVED`: 67;
- omitted: 0;
- accounting SHA-256:
  `679d8f7f201da7f77a7f26811f1141ecfd6d15e6c6c2f8d9e49f5715f4c29291`.

The 67 unresolved runtime units are zero-row tables. Their status means no
surviving observation in the sealed snapshot. It does not mean that a capability
was historically absent. Missing evidence is not a missing capability.

Seventeen source artifacts remain explicitly unresolved around corpus-curation
identity/gap/store boundaries, Gemini embedding adapter flow, and evaluation
metrics/persistence. Required output fields remain intact, so the correct
measurement disposition is `PARTIAL`, not `UNMEASURABLE_AS_SPECIFIED`.

The completeness artifact SHA-256 is
`37c26afabaaef9f2b2ba3f7143a3c9cefb28f3689f71204a2f29e5ffa9458667`.

## 6. Cost and safety

Model API calls:

- DeepSeek: 19 calls/iterations, USD `0.112111622`;
- Grok: 2 audits, USD `0.5690875`;
- Luna: 1 disposition, USD `0.0007792`.

Q3-002 total: USD `0.681978322`.  
Mission spend after Q3-002: USD `7.056978322`.

All 67 repository tool calls recorded question
`REFORMULATED-Q3-002` and resolved to
`/home/ubuntu/campaign-588db11`. DeepSeek made no SQL tool call. Production was
queried only by the deterministic read-only registrar.

`PRODUCTION_MUTATED=false`  
`EXPECTED_COUNT_USED=false`  
`QUESTION_TARGET_DRIFT=false`  
`QUESTION_EXPANSION_EXECUTED=false`  
`V2_DESIGN_ALLOWED=false`

## 7. Final status

`REFORMULATED_Q3_002_MEASUREMENT_001 = PARTIAL`

`LUNA_DISPOSITION = ACCEPT`

The result is authoritative only as an explicit partial inventory under the
frozen ontology. It does not authorize automatic refinement, controlled question
expansion, V2, production changes, or a completeness claim.
