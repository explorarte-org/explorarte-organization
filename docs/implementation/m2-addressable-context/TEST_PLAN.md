# M2 — Addressable Context + Progressive Disclosure: TEST_PLAN

Pre-implementation test design only. No test code in this repository is
added or run by this document; this is the plan a later implementation
mission (see DESIGN.md §27 slices) must satisfy before widening scope.
Every test below is written against the domain model and contract defined
in `DESIGN.md`.

Categories A-J are exactly the mission-specified list. Within each
category, tests are grouped by which M2.x slice (DESIGN.md §27) they
belong to, so a slice can be judged "done" against its own subset without
needing the whole suite to exist first.

## A. Identity

- **A1.** Fetching the same handle twice returns byte-identical
  `ContextResource.content` and identical `content_digest`, in the same
  invocation and across two different invocations of the same snapshot.
  (M2.1/M2.2)
- **A2.** A resource with two different `source_version`s (e.g. a RAG
  document that had two versions each independently made addressable in
  two different snapshots) produces two distinct
  `context_addressable_resources` rows with distinct handles; fetching
  each returns the respective pinned content, never the other's. (M2.1)
- **A3.** A handle whose claimed `content_digest` does not match the
  digest recomputed from the currently-stored content at fetch time (i.e.
  someone tampered with the handle string, or the DB row's digest and the
  underlying content have diverged) is rejected with STALE_DRIFT, not
  silently served. (M2.2)
- **A4.** A handle with a syntactically valid shape but a `resource_id`
  that does not exist in `context_addressable_resources` for the claimed
  `context_snapshot_id` is rejected NOT_FOUND. (M2.2)
- **A5.** A handle with an internally inconsistent structured payload
  (e.g. `organization_id` field in the handle string does not match the
  organization the referenced `snapshot_id` actually belongs to in
  storage) is rejected — this must fail on the server-side re-derivation
  (I-2), not on trusting the handle's own claimed org field; assert the
  test forges a handle with a mismatched claimed org and confirms the
  *stored* org is what decides the outcome. (M2.2)
- **A6.** Two resources with identical byte content but different
  `source_reference`/`source_version` (e.g. two different memory entries
  that happen to have identical text) get distinct identities/handles —
  identity is never collapsed by content equality alone. (M2.1)

## B. Org isolation

- **B1.** A `context.fetch` call presenting a handle whose
  `context_snapshot_id` belongs to organization X, issued during an
  invocation whose own `execution_context_view`/snapshot belongs to
  organization Y, is rejected **NOT_FOUND** (corrected in round 2 — see
  B4) — even if X and Y share the same `resource_id` numeric value by
  coincidence (assert no numeric-ID collision can cross the boundary, and
  assert no content from org X is ever present in the response). The
  internal `context_disclosure_events.outcome` for this call still records
  the true cross-org reason. (M2.2/M2.3)
- **B2.** `context.search` never returns a result, snippet, or handle
  belonging to a different organization's snapshot, even when the search
  query text happens to closely match content that exists in another
  org's corpus. Construct two orgs with near-identical RAG content and
  confirm zero cross-org leakage in results or in snippet text. (M2.4)
- **B3.** `context.inspect` (list-all form) never enumerates a resource
  belonging to any snapshot other than the current invocation's own. (M2.2)
- **B4.** Guessing a plausible-looking `resource_id`/`snapshot_id` pair
  across an org boundary (sequential integer IDs are enumerable) fails
  closed — assert iterating a range of neighboring IDs against a
  foreign-org invocation returns **NOT_FOUND for every one of them, never
  FORBIDDEN and never a partial success** (corrected in independent review
  round 2: DESIGN.md §17's cross-org existence-oracle fix means a
  model/API-visible FORBIDDEN would itself leak that a given ID exists in
  some other org; only NOT_FOUND is acceptable at this boundary). A
  companion assertion checks the **internal**
  `context_disclosure_events.outcome` for the same calls: it MUST still
  record the true `forbidden_cross_org` reason (or `not_found` for a
  genuinely nonexistent ID) — the collapse to NOT_FOUND is model/API-facing
  only, never applied to the audit trail. (M2.2)
- **B5.** `context.aggregate` with a handle list mixing same-org and
  cross-org handles fails the entire call closed (no partial aggregate
  silently dropping the cross-org member) — per DESIGN.md §11's explicit
  aggregate failure rule. (M2.4)

