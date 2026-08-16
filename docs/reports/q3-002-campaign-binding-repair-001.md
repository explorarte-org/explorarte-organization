# Q3_002_CAMPAIGN_BINDING_REPAIR_001

## Status

`Q3_002_CAMPAIGN_BINDING = VALIDATED_OFFLINE`

`Q3_002_FULL_ENTRY_GATE = NOT_RUN`

`REFORMULATED_Q3_002_EXECUTED = false`

This phase repairs only campaign selection and provenance binding. It does not
collect evidence, access the database, call a model, count capabilities, or
execute Q3-002.

## Classification of the failed attempt

The immediately preceding fail-closed run loaded the RERUN-002 campaign root,
questions, and harness. It did not load the frozen Q3-002 campaign contract.
It is therefore classified as:

`STALE_CAMPAIGN_BINDING_DETECTED`

The event is evidence that fail-closed behavior worked, but is not an attempted
measurement or entry-gate execution of Q3-002. Its model spend was `$0.00`; it
did not access the database or mutate production.

## Frozen Q3-002 identity

- mission: `ORGANIZATION-REDESIGN-001`
- question: `REFORMULATED-Q3-002`
- repair development/validation base SHA:
  `37ed48d97cfda9efaf4f660a4f626745d793107e`
- remote main SHA at campaign freeze: the same SHA
- instrument: `INSTRUMENT_V4`
- bound controller SHA256: `3547b61eba43b3aad6d14eec7313fdab1b480483c7d5d55b2368aba558066c8f`
- controller bytes: `66401`
- ontology version: `Q3_ONTOLOGY_V1`
- ontology whole-file SHA256: `0da2f727ceff769469c0ffad32c165690b3f4c1e5deac31d6e099117f9f1f974`
- ontology normative SHA256: `cbf4b72975b4a8d8c2650969181f470d7a91fbf5f35beeabd23eb66a03f6e11c`
- question-identity contract: `5d14179555b1634238ffd197bc56afa42bc5b1dc93e5605f5d2e58f368e48f9c`
- gate source-set SHA256: `5b1dd340e26a27879f30830d0b51068220b07665b0a459aaaa1f7b512d4c307a`
- gate binary SHA256: `43b25561ed645231d4c74b499a99fc8c375aeb7ee947dcd86a9470e2a467f0cd`

## Measurement-root binding

A fresh detached worktree was created at:

`/home/ubuntu/campaign-37ed48d`

The campaign manifest declares all four roots as that exact path:

- `measurement_root`
- `tool_root`
- `citation_root`
- `integrity_root`

Observed HEAD is `37ed48d97cfda9efaf4f660a4f626745d793107e` and
`git status --porcelain=v1` is empty.

The gate treats `declared_base_sha` as measurement authority. The remote-main
identity is recorded only as creation/freeze context. A later movement of main
does not invalidate a frozen campaign whose four roots remain clean and pinned
to its declared SHA.

For this repair manifest, `37ed48d...` is explicitly a development and offline
validation base. It is not the final scientific measurement base. After this
repair is reviewed and merged, the new canonical main SHA becomes the candidate
base to freeze for the separate Q3-002 campaign and full entry gate.

## State isolation

The new initial state contains exactly one question:

`set(active_questions) == {"REFORMULATED-Q3-002"}`

Its status is `UNKNOWN`. DeepSeek, Grok, and Luna counters are zero. Model
spend and database-access count are zero. Tool and refinement counters are
empty. Prior findings and dispositions are empty.

The gate rejects any occurrence of `RERUN-002`, `FALSIFY-Q1`, `FALSIFY-Q2`, or
`FALSIFY-Q4` with `STALE_CAMPAIGN_STATE_DETECTED`.

The state was constructed from the `Q3_002_INITIAL_STATE_V1` schema whitelist.
It has no historical source and was not produced by subtracting known fields
from a RERUN-002 state. The gate requires the exact twelve-field whitelist and
rejects both missing and additional fields. This prevents an unknown historical
field from surviving a cleanup-by-blacklist operation.

## Offline tests

Seven deterministic tests pass:

1. Test A, named `remote main is informational, not measurement authority`:
   frozen SHA differs from a later observed remote main while all four roots
   equal the frozen SHA -> PASS.
2. Test B: all roots resolve to `52275cad...` while Q3-002 declares
   `37ed48d...` -> `CAMPAIGN_BASE_SHA_MISMATCH`.
3. Test C: state contains historical RERUN-002 questions ->
   `STALE_CAMPAIGN_STATE_DETECTED`.
4. A non-Q3-002 campaign question ID fails closed.
5. Nonzero inherited counters fail closed.
6. Divergent configured roots fail closed.
7. Any field outside the initial-state whitelist fails closed, demonstrating
   construction from schema rather than subtraction from a historical state.

The real offline gate then returned:

`Q3_002_CAMPAIGN_BINDING_VALID`

with exactly `REFORMULATED-Q3-002`, the frozen SHA, both ontology hashes, the
question-identity contract, `INSTRUMENT_V4`, `model_calls=0`,
`database_accesses=0`, and `q3_002_executed=false`.

## Repair artifact identities

- campaign manifest: `1bb69e5778e9f6a9fde0ce7d2b9f70b226dc78afb6f4ce8b12f024fe3bb28ecd`
  (`2956` bytes)
- entry gate: `5824388c2c158652360a81144711eb72d3d52373088e31f6e36ba0ff9b705142`
  (`15192` bytes)
- entry-gate tests: `d565d4de27c85109d75f57426ff17dcaa0f4865d72f7aec4bb51cf154d9e1df3`
  (`7882` bytes)
- clean initial state: `2f41f95dfe95131bb9763909a4aaed2babce9448dbecef93e00c28e1aa17a47e`
  (`522` bytes)

## Non-actions and spend

- model calls: `0`
- model spend: `$0.00`
- database accesses: `0`
- production mutated: `false`
- ontology modified: `false`
- question-identity logic modified: `false`
- Q1/Q2/Q4 executed: `false`
- Q3-002 executed: `false`
- question expansion executed: `false`
- V2 authorized: `false`

## Closure

`Q3_002_CAMPAIGN_BINDING_REPAIR_001 = VALIDATED_OFFLINE`

The next action is a separate human-reviewed execution of the complete Q3-002
entry gate using this binding. It must not start measurement automatically.
