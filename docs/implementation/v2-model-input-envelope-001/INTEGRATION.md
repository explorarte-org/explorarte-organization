# V2 Model Input Envelope 001

## Identity

- Canonical base: `bd9becf0902ed142ead26c62c5011b8f20fd4e95`
- Branch: `v2/model-input-envelope-001`
- Slice: `V2_MODEL_INPUT_ENVELOPE_001`
- Architecture decision: `EXTEND_MODEL_RUNTIME_WITH_DURABLE_MODEL_INPUT_ENVELOPE`
- Production mutation: none
- Provider/model calls during implementation: zero
- Database access: PostgreSQL 17 disposable test database only

## Scope

This slice adds the durable, provider-independent input for one Model Runtime
invocation. It does not connect the Execution Harness to Model Runtime yet.
That adapter remains the next slice.

The three durable concepts remain distinct:

1. `ContextSnapshot`: canonical authorized task context.
2. `ModelInputEnvelope`: exactly one invocation's canonical cognitive input.
3. Harness history: the complete append-only execution trajectory (not stored
   by this slice).

One Harness turn will map to one Model Runtime invocation and one immutable
input envelope. A context snapshot is not recreated per turn.

## Contract

`ModelInputEnvelope` contains:

- schema version and context snapshot identity;
- the upstream canonical projection digest;
- stable-prefix messages and digest;
- append-only visible assistant/tool history for the invocation;
- immutable provider-visible tool definitions;
- an optional opaque provider continuation reference;
- effective input classifications and their digest.

The stable-prefix digest binds both prefix messages and tool definitions.
Appending visible history does not change it; changing the action space does.

Legacy single-shot callers receive a canonical one-message envelope at
invocation creation. Dispatch has no legacy path that can omit the durable
input record.

## Persistence and provenance

Migration `000049_create_model_invocation_inputs` adds a 1:1 input record for
every invocation created after the migration:

- `invocation_id` and `context_snapshot_id` are jointly bound to the owning
  invocation;
- canonical bytes are stored as `BYTEA`, without JSONB reserialization;
- canonical, projection, stable-prefix, and classification digests are stored;
- database triggers reject `UPDATE` and `DELETE`;
- invocation creation and input insertion occur in one transaction;
- idempotent reuse verifies the same input digest;
- requested/reused audit events expose only schema/digest provenance, never
  input bytes.

Installation fails if nonterminal historical model invocations exist. Terminal
historical invocations are deliberately not given reconstructed input records:
their exact historical model-visible bytes cannot be recovered honestly.

The envelope digest participates in `Invocation.RequestHash`; `ActionDigest`
therefore binds it transitively. Provider request evidence additionally binds
the envelope schema, envelope digest, stable-prefix digest, classification
digest, and adapter version.

## Egress safety

Egress now evaluates the union of:

- current `ContextSnapshot` data classes;
- durable envelope classifications;
- deterministic Content Policy analysis of every dynamic model-visible
  message, tool result, tool arguments, tool definition, and continuation
  reference;
- deterministic Content Policy analysis of a provider-visible output schema.

A credential introduced by a tool/model result classifies the complete input
as `secret`. `InvocationService.Create` rejects that input before
`Store.CreateInvocation`, so neither an invocation nor canonical input bytes
are persisted. The rejection message contains no matched material and directs
the caller to store credentials in the secret store and pass a reference.

Dispatch independently retains the existing hard-deny path before context
rendering, cost reservation, or provider dispatch. This is defense in depth
for corrupt or externally injected durable records, not the normal admission
path. Tests assert zero provider calls in both cases.

No clinical vocabulary heuristic was introduced. Ordinary words such as
`patient` remain ordinary organizational knowledge. Explicit upstream
`clinical` classification remains enforceable.

## Provider translation

`CanonicalRequest` carries the validated structured envelope. Adapter version
2 translates it as follows:

- DeepSeek, OpenAI-compatible, Gemini compatibility, and MiMo adapters render
  native chat messages, assistant tool calls, tool results, and tool
  definitions.
- OpenAI Responses renders message/function-call/function-result input items
  plus function tool definitions.
- Alibaba Claude CLI deterministically renders non-tool multi-turn messages;
  tool-bearing or continuation-bearing input fails closed because that bounded
  CLI profile explicitly disables tools.
- The deterministic fake adapter hashes the complete canonical envelope.

Provider-returned tool call IDs are preserved through normalization. Invalid
or duplicate non-empty IDs are rejected.

Current adapters reject non-empty opaque continuation references until an
adapter has a verified native continuity contract. They never discard the
reference silently.

## Rejected alternatives

### ContextSnapshot per turn

Rejected because visible assistant/tool history is invocation input, not
canonical task knowledge. Recreating snapshots would duplicate execution
history inside Context Assembly.

### Direct Harness-to-provider dispatch

Rejected because it bypasses assignment, execution principal, egress,
invocation persistence, cost reservation, usage settlement, and provider
outcome evidence.

### JSONB as canonical evidence

Rejected because PostgreSQL reserialization cannot prove preservation of the
exact canonical representation that was hashed. Canonical bytes are `BYTEA`;
queryable metadata is stored separately and checked against those bytes.

### Store Harness history in model_invocation_inputs

Rejected because the table records one invocation's input, not the Run's
complete execution trajectory. Harness history remains a separate append-only
boundary.

### Reconstruct historical inputs during migration