## C. Snapshot binding

- **C1.** A resource that exists in `context_addressable_resources` for
  snapshot A is not fetchable via an invocation bound to snapshot B, even
  when A and B belong to the same organization and same actor role. (M2.2)
- **C2.** A resource that is a genuine member of the current invocation's
  snapshot is fetchable, and its provenance fields
  (`authority_tier`/`trust_class`/`data_class`/`may_grant_capabilities`)
  match exactly what was recorded at snapshot-build time. (M2.2)
- **C3.** A historical snapshot (created before some resource's underlying
  source was later modified/re-versioned) continues to serve its
  originally-pinned version when re-fetched, even after the live source
  has moved on to a newer version that a *newer* snapshot would now
  reference instead. Construct: build snapshot S1 addressing RAG chunk v1;
  update the RAG document to v2; build snapshot S2 (now addressing v2);
  confirm a fetch against S1's handle still returns v1 content, and S2's
  handle returns v2. (M2.2, requires read-path integration with `rag`)
- **C4.** `context.inspect` against a snapshot never surfaces a resource
  added to `context_addressable_resources` for a *different* snapshot
  built later against the same underlying source. (M2.2)

## D. Authority

- **D1.** A `ContextResource` returned for a RAG-evidence-kind resource
  always has `instruction_class == data` (or the DESIGN.md-defined
  equivalent) and `may_grant_capabilities == false`, and this cannot be
  overridden by content inside the fetched document itself (e.g. a
  document whose text contains an embedded fake "[authority:tier=1]"-style
  string is still delivered wrapped/escaped per the existing
  `BuildProviderRenderV2` marker scheme, never literally interpreted).
  (M2.3, requires the wrapping step)
- **D2.** Rescoped in independent review round 2 (DESIGN.md §4B: M2's
  addressable universe in this milestone is evidence/data-kind sources
  only — `SourceApprovedMemory`/`SourceRAGEvidence`/`SourceWebEvidence` —
  and explicitly excludes role profile, skill content, organization/
  department AGENT, owner constraints, canonical policy, and project/task
  instructional context). This test now asserts the *boundary itself*
  rather than a fetch-time property of excluded content: no
  `context_addressable_resources` row is ever written for a source whose
  `Kind` is not one of the three evidence kinds, even when the assembler
  omits or excerpts such a source for other reasons — assert this at the
  `Assembler.Assemble`/`Store.Create` write path (§9 step 2), not at fetch
  time, since under this design a skill/profile excerpt should never
  reach a state where it *could* be fetched dynamically at all. (Testing
  "does a dynamically-fetched skill excerpt preserve its authority_tier"
  is deferred to whatever future "addressable instructions" milestone
  might introduce a materially different authority model for
  instruction-bearing content — out of scope here.) (M2.1)
- **D3.** `data_class` (`public`/`organizational`/`sanitized`) is
  preserved unchanged through a dynamic fetch — assert a `sanitized`
  resource's fetched `ContextResource.data_class` still reads
  `sanitized`, never silently promoted to `public`. (M2.2)
- **D4.** No `context_addressable_resources` row can ever be written with
  `may_grant_capabilities = true` (DB CHECK constraint enforced at the
  schema level per DESIGN.md §6.1) — a schema-level test attempting to
  insert such a row directly must fail at the database, independent of
  any Go-level validation, mirroring the existing `context_segments` CHECK
  discipline. (M2.1)
- **D5.** A fetched resource appended to `VisibleHistory` and sent to a
  provider is never placed in the `contextcompiler`-owned stable prefix —
  assert (at the `executionharness`/`modelruntimeadapter` integration
  level) that a tool-result message containing disclosed content flows
  through the dynamic/history path, and that `ExecutionContextView`'s own
  `ProviderVisibleBytes`/digest for the snapshot is unchanged by any
  number of disclosure calls made during the run. (M2.3)

## E. Limits

- **E1.** A `context.fetch` for a resource larger than `max_fetch_bytes`
  is rejected with a clear error naming the `context.slice` alternative,
  per the frozen contract in DESIGN.md §11 — never silently truncated.
  (M2.2)
- **E2.** `context.slice` requests exceeding `max_slice_bytes` are
  rejected INVALID_REQUEST; requests with `offset+length` beyond the
  resource's actual `byte_count` are rejected INVALID_REQUEST, not
  silently clamped. (M2.2)
