# INSTRUMENT_V4_CONTROLLER_BINDING_001

## Status

`CONTROLLER_BINDING_IMPLEMENTATION = VALIDATED_OFFLINE`

`INSTRUMENT_V4 = VALIDATED`

The binding, ordering, negative historical replay, zero-call property,
determinism, and provenance guards pass. Human review accepted
`INSTRUMENT_V4_VALIDATION_PROTOCOL_AMENDMENT_001`; its pre-registered replay
also passes.

## Frozen input

- Base SHA: `7cfcbe5cfdda9e8939a9b6927219d9dc9f8a9e71`
- Historical controller: `/home/ubuntu/redesign-001/loop_controller.py`
- Original controller SHA256: `3fc02a8ae20a43d6b4cd0a167af488aa6b72ddd0b4acaa7e084d4886b8be7962`
- Original controller bytes: `60978`
- Byte-exact backup: `/home/ubuntu/redesign-001/loop_controller.py.instrument-v4-controller-binding-001.original`
- Frozen question-identity contract: `5d14179555b1634238ffd197bc56afa42bc5b1dc93e5605f5d2e58f368e48f9c`

## Frozen original locators

- refinement decision: `step_luna_disposition`, line 852
- disposition provider call: `worker.call_luna`, line 898
- refinement extraction: `assignments = er.get("assignments")`, line 913
- free-form refinement selection: `refined = assignments[0]`, line 923
- next-task mutation: `state["current_task"] = refined`, line 925
- next phase mutation: `DEEPSEEK_EVIDENCE_REQUIRED`, line 927
- evidence step: `step_deepseek_evidence`, line 713
- first evidence-provider call: `worker.call_deepseek_with_tools`, line 732
- evidence repair-provider call: `worker.call_deepseek_with_tools`, line 753

Line numbers identify the frozen original bytes and are not used as runtime
selectors.

## Binding design

The controller does not implement or copy the semantic rules in Python. It
invokes the versioned `question-identity-gate` process and verifies the process
binary by SHA256 before every refinement decision.

Gate source-set SHA256 is
`5b1dd340e26a27879f30830d0b51068220b07665b0a459aaaa1f7b512d4c307a`.
Two consecutive builds with `-trimpath -buildvcs=false` reproduced the frozen
binary `43b25561ed645231d4c74b499a99fc8c375aeb7ee947dcd86a9470e2a467f0cd`
byte-for-byte.

The candidate language is closed and structured:

- binding schema;
- frozen contract hash;
- question ID;
- one allowlisted refinement profile.

Unknown fields, free-form assignments, unknown profiles, malformed input,
schema drift, contract drift, missing gate configuration, missing binaries,
binary hash mismatch, invalid gate output, or decision/permission
inconsistency fail closed.

The candidate model cannot provide the executable assignment. On acceptance,
the Go component creates the complete task from a versioned profile. The
historical controller assigns only this returned task to `current_task`.

The resulting order is:

```text
candidate assignment
  -> byte-exact JSON serialization
  -> verified question-identity-gate binary
  -> canonical question contract
  -> REJECT: drift record, no current_task mutation, no evidence provider
  -> ACCEPT: controller-owned task, existing evidence-provider path
```

## Bound controller identity and locators

- Bound controller SHA256: `3547b61eba43b3aad6d14eec7313fdab1b480483c7d5d55b2368aba558066c8f`
- Bound controller bytes: `66401`
- Reproducible unified-patch SHA256: `799df9c2085cd3a1a990a7c37e5f229d93a9939631f9235c673c62cffcb3fc2e`
- Reproducible unified-patch bytes: `8525`
- Versioned patch: `tools/instrumentv4/loop_controller.binding.patch`
- binding entrypoint: `bind_question_refinement`, line 44
- evidence step: `step_deepseek_evidence`, line 781
- first evidence-provider call: line 800
- disposition step: `step_luna_disposition`, line 920
- disposition provider call: line 975
- candidate extraction: line 990
- reject branch before task mutation: line 1000
- accepted authorized-task mutation: line 1017

Patch derivation is byte-exact:

```text
diff -u --label loop_controller.py.original --label loop_controller.py.bound \
  ORIGINAL_CONTROLLER BOUND_CONTROLLER
```

No newline conversion, normalization, or reserialization is applied.
Applying the versioned patch to the frozen backup was verified to reproduce the
bound controller byte-for-byte.

## Offline replay