Rejected because downstream context segments and hashes do not prove the exact
provider-visible bytes of completed historical invocations. Golden History
forbids manufacturing that evidence retroactively.

## Chronological implementation evidence

The following sequence is intentionally append-only.

1. Base/worktree gate: `HEAD` and merge-base both equaled `bd9becf...`; status
   was clean.
2. Initial focused run (`go test ./internal/modelruntime/... ./migrations/...`)
   failed compilation because the new file referenced a package-local digest
   regexp that did not exist. Adapter tests also showed the sandbox's expected
   loopback-socket denial.
3. A local digest validator was added. The next compile exposed every stale
   `invocationRequestHash` call and the missing fake-store input method.
4. After those fixes, core tests failed because the first implementation
   rendered context before authorization/egress/adapter gates. Existing tests
   correctly detected the ordering regression.
5. Stored-envelope validation was split from byte-render verification. Digest,
   classification, and egress checks now happen without rendering; byte-exact
   context verification remains after authorization, egress, and adapter
   preflight. Core tests passed.
6. Focused envelope, dynamic-secret, provider-translation, migration, and
   normalization tests passed.
7. `go test ./... -run '^$'` and `go vet ./...` passed, proving repository-wide
   compile/static compatibility.
8. `go test ./...` was rerun with loopback access for local `httptest` servers
   and passed repository-wide. It made no live provider calls.
9. The integration-tagged PostgreSQL package compiled with
   `go test -tags=integration ./internal/modelruntime/postgres -run '^$'`.
   No disposable database URL was available, so no database was accessed.
10. `make verify` stopped at the repository's pre-existing `fmt-check` debt.
    The clean base reported ten files; this branch reported the same set except
    for `internal/modelruntime/adapter/mimo/adapter.go`, which this slice
    legitimately modified and formatted. The remaining nine files are
    untouched. Therefore `changed_files_causing_failure=0`; the result is
    `BASELINE_BLOCKED`, not a passing verification claim.
11. The model-runtime fitness gate reported the same three MiMo HTTP-client
    paths on the clean base and this branch. The egress, dispatch, identity,
    and provider fitness gates likewise reproduced the base's existing
    canonical-hash failures byte-for-byte. These gates remain
    `BASELINE_BLOCKED`. They were run separately so their failures did not hide
    the build result.
12. `make build` passed for both `orgd` and `orgctl`. No provider, production,
    shared database, or disposable database was contacted.
13. Human review identified that the first implementation classified dynamic
    credentials but persisted the canonical envelope before Dispatch denied
    egress. This was accepted as a blocking at-rest admission defect.
14. `InvocationService.Create` was changed to reject effective `secret`
    classifications before calling the store. Unit negatives for assistant
    content, tool results, tool-call arguments, tool schemas, explicit secret
    classification, and provider-visible output schema passed. The artificial
    stored-secret Dispatch denial remained passing.
15. The first PostgreSQL runtime attempt on the local host stopped at
    `postgres-healthy`: the installed Docker client has no Compose plugin.
    Isolation and destructive-authorization preconditions passed; the result
    was recorded as `INFRASTRUCTURE_ABORT`, not a database-test result.
16. The same committed bytes were replayed in an isolated VPS worktree with
    Docker Compose and PostgreSQL 17. All safety/bootstrap preconditions
    passed, then the integration-tagged package failed to compile because the
    new zero-row assertions called a one-argument helper with two SQL
    parameters.
17. The assertions were corrected to use the existing two-argument helper.
    The next isolated PostgreSQL 17 replay compiled and ran, then correctly
    exposed a migration reversibility gap: migration 49's FK prevented the
    test from dropping migration 7's `model_invocations` table.
18. The down/reapply test was corrected to roll back migration 49 before 7
    and to verify `model_invocation_inputs` after ordered reapplication. No
    `CASCADE` or integrity weakening was introduced.
19. The third isolated replay on commit
    `8ff221e890b39bc0f7861943c08697ae7ffb84af` passed all harness
    preconditions and `modelruntime-postgres`. Evidence manifest SHA-256 was
    `443dcc8d31fc9c90c07246eefef96e983c0ef372937f3458e38facf52d5e8e97`.
    Evidence accounting was
    `31/31`, with the selected suite passing, 30 non-selected suites explicitly
    skipped, zero failed/blocked/unknown, and final status `COMPLETE_GREEN`.
    This run exercised real durable insert, immutable UPDATE/DELETE denial,
    idempotent reuse, dispatch, secret-admission zero rows, rollback, and
    reapplication against the disposable database `explorarte_test`.
20. `go test ./...` and `go vet ./...` passed after the PostgreSQL fixes.
    No live provider or production resource was contacted.

## Known gaps

- `V2_HARNESS_MODEL_RUNTIME_INTEGRATION_001` must translate Harness
  `NormalizedModelRequest` into this envelope and create/dispatch one
  invocation per turn.
- The migration was not applied to production or any shared database.
- Opaque provider continuation is durable but unsupported by current adapter
  transports; non-empty values fail closed.
- Provider wire bytes remain adapter-derived. The provider-independent
  canonical envelope bytes are reproducible and durable; no claim is made that
  all provider wire encodings are byte-identical across adapter builds.

## Follow-ups

1. Implement the narrow Harness `ModelExecutor` adapter using this envelope.
2. Define a verified provider-specific continuation contract before enabling
   opaque continuation for any adapter.