- **E3.** `context.search` never returns more than `max_search_results`
  entries even when far more candidates match within the snapshot's
  addressable set. (M2.4)
- **E4.** `context.search` rejects a query exceeding
  `max_search_query_bytes` as INVALID_REQUEST before any storage access
  occurs (assert no `context_disclosure_events` audit row with an
  `operational_failure`-flavored side effect from an oversized query —
  it must be a clean, cheap, pre-storage rejection). (M2.4)
- **E5.** `context.aggregate` rejects a handle list exceeding
  `max_aggregate_handles`, and rejects (or bounds) a request whose summed
  content would exceed `max_aggregate_bytes`, before performing all
  constituent reads. (M2.4)
- **E6.** `context.inspect`'s list-all form never returns more than
  `max_inspect_results` entries for a snapshot with a very large
  addressable set; assert pagination or truncation behavior is
  well-defined and documented, not an unbounded response. (M2.2)

## F. Idempotency

- **F1.** A duplicate `context.fetch` call for the same handle (e.g. the
  model or a client retries after a network hiccup) is safe: returns the
  same content both times, and produces two `context_disclosure_events`
  rows (both `outcome=ok`) rather than corrupting state or double-counting
  in a way that misrepresents what happened. (M2.2)
- **F2.** A retry of a failed disclosure call (e.g. first attempt hits
  OPERATIONAL_FAILURE because storage was briefly unavailable) followed
  by a successful retry produces two distinct audit rows with their true,
  different outcomes (`operational_failure` then `ok`) — the audit trail
  is never silently collapsed to just the final outcome. (M2.2)
- **F3.** A provider-level retry of the same model turn (the provider
  resends the same tool-call request due to a transport-level retry) that
  reaches `executionharness`'s replay guard is deduplicated at that layer
  per existing Harness behavior; if it is NOT deduplicated (replay guard
  bypassed or a different `ToolCallID` used by the retry), the resulting
  duplicate `contextdisclosure` read must still be idempotent per I-3 and
  must not corrupt telemetry aggregation into reporting a wrong dynamic
  byte/token total — assert the aggregation sums exactly what occurred,
  honestly, rather than trying to guess at deduplication. (M2.3/M2.5)

## G. Concurrency

- **G1.** Two goroutines concurrently fetching the same handle both
  succeed with identical content, and no database constraint violation or
  torn read occurs — assert via a concurrent-fetch integration test
  against a real (test) Postgres instance for the `context_disclosure_events`
  append-only insert path. (M2.2)
- **G2.** A `context.search` call racing against nothing (search never
  invalidates or mutates `context_addressable_resources`, per DESIGN.md
  §18) never observes a partially-written row — corrected in independent
  review round 2: assert the write transaction in
  `contextengine/postgres.Store.Create` (not `Assembler.Assemble`, which
  DESIGN.md §9 step 2 confirms is a pure, I/O-free function) that adds
  `context_addressable_resources` rows commits atomically with the
  `context_segments`/`context_snapshots` write it accompanies, so a search
  can never see a snapshot with segments but no addressable-resource rows
  (or vice versa) mid-build. (M2.1)
- **G3.** A deliberately tampered/contradictory binding attempt — e.g. two
  concurrent requests trying to insert conflicting
  `context_addressable_resources` rows for the same
  `(context_snapshot_id, source_reference, source_version)` — is rejected
  by the `UNIQUE` constraint (DESIGN.md §6.1), and the losing writer's
  transaction fails cleanly rather than corrupting the winning row. (M2.1)
- **G4.** If a manifest-like read (`context.inspect` list-all) races with
  the tail end of `Store.Create`'s write of addressable-resource rows for
  the same snapshot build (corrected in round 2 — see G2), the read either
  sees the fully-committed set or none of it — never a partial set —
  assert this via the same transaction boundary as G2 (there is no
  separate "manifest creation" step in this design per DESIGN.md §18, so
  this test doubles as confirmation that no such race window exists by
  construction). (M2.1)
- **G5.** Process restart between "durable tool-call-requested event
  appended" and "disclosure read executed" (mirroring
  `executionharness`'s existing crash-safety pattern) results in the run
  failing closed / being retried per the Harness's own existing recovery
  behavior, never in a `contextdisclosure` side effect being silently
  lost or duplicated in a way that corrupts the audit trail. (M2.3)

## H. Telemetry