The replay imports the real controller at
`/home/ubuntu/redesign-001/loop_controller.py`, substitutes a fake worker, and
uses the built Go gate. It performs no HTTP call, model call, database access,
or production mutation.

Historical negative fixture:

- source: `campaign_q3_state.json.accepted_findings[0].task`
- fixture SHA256: `84dbb392c06d3977975bf3933789aeae65ba9f6c5733fbc7829ac2ddc04cdb0b`
- source extraction SHA256: the same value
- contains the rule `Search ... exact-five|exactly five|five custom mechanisms`
- result: `QUESTION_TARGET_DRIFT`
- downstream provider calls before/after: `0 / 0`
- drift records: `1`

Positive typed fixture:

- profile: `runtime_evidence_observation_timestamps_where_present`
- fixture SHA256: `4d395c06d80276c60bb911369eb54eeaec7cf1d0161ca870b1e821914a0f272e`
- fixture bytes: `265`
- expected and observed result: `ACCEPT_IDENTITY_PRESERVED`
- expected and observed authorized task: `Q3-002-RUNTIME-EVIDENCE-TIMESTAMPS`
- expected and observed existing downstream provider path calls: `1`
- pre-registered at: `2026-08-16T02:54:44Z`
- amended replay executed at: `2026-08-16T02:55:45Z`

Additional properties:

- identical input and contract produce byte-equivalent binding outcomes;
- a wrong gate-binary identity fails with `instrument_regression`;
- wrong gate identity performs zero downstream provider calls;
- rejected candidates never return an authorized task;
- accepted candidates never use candidate prose as their task.

## Historical-positive limitation

The controller called LUNA with `store=false`. Its call ledger retained model,
cost, timestamp, and task metadata, but not response bodies. Campaign state was
updated in place. The following survive:

- log marker that round 1 selected `Q3-EVIDENCE-002`;
- DeepSeek evidence generated afterward;
- exact final drift assignment `REFORMULATED-Q3-001-EVIDENCE-003`.

The actual assignment body that produced `Q3-EVIDENCE-002` does not survive.
Reconstructing text from its later evidence packet would not be a replay of the
same historical input. Therefore:

`HISTORICAL_LEGITIMATE_NARROWING_REPLAY = UNAVAILABLE_WITH_PROOF`

## Protocol amendment 001

Human review accepted the following disposition:

- do not search further for `Q3-EVIDENCE-002`;
- do not reconstruct it from downstream evidence;
- preserve `HISTORICAL_LEGITIMATE_NARROWING_REPLAY = UNAVAILABLE_WITH_PROOF` as
  a measurement limitation;
- waive historicity for the positive-path sample;
- replace it with the exact pre-registered typed fixture above;
- leave the exact historical target-drift replay requirement unchanged.

The amended validation conjunction requires the real controller binding,
pre-provider gate ordering, rejection of the exact historical drift fixture,
acceptance of the pre-registered typed positive, zero downstream calls on
rejection, deterministic replay, and controller/gate provenance binding.

No result from the amended replay was available when the positive fixture,
expected status, expected call count, timestamp, and hash above were recorded.

## Verification

- questionidentity and CLI unit tests: PASS
- race detector: PASS
- package vet: PASS
- real-controller negative replay: PASS
- real-controller typed-positive replay: PASS
- deterministic replay: PASS
- wrong-binary fail-closed replay: PASS
- Python syntax compilation: PASS
- `go vet ./...`: PASS
- `go test -short ./...`: PASS
- `orgd`, `orgctl`, and `question-identity-gate` builds: PASS
- `make verify`: `BASELINE_BLOCKED` by the same ten pre-existing gofmt files
- baseline gofmt-list SHA256: `263f0c171e0aff563c57adebdcfbdaf5c53bc2f7ca3ee89f295e17a03700a357`
- changed files causing the formatting failure: `0`
- model calls: `0`
- model spend: `$0.00`
- database writes: `0`
- production mutated: `false`
- Q3-002 executed: `false`
- question expansion executed: `false`
- V2 authorized: `false`

## Closure decision

All seven terms of the amended validation conjunction pass. The final status is:

`INSTRUMENT_V4_VALIDATED`

Measurement limitation retained:

`HISTORICAL_LEGITIMATE_NARROWING_REPLAY = UNAVAILABLE_WITH_PROOF`

Substitute validation:

`PRE_REGISTERED_TYPED_LEGITIMATE_NARROWING = PASS`