- **H1.** `ContextTokenTelemetry`'s existing `EstimatedProviderVisibleTokens`
  (initial context) is unchanged by any number of subsequent disclosure
  calls in the same invocation — assert the M1.2 telemetry row recorded at
  invocation-input-preparation time is never mutated or re-derived after
  disclosure activity (it's immutable per the existing all-or-nothing
  CHECK, but this test asserts the *value* itself stays semantically
  "initial only," not just that the row is unmutated). (M2.5)
- **H2.** The dynamic-context aggregation (`dynamic_context_bytes`,
  `dynamic_context_estimated_tokens`, `dynamic_context_fetch_count`,
  `resources_inspected`, `resources_fetched`, `search_calls`) computed
  from `context_disclosure_events` for a given
  `requesting_model_invocation_id` (DESIGN.md §6.2 naming, round 2)
  correctly attributes each event to that invocation and no other, even
  when multiple invocations share the same underlying snapshot (e.g. a
  retried invocation against the same context) — and assert the naming
  itself is honored in the aggregation's own output/documentation (never
  presented as "tokens invocation N consumed," per DESIGN.md §6.2's
  naming note). (M2.5)
- **H3.** `context_disclosure_events.estimated_tokens` is computed with
  the same estimator identity (`EstimatorID`/`EstimatorVersion`) family as
  `ContextTokenTelemetry`, and is never mislabeled or exported anywhere as
  a provider-reported `Usage.InputTokens` figure — assert a test that
  fails if the two ever get conflated in the aggregation query or its
  output shape (naming/typing check, not just a value check). (M2.5)
- **H4.** No `context_disclosure_events` row is ever synthesized to
  reflect actual provider `Usage` — provider usage recording in
  `modelruntime`/`costgate` continues to run and record independently of
  whether any `context.*` operation occurred, and no M2 code path writes
  to or reads from provider-usage tables. (M2.5, negative/absence test)
- **H5.** Duplicate/retried disclosure calls are counted honestly in the
  aggregation (ties to F1-F3): the aggregation reports the true count of
  attempted operations including duplicates, and this is documented as
  "operations attempted," not "distinct resources the model needed" — a
  test asserting the aggregation's semantics are internally consistent
  with its own documentation. (M2.5)

## I. Historical

- **I1.** A pre-M2 `ContextSnapshot` (one with zero
  `context_addressable_resources` rows, because it predates the table)
  loads and functions normally for `context.inspect` (returns empty list,
  not an error) and for ordinary, non-M2 context assembly/rendering
  (completely unaffected — assert existing M1.x snapshot/view tests still
  pass unchanged against the same fixture data). (M2.6)
- **I2.** A pre-M2 `ExecutionContextView` is never recompiled, re-rendered,
  or otherwise mutated by anything M2 introduces — assert its
  `ProviderVisibleBytes`/digest are bit-identical before and after M2 code
  is deployed and exercised against unrelated invocations. (M2.6)
- **I3.** A historical `requesting_model_invocation_id` that predates the
  `context_disclosure_events` table produces an aggregation result that is
  explicitly `unavailable`/zero-with-an-honesty-marker (DESIGN.md §19),
  not indistinguishable from "we confirmed it made zero dynamic reads" —
  assert the aggregation's output shape actually carries this distinction
  (e.g. a boolean or enum field, not just a bare zero). (M2.6)
- **I4.** Rollback behavior: dropping the two new M2 tables
  (`context_addressable_resources`, `context_disclosure_events`) in a test
  environment leaves all M1.x tables, triggers, and existing tests
  (`go test ./internal/contextengine/...`, `./internal/contextcompiler/...`)
  fully passing and unaffected — assert via an actual down-migration dry
  run against a disposable test database (never against the shared/
  production database) that no FK from an M1.x table into an M2 table
  exists (there should be none, per DESIGN.md §20 — M2 FKs only ever point
  *from* M2 tables *into* M1.x tables, never the reverse). (M2.6)
- **I5.** Retry of the *same idempotency key* for context-build on a
  pre-M2-era-shaped request continues to produce the identical existing
  snapshot behavior (unaffected by M2) — regression test reusing the
  existing M1.1-era idempotency test fixtures unchanged. (M2.6)

## J. Operational

- **J1.** Storage unavailable (e.g. Postgres connection failure) during a
  `context.fetch` produces `outcome=operational_failure`, never
  `forbidden` or `not_found` — assert via a fault-injection test
  (mockable storage layer returning a connection error) that the failure
  category is correctly OPERATIONAL_FAILURE and that no
  `context_disclosure_events` row is misleadingly recorded as if
  authorization were evaluated and denied. (M2.2)
- **J2.** A corrupt/unreadable stored content digest (the stored bytes no
  longer hash to the recorded `content_digest` — e.g. simulated storage
  bit-rot in a test fixture) fails closed as STALE_DRIFT or
  OPERATIONAL_FAILURE (per DESIGN.md §17's classification — corrupt bytes
  that fail digest verification are STALE_DRIFT; bytes that cannot be read
  at all are OPERATIONAL_FAILURE), and is distinguishable in the audit
  trail from a legitimate action-level FORBIDDEN (see J3). (M2.2)
- **J3.** Corrected in independent review round 2 (DESIGN.md §17's
  cross-org/cross-snapshot existence-oracle fix removed content-membership
  FORBIDDEN as a model-visible outcome). Two distinct assertions:
  (a) a missing resource (valid handle shape, no matching row) and a
  resource that exists but belongs to a different org/snapshot both report
  the same model-visible **NOT_FOUND** — assert no observable difference
  in the response (timing, error text, shape) that would let a caller
  distinguish them; (b) an action-level denial (e.g. the invoking role's
  `context.search.invoke` capability itself is not granted, per §10
  boundary #1 — independent of any specific resource's existence) reports
  **FORBIDDEN**, and is model-visibly distinguishable from NOT_FOUND, since
  an action-capability denial reveals nothing about content existence. A
  third assertion covers the audit trail only: the **internal**
  `context_disclosure_events.outcome` for case (a)'s two sub-cases still
  differs (`not_found` vs the cross-org/cross-snapshot qualified
  `forbidden_*` reason), even though the model/API response did not. (M2.2)
- **J4.** `context.search` against a temporarily-unavailable index/storage
  layer reports OPERATIONAL_FAILURE, not an empty result set — this is
  the sharpest test of the "silence is not success" principle: an empty
  result set must only ever mean "genuinely no matches in the authorized
  set," never "the search backend was down," which would otherwise be
  indistinguishable to the model and to an auditor. Cross-reference
  AUDIT.md R-3 (the `rag/contextprovider` silent-denial pattern) — this
  test exists specifically because that pattern is known NOT to be safe to
  copy verbatim into the disclosure-time path. (M2.4)
- **J5.** A network/storage timeout during any `context.*` operation
  produces OPERATIONAL_FAILURE within the operation's declared timeout
  bound (DESIGN.md §16), never hangs indefinitely and never silently
  retries beyond the bound without the caller's knowledge. (M2.2/M2.4)

## Implementation-slice test ordering (cross-reference to DESIGN.md §27)

- **M2.0** (contract + domain types): no persistence-dependent tests yet;
  pure unit tests for handle encode/decode round-tripping and
  `ContextResource` shape validation belong here, ahead of A-J.
- **M2.1** (durable addressable resources): A1, A2, A6, D2 (rescoped,
  round 2), D4, G2, G3, G4.
- **M2.2** (fetch/inspect/slice + auth chain): A3, A4, A5, B1, B3, B4, C1,
  C2, C3, C4, D1 (partial, wrapping deferred to M2.3), D3, E1, E2, E6, F1,
  F2, G1, I1 (partial), J1, J2, J3, J5 (partial).
- **M2.3** (Harness tool wiring): D1 (full), D5, F3, G5. This slice is
  also where `ToolExecutionContext` (DESIGN.md §9A, round 2) is
  introduced — before any of these tests can exercise a real
  `contextdisclosure.ToolExecutor`, add a dedicated unit test asserting
  `Runtime.Execute` constructs `ToolExecutionContext.ContextSnapshotID`/
  `RequestingModelInvocationID` from `RunSpec.Context.ID`/the current
  turn's `ModelResult.InvocationRef` exactly once per turn, never per tool
  call, and never from `ToolRequest.Arguments`.
- **M2.4** (search/aggregate): B2, B5, E3, E4, E5, J4, J5 (search-specific).
- **M2.5** (telemetry): H1-H5.
- **M2.6** (integration/historical): I1-I5.

No slice widens scope until its own listed tests pass; M2.3 in particular
must not begin until M2.2's authorization-chain tests (A3-A5, B1, B3-B4,
C1-C4) are green, since M2.3 is the first point at which a real model can
reach this code path at all.
