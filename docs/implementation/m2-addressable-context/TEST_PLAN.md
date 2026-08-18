# M2 — Addressable Context + Progressive Disclosure: TEST_PLAN

Pre-implementation test design only. No test code in this repository is
added or run by this document; this is the plan a later implementation
mission (see DESIGN.md §27 slices) must satisfy before widening scope.
Every test below is written against the domain model and contract defined
in `DESIGN.md`.

Categories A-J are exactly the original mission-specified list. Round 4
adds K (search determinism) and L (plaintext durability policy) as two
further categories, explicitly flagged where they appear — both cover
contract dimensions (§12A's determinism freeze, §12B's plaintext policy)
that round 4 itself introduced and that don't fit cleanly inside any
single A-J category without distorting it. Within each category, tests
are grouped by which M2.x slice (DESIGN.md §27) they belong to, so a
slice can be judged "done" against its own subset without needing the
whole suite to exist first.

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
- **A7.** Added round 5 (P1 finding: "M2a incorrectly assumes an omitted
  `SourceRecord` has no `context_segment`," and the dependent finding that
  RAG/Memory APIs can't provide a pinned re-read anyway — DESIGN.md §6.1's
  correction), FK shape corrected round 6 (P2 finding: a bare `segment_id`
  only proves *some* segment with that id exists, not that it belongs to
  the *same snapshot* — DESIGN.md §6.1's `(context_snapshot_id,
  segment_ordinal)` composite-FK correction). For every
  `context_addressable_resources` row Assemble produces: (a)
  `segment_ordinal` is `NOT NULL` and, combined with `context_snapshot_id`,
  resolves via the composite FK to a real `context_segments` row with
  `included=false` for that same snapshot (assert the FK actually holds
  against a genuine fixture, not just that the column type allows it —
  and add a negative case: a `segment_ordinal` value that exists for a
  *different* snapshot must NOT satisfy this resource's FK); (b)
  `content_digest` equals that `context_segments` row's own
  `content_hash` exactly; (c) `context.fetch` against the resulting handle
  returns content read from `context_addressable_resources.content`
  directly — assert via a fault-injection test that disables/errors any
  `rag`/`memory` read path during the fetch call and confirms the fetch
  still succeeds unaffected (proving no live re-read ever occurs, not
  merely that the design doc says so). (M2.1/M2.2)

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
  reference instead. **Corrected round 5**: this no longer "requires
  read-path integration with `rag`" (round 2-4's premise, since dropped —
  DESIGN.md §6.1/§13's correction: content is captured durably at build
  time, never re-read). Construct: build snapshot S1 addressing RAG chunk
  v1 (durably copying v1's content into S1's own
  `context_addressable_resources.content`); update the RAG document to v2;
  build snapshot S2 (now addressing v2, with its own independently-copied
  content); confirm a fetch against S1's handle still returns v1 content
  and S2's handle returns v2 — and additionally assert this holds even
  with `rag`'s live store made completely unavailable at fetch time for
  both S1 and S2 (proving neither depends on a live read, only on what
  was captured at each snapshot's own build time). (M2.1/M2.2)
- **C4.** `context.inspect` against a snapshot never surfaces a resource
  added to `context_addressable_resources` for a *different* snapshot
  built later against the same underlying source. (M2.2)
- **C5.** Added round 3 (this is the concrete regression test for the
  headline P1: "the sealed universe isn't actually sealed"). After a
  snapshot's `context_addressable_resource_sets` seal row (DESIGN.md
  §6.1B) is committed, a direct attempt to `INSERT` an additional
  `context_addressable_resources` row for that same `context_snapshot_id`
  — bypassing the Go layer, straight at the database, simulating a bug or
  a future careless code path — is rejected by the `BEFORE INSERT`
  trigger. Assert the trigger fires (a DB-level error, not merely an
  application-level check that could be skipped), and assert the
  snapshot's addressable set (as read back via `context.inspect`) is
  identical before and after the rejected attempt. (M2.1)
- **C6.** A snapshot with `context_addressable_resources` rows but no
  matching `context_addressable_resource_sets` seal row (an
  inconsistent/corrupted state that should never occur if `Store.Create`'s
  transaction is correct, but is worth testing defensively) is treated by
  the read path as having **no** addressable universe — `context.inspect`/
  `fetch`/`search` all fail or return empty, never trusting the orphaned
  rows. Construct this state directly via a test fixture (not through
  `Store.Create`, which cannot produce it) to prove the read path checks
  for the seal row rather than assuming its existence. (M2.2)
- **C7.** A snapshot built through the real `Store.Create` transaction
  path always has exactly one seal row whose `resource_count` matches the
  actual number of `context_addressable_resources` rows for that snapshot,
  including the zero-resource case (a snapshot that omitted nothing still
  gets a seal row with `resource_count=0`) — this is also what makes
  seal-row presence a reliable "this snapshot is M2-era" signal
  independent of whether it happens to have any addressable resources
  (DESIGN.md §19). (M2.1)
- **C8.** Added round 4, **corrected round 5** (P1 finding: round 4's own
  naive `BEFORE INSERT` seal check had a race window; the row-lock-based
  fix round 4 proposed for it was itself broken, since `SELECT ... FOR
  UPDATE` does not block on a row a concurrent, still-uncommitted
  transaction hasn't made visible yet — DESIGN.md §6.1B's `pg_advisory_
  xact_lock`-based correction). This is a real integration test against
  real PostgreSQL with **explicit coordination, not two unsynchronized
  goroutines**, and it specifically exercises the timing round 4's own
  fix would have gotten wrong:
  1. `T1` begins a transaction and, as its very first statement, acquires
     the **namespaced** advisory lock production actually uses —
     `pg_advisory_xact_lock(hashtextextended('context-addressable-seal:'
     || id::text, 0))` (**corrected round 6.1**: this test previously
     described a bare `pg_advisory_xact_lock(id)` on the raw numeric ID —
     that is not what DESIGN.md §6.1B specifies as of round 6, and a test
     exercising the wrong primitive doesn't actually prove P2-2's
     namespacing fix; the test MUST use the same call production code
     does, not a simplified stand-in) — for the snapshot ID it just
     allocated — **before** inserting the `context_snapshot` row itself.
     `T1` then inserts the `context_snapshot` row and its
     `context_addressable_resources` rows, but does **not** yet insert the
     seal row or commit — the test holds `T1` open at exactly this point
     via a channel/barrier the test controls directly (e.g. the test's
     fake/instrumented store signals "resources inserted, about to seal"
     and then blocks on a channel read before proceeding). Critically,
     assert this barrier fires **before** `T1`'s `context_snapshot` row
     has committed — i.e. the row is genuinely not yet visible to any
     other transaction — this is the exact timing round 4's row-lock fix
     would have failed at silently (a `SELECT ... FOR UPDATE` on an
     invisible row simply returns zero rows and does not block).
  2. Once `T1` signals it has reached that point, the test starts `T2` in
     a second connection/goroutine, attempting to `INSERT` one additional
     `context_addressable_resources` row for the same
     `context_snapshot_id`.
  3. Assert `T2` **blocks** (does not return, does not error yet) — proven
     by asserting `T2`'s goroutine has not signaled completion after a
     short deterministic wait, not by a race-prone sleep-and-hope; a
     cleaner assertion is checking `pg_locks` (`locktype='advisory'`) for
     `T2` waiting on the same advisory lock ID `T1` holds, if the test
     harness can query it, or at minimum asserting `T2`'s completion
     channel has not fired. This assertion is what actually distinguishes
     the corrected protocol from round 4's broken one — a test that only
     checked "does `T2` eventually fail" without first confirming it
     genuinely blocked during `T1`'s uncommitted window would not have
     caught round 4's bug either.
  4. The test then lets `T1` proceed: insert the seal row, `COMMIT`.
  5. Assert `T2` **resumes** only after `T1`'s commit, and its `INSERT`
     now **fails** (the seal row `T1` just committed is visible to `T2`'s
     advisory-lock-then-check trigger).
  6. Assert the snapshot's final addressable-resource set (read back via
     `context.inspect` or a direct query) contains exactly what `T1`
     inserted — `T2`'s attempted row is absent.
  A companion assertion, **corrected in round 6** (P1 finding: the
  original wording asserted `T2`'s `INSERT` "proceeds successfully" after
  `T1` rolls back — that is wrong given the actual schema; a rolled-back
  `T1` also removes the `context_snapshot` row `T2`'s
  `context_addressable_resources` row would need to reference as its
  parent, so `T2` cannot succeed): run the same scenario but have `T1`
  **roll back** instead of committing (simulating a failed snapshot
  build). Assert the full correct chain: `T2`'s advisory lock acquisition
  unblocks once `T1`'s rollback releases it (advisory locks are
  transaction-scoped and release automatically on rollback, same as
  commit) → `T2`'s seal check correctly finds no seal (none was ever
  written) → `T2` proceeds to attempt its `INSERT` → the table's own
  `FOREIGN KEY (context_snapshot_id, organization_id) REFERENCES
  context_snapshots(id, organization_id)` **rejects** it, because `T1`'s
  rollback means no `context_snapshots` row for that ID exists at all.
  Assert specifically that this failure is an ordinary FK-violation error
  (`context_snapshot_id` not found in the parent table), NOT a
  seal-trigger rejection — the two are different failure reasons and this
  test exists to prove the advisory-lock protocol correctly reaches the
  FK check rather than deadlocking or hanging, not to prove `T2` can ever
  succeed against a build that never completed. This distinction matters
  operationally: an implementer must not "fix" this test by weakening the
  FK or by having the trigger swallow the FK's own rejection. (M2.1)

## D. Authority

- **D1.** A `ContextResource` returned for a RAG-evidence-kind resource
  always has `instruction_class == data` (or the DESIGN.md-defined
  equivalent) and `may_grant_capabilities == false`, and this cannot be
  overridden by content inside the fetched document itself (e.g. a
  document whose text contains an embedded fake "[authority:tier=1]"-style
  string is still delivered wrapped/escaped per the existing
  `BuildProviderRenderV2` marker scheme, never literally interpreted).
  (M2.3, requires the wrapping step)
- **D2.** Rescoped in independent review round 2 (DESIGN.md §4B), narrowed
  further in round 4 (DESIGN.md §6.1's source-kind decision: M2a's
  addressable universe is `SourceApprovedMemory`/`SourceRAGEvidence`
  **only** — `SourceWebEvidence` is excluded too, not merely deferred, and
  role profile, skill content, organization/department AGENT, owner
  constraints, canonical policy, and project/task instructional context
  remain excluded as before). This test asserts the *boundary itself*
  rather than a fetch-time property of excluded content: no
  `context_addressable_resources` row is ever written for a source whose
  `Kind` is not `approved_memory`/`rag_evidence`, even when the assembler
  omits such a source for other reasons — assert this at the
  `Assembler.Assemble`/`Store.Create` write path (§9 step 2), not at fetch
  time, since under this design a skill/profile/web-evidence source should
  never reach a state where it *could* be fetched dynamically at all.
  (Testing "does a dynamically-fetched skill excerpt preserve its
  authority_tier" is deferred to whatever future "addressable
  instructions" milestone might introduce a materially different
  authority model for instruction-bearing content — out of scope here.)
  (M2.1)
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
- **D4a-D4i.** Added round 4 (P1 finding: "the evidence-only contract was
  Go-level only, not a DB backstop" — DESIGN.md §6.1's CHECK constraints).
  All of the following are **direct-SQL** tests — `INSERT` statements
  issued straight against a real test-PostgreSQL `context_addressable_resources`
  table, bypassing any Go validation entirely, run **before** the target
  snapshot is sealed (so the only thing under test is the CHECK
  constraints themselves, not the seal trigger from C5/C8):
  - **D4a.** `resource_kind='role_profile'` → CHECK violation (FAIL).
  - **D4b.** `resource_kind='approved_skill'` → FAIL.
  - **D4c.** `resource_kind='task_context'` → FAIL.
  - **D4d.** `resource_kind='project_context'` → FAIL.
  - **D4e.** `resource_kind='canonical_document'` → FAIL.
  - **D4f.** `resource_kind='rag_evidence'` but `instruction_class` set to
    anything other than `'data'` (e.g. `'role_instruction'`) → FAIL.
  - **D4g.** `resource_kind='rag_evidence'` but `trust_class` set to
    anything other than `'untrusted'` (e.g. `'authoritative'`) → FAIL.
  - **D4h.** `may_grant_capabilities=true` regardless of `resource_kind`
    (this is D4 restated as part of the same systematic sweep — kept as
    its own row here for symmetry with D4a-g/i).
  - **D4i.** An internally-inconsistent tier/priority pairing (e.g.
    `authority_tier='rag_evidence'` with `authority_priority` set to
    anything other than `6`) → FAIL — proves the CHECK enforces the tier
    → priority mapping, not just each column independently.
  - **D4j.** Added round 5 (P2 finding: "`resource_kind` ↔ `authority_tier`
    pairing not DB-enforced"). `resource_kind='approved_memory'` paired
    with `authority_tier='rag_evidence'` (each individually valid per its
    own IN-list CHECK, but mutually inconsistent as a pair) → FAIL, via
    the `CHECK (authority_tier = resource_kind)` constraint (DESIGN.md
    §6.1, round 5) — proves the two columns are cross-validated, not
    merely each independently constrained to the same value set.
  - **Positive controls (must PASS, same pre-seal window):**
    `resource_kind='approved_memory'`, `authority_tier='approved_memory'`,
    `authority_priority=6`, `instruction_class='data'`,
    `trust_class='untrusted'`, `may_grant_capabilities=false` → succeeds;
    the same with `resource_kind='rag_evidence'`/`authority_tier=
    'rag_evidence'` → succeeds. Both positive controls prove D4a-i are
    failing because of the specific invalid field under test, not because
    the row is malformed some other way.
  - **Post-seal distinguishing test:** repeat one positive-control insert
    (a valid `approved_memory` row, otherwise identical to the passing
    control above) **after** the snapshot has been sealed (C5's scenario)
    — assert this now fails too, but assert (via the specific Postgres
    error/constraint name surfaced) that it fails because of the **seal
    trigger** (C5), not because it suddenly became an invalid `resource_kind`/
    tier/instruction/trust combination — i.e. confirm the test suite can
    tell "rejected for being a non-evidence kind" (D4a-i) apart from
    "rejected for arriving after the seal" (C5/C8), since both manifest as
    an `INSERT` failure but for structurally different reasons an operator
    or a future implementer needs to be able to distinguish. (M2.1)
- **D5.** A fetched resource appended to `VisibleHistory` and sent to a
  provider is never placed in the `contextcompiler`-owned stable prefix —
  assert (at the `executionharness`/`modelruntimeadapter` integration
  level) that a tool-result message containing disclosed content flows
  through the dynamic/history path, and that `ExecutionContextView`'s own
  `ProviderVisibleBytes`/digest for the snapshot is unchanged by any
  number of disclosure calls made during the run. (M2.3)
- **D6.** Added round 3 (P1 finding: "the sealed universe still allowed
  handles into the stable prefix"). `contextcompiler.ResolveProviderContext`
  produces byte-identical `StablePrefixHash`/`StablePrefixBytes` for the
  same snapshot regardless of how many addressable resources it has,
  regardless of any handle values, and regardless of whether M2 code is
  even wired up for the run — assert no code path exists by which a
  handle string or resource-set summary can reach `StablePrefix`. If a
  future milestone does send an initial handle summary, this test asserts
  it is counted in `DynamicSuffixBytes`, never `StablePrefixBytes`
  (DESIGN.md §9 step 3). (M2.0/M2.3 — this test should exist even before
  M2.3's Harness wiring, since it is really a `contextcompiler`-side
  invariant M2 must never violate, not something that requires a live
  disclosure call to check.)
- **D7.** Added round 4 (P2 finding: "the capability/action matrix was
  still illustrative" — DESIGN.md §10A's frozen matrix). Positive/negative
  controls for the `context.disclose` capability, covering all four
  operations it gates: a role granted `context.disclose` can successfully
  `context.inspect`/`fetch`/`slice`/`aggregate` an authorized resource
  (positive); a role NOT granted `context.disclose` is denied FORBIDDEN
  for all four operations, even when the specific resource requested
  genuinely exists and belongs to the current snapshot (negative — proves
  the action check runs and blocks regardless of content membership).
  (M2.2)
- **D8.** Companion to D7 for the separate `context.search` capability: a
  role granted `context.search` but NOT `context.disclose` can search but
  cannot fetch/inspect/slice/aggregate (and vice versa) — proves the two
  capabilities are genuinely independent, not aliases of each other.
  (M2.2/M2.4)
- **D9.** Denial occurs strictly before any content read (DESIGN.md §10A's
  ordering guarantee): for a role denied `context.disclose`, assert that a
  `context.fetch` call for a handle that does NOT exist in
  `context_addressable_resources` at all still returns FORBIDDEN (not
  NOT_FOUND) — if it returned NOT_FOUND, that would mean the membership
  lookup ran before the action check, which DESIGN.md §10A's ordering
  explicitly forbids (it would also reopen a content-existence oracle
  through response-timing/shape differences). (M2.2)
- **D10.** Authority-unavailable semantics (DESIGN.md §10A): when the
  `internal/authorization` evaluation for `context.disclose`/
  `context.search` cannot be reached at all (mirroring
  `executionharness.ErrAuthorityUnavailable`'s existing distinct-from-denial
  semantics), assert the operation returns neither content nor a definite
  FORBIDDEN/NOT_FOUND — it fails as an operational/retryable condition,
  and no content is ever returned in this state regardless of whether the
  requested resource would otherwise have been authorized. (M2.2)
- **D11.** No content-existence oracle through action denial (companion to
  §17's cross-org existence-oracle fix, extended to the action boundary):
  assert that a FORBIDDEN response's timing, error text, and shape are
  identical whether the requested handle would have resolved to a real,
  member resource or to a nonexistent one — an action-capability denial
  must reveal nothing about content, by construction, since it is
  evaluated first and never reaches the membership lookup (D9). (M2.2)
- **D12.** Added round 5 (P2 finding: "`ActionDigest` does not bind
  operation/arguments"). Two `context.fetch` calls against the same
  snapshot but for two DIFFERENT handles produce two DIFFERENT
  `ActionDigest` values (DESIGN.md §10A) — assert they are not
  digest-equal. Companion approval-mode test (if/when a `context.disclose`
  capability is ever configured with an approval mode in a test fixture):
  an approval consumed for one concrete action's `ActionDigest` (e.g.
  fetch handle A) MUST NOT successfully authorize a different concrete
  action (fetch handle B, or an aggregate including different handles)
  even against the same snapshot and the same capability — assert
  `ReasonApprovalScopeMismatch` (or the equivalent denial) for the
  mismatched case, mirroring `internal/authorization.Service`'s existing
  exact-match `ActionDigest` comparison. (M2.2)

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
  `max_search_query_bytes` as INVALID_REQUEST **before any resource/
  content lookup occurs** — **corrected round 6.1** (P1 finding: this
  previously said "before any storage access," which contradicted the M2
  CONTRACT's own "record an audit event for every attempt" rule; DESIGN.md
  §17A's audit-store-unavailable exception is the ONLY case where an
  attempt goes unrecorded). Assert instead: (a) NO `rag`/`memory`/
  `context_addressable_resources.content` read is ever attempted for an
  oversized query — a clean, cheap, pre-content-lookup rejection; (b) a
  `context_disclosure_events` row IS written, with `outcome=invalid_request`
  (never `operational_failure`) — the validation failure is itself a
  real, auditable attempt, exactly like any other `invalid_request`
  outcome (§17). (M2.4)
- **E5.** `context.aggregate` rejects a handle list exceeding
  `max_aggregate_handles`, and rejects (or bounds) a request whose summed
  content would exceed `max_aggregate_bytes`, before performing all
  constituent reads. (M2.4)
- **E6.** `context.inspect`'s list-all form never returns more than
  `max_inspect_results` entries for a snapshot with a very large
  addressable set; assert pagination or truncation behavior is
  well-defined and documented, not an unbounded response. (M2.2)
- **E7.** Added round 3 (P1 finding: "`context.search` has no frozen
  searchable representation" — DESIGN.md §12A introduced
  `context_addressable_resources.search_text`). A resource with a
  non-NULL `search_text` is findable by `context.search` when the query
  matches it; a resource with `search_text IS NULL` is never returned by
  `context.search` under any query, but remains listed by
  `context.inspect` and fetchable by handle via `context.fetch` — assert
  both halves of this in one test (search misses it, inspect/fetch don't)
  so a future implementation cannot silently treat "not searchable" as
  "not addressable." Also assert `search_text` itself is never returned
  as part of a `context.fetch`/`context.slice` `ContextResource.content` —
  it is a search-only excerpt, not the resource's actual content. (M2.4)
- **E8.** Added round 6 (P1 finding: "the new 1 MiB `content` CHECK can
  make `Store.Create` fail on a snapshot that builds successfully today").
  Build a snapshot containing an optional `approved_memory`/`rag_evidence`
  source whose content exceeds `max_addressable_resource_bytes` (DESIGN.md
  §6.1/§16) but is otherwise a completely ordinary oversized-and-omitted
  source (exactly today's existing `ReasonSourceTooLarge` path in
  `Assembler.Assemble`) — assert the build **succeeds** exactly as it does
  today: the source is omitted, `context_segments`/`context_snapshots`
  behave unchanged, and critically **no** `context_addressable_resources`
  row is created for it (never a `Store.Create` failure, never a
  truncated/partial row). **Companion test, corrected round 6.1** (P1
  finding: "which resources win when the aggregate budget binds" was left
  as "a defined, documented order — e.g. assembly order," not actually
  normative): build a snapshot where several individually-eligible
  omitted sources would collectively exceed
  `max_addressable_total_bytes_per_snapshot` — assert the ones that
  become addressable are **exactly** those that sort earliest under
  `Assemble`'s own existing deterministic order (ascending `Segment.
  Ordinal`, i.e. the same `sort.SliceStable` by
  `(AuthorityTier rank, Reference, Version, ContentHash)` `Assemble`
  already applies — DESIGN.md §6.1's round-6.1 correction), not merely "a
  consistent order" or "an order the implementer chose" — construct a
  fixture where the expected winners are unambiguous from that specific
  ordering and assert the exact set, then rerun the same build twice and
  assert byte-identical results both times (determinism, not just a
  documented policy). The remainder fall back to ordinary non-addressable
  omission, again without failing the build. A third assertion:
  `context_snapshots.omitted_segment_count` counts BOTH kinds of omission
  (size-ineligible and otherwise) identically — it is not a proxy for "how
  many resources are addressable," and `context.inspect`'s count is
  expected to be less than or equal to it, never asserted equal. A fourth
  assertion (round 6.1, companion to the config-upper-bound fix):
  `max_addressable_resource_bytes` configured above 1 MiB is rejected as
  invalid configuration at load time — assert this is caught before any
  snapshot build is even attempted, never surfacing as a later
  `Store.Create` CHECK-constraint failure. (M2.1)

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
  appended" and "disclosure read executed." **Corrected round 6.1**
  (DESIGN.md §18's round-6 correction: this previously said "failing
  closed / being retried," which is not what `executionharness` actually
  does). Assert the exact, specific behavior: on resume,
  `Runtime.Execute` detects the unresolved `tool_call_requested` event and
  terminates the run with `StatusIndeterminateToolExecution` — assert
  this specific terminal status, and assert `contextdisclosure`'s own
  tool executor is NOT re-invoked automatically (no automatic
  re-execution) — never a generic "retried" claim. The test should also
  assert no `contextdisclosure` side effect is silently lost or
  duplicated by this terminal path (it has none to worry about, §18), and
  that recovery/retry, if it happens at all, is visibly a decision made
  by whatever higher layer manages runs, not something this test should
  expect `executionharness` itself to do automatically. (M2.3)
- **G6.** Added round 3 (P1 finding: "`ToolExecutionContext` coupled a
  generic Harness to Context/ModelRuntime IDs" — the corrected design
  moves interpretation into `contextdisclosure.BindingResolver`). Two
  assertions: (a) `Runtime.Execute` constructs
  `ToolExecutionContext{InitialContextRef: spec.Context.ID,
  RequestingInvocationRef: modelResult.InvocationRef}` exactly once per
  turn (never per tool call, never parsed/interpreted by
  `executionharness` itself — assert via a fake `ToolExecutor` that
  records what it received and confirms the strings are passed through
  unparsed); (b) `contextdisclosure.BindingResolver.Resolve` correctly
  parses and DB-verifies a genuine ref pair into a `ResolvedBinding`, and
  fails closed (not a panic, not a zero-value binding silently accepted)
  on a malformed ref, a ref for a nonexistent snapshot/invocation, or a
  ref pair where the invocation does not actually reference the claimed
  snapshot (mirroring `modelruntimeadapter.Adapter`'s existing
  `validateCreatedInvocation`/`validateExistingInvocation` cross-checks).
  (M2.2/M2.3)

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
- **H3.** `context_disclosure_events.disclosure_estimated_tokens`
  (corrected field name, round 3 — this test still named the pre-round-2
  column) is computed with the same estimator identity
  (`EstimatorID`/`EstimatorVersion`) family as `ContextTokenTelemetry`,
  and is never mislabeled or exported anywhere as a provider-reported
  `Usage.InputTokens` figure — assert a test that fails if the two ever
  get conflated in the aggregation query or its output shape
  (naming/typing check, not just a value check). (M2.5)
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
- **I4.** Rollback behavior: this is SCHEMA DOWN specifically (DESIGN.md
  §21, round 3 — distinct from the preferred APPLICATION ROLLBACK, which
  has no schema to test since it changes nothing durable). Dropping the
  three new M2 tables (`context_addressable_resources`,
  `context_addressable_resource_sets` — round 3, §6.1B —,
  `context_disclosure_events`) in a test environment leaves all M1.x
  tables, triggers, and existing tests
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
  `context.search` capability itself is not granted, per §10A — independent
  of any specific resource's existence) reports
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
- **J6.** Added round 3 (P2 finding: "record every attempt" is impossible
  when the audit store itself is down — DESIGN.md §17A's three-way split).
  Case 1: a `context_addressable_resources.content` read fails (round 6 —
  never `rag`/`memory`, which no longer participates at fetch time at all,
  §13) but the audit DB is healthy — assert a `context_disclosure_events`
  row IS written with `outcome=operational_failure`, exactly as J1 already
  covers. Case 2: the audit DB itself (the same Postgres
  `context_disclosure_events` lives in) is unavailable — assert
  `contextdisclosure` fails closed (no content returned to the model,
  regardless of whether the underlying content read would have succeeded)
  and does NOT attempt or claim to guarantee an audit row was written.
  Case 3: both unavailable — same fail-closed assertion as case 2. (M2.2)
- **J7.** Added round 3, companion to J6: no test in this suite (and no
  code path in the design) may ever assert or rely on "a
  `context_disclosure_events` row exists" as a precondition for "content
  was NOT returned" — i.e. fail-closed behavior in J6 case 2/3 must not
  itself depend on successfully writing the audit row first; assert the
  fail-closed check happens even when the audit write is what's failing,
  not only when a separate content read fails. (M2.2)

## K. Search determinism (round 4 addition — beyond the original A-J list)

> The mission-specified categories were A-J; round 4's P2 finding
> ("`context.search` retains two incompatible determinism contracts")
> introduces a genuinely new test dimension — determinism of a pure
> function's output — that doesn't fit cleanly inside any of A-J without
> distorting one of them. Added explicitly as its own category rather than
> silently folded into E (Limits) or G (Concurrency), so the determinism
> contract (DESIGN.md §12A) has dedicated, visible test coverage.

- **K1.** The same query issued 100 times against the same sealed
  snapshot, same `search_algorithm_id`/`search_algorithm_version`,
  produces byte-identical `returned_resource_ids` (order included), scores,
  and snippets on every single call — a real repeated-call test, not a
  single assertion. (M2.4)
- **K2.** Two snapshots built with `context_addressable_resources` rows
  inserted in deliberately different orders (e.g. reverse-insert one
  relative to the other, achieved by constructing the `Assembly` with
  sources in a different input order — DESIGN.md §12A explicitly forbids
  relying on unspecified row ordering) but otherwise identical content
  produce the same `context.search` result order for the same query —
  proves the ranking function doesn't accidentally depend on physical
  insertion/storage order. (M2.4)
- **K3.** A deliberately constructed tie (two resources with identical
  `score` for a given query) resolves via exactly the frozen tie-break —
  `source_reference ASC, resource_id ASC` — no other ordering is a valid
  implementation (DESIGN.md §12A, round 5: no per-implementer discretion)
  — assert the tie-break is exercised and produces exactly this order,
  not merely "a stable one."
  (M2.4)
- **K4.** Process/connection restart between two `context.search` calls
  against the same sealed snapshot with the same query produces the same
  result order — proves determinism doesn't depend on any in-memory cache
  or connection-local state. (M2.4)
- **K5.** `search_algorithm_id`/`search_algorithm_version` are recorded on
  every `context_disclosure_events{operation:"search"}` row and are
  queryable/comparable across historical rows — a test asserting the
  columns are actually populated and not silently NULL/omitted. (M2.4/M2.5)
- **K6.** Different query bytes are permitted to (and, for a
  content-varying corpus, generally will) produce a different result order
  than a different query — this is the expected, non-buggy case; the test
  asserts determinism is about *repeatability for the same input*, not
  about all queries converging to one order. (M2.4)
- **K7.** A resource that is a genuine member of a DIFFERENT snapshot (not
  the current sealed one) is never considered as a `context.search`
  candidate, regardless of query — restates C1/B2's membership guarantee
  specifically in the context of the ranking function's candidate set,
  since K2's insertion-order test could otherwise be misread as implying
  cross-snapshot rows are ever visible to the ranking function. (M2.4)
- **K8.** A resource with `search_text IS NULL` is never present in
  `returned_resource_ids` for any query, including an empty/wildcard-like
  query — restates E7 specifically as a determinism-suite regression
  guard (a future ranking-algorithm change must not accidentally start
  scoring NULL `search_text` as an empty-string match). (M2.4)
- **K9.** No `context.search` call, under any of K1-K8's scenarios, issues
  a live query against `rag`/`memory` storage — assert via a test double
  for those subsystems' read paths that records zero calls during any
  `context.search` invocation (restates §12's live-corpus prohibition as
  a concrete, mockable-dependency test). (M2.4)

## L. Plaintext durability policy (round 4 addition — beyond the original
A-J list)

> Same rationale as category K — DESIGN.md §12B's plaintext policy
> (search_text classification/retention, query digest-only persistence)
> is a genuinely new dimension, given its own category rather than forced
> into an existing one.

- **L1.** A `context.search` query containing a credential-like string
  (e.g. a fixture string matching `internal/contentpolicy.Analyze`'s own
  detection patterns) never appears in `context_disclosure_events` as
  recoverable plaintext — assert the row contains only `query_digest`/
  `query_byte_count`, never the raw bytes. **Corrected round 5** (P1
  finding: the raw query is already durably persisted by
  `executionharness`'s pre-existing `ExecutionHistoryStore` tool-call
  event log, independent of M2 — DESIGN.md §12B.2's correction) — this
  test's scope is `context_disclosure_events` specifically, not "nowhere
  in the system"; it MUST NOT assert or imply that the raw query is
  absent from `ExecutionHistoryStore`'s own tool-call-requested events,
  which is out of scope for M2 to change and would be a false assertion
  if made. (M2.2/M2.4)
- **L2.** `query_digest` is stable: the same query bytes always produce
  the same digest (sha256), and two different queries (even differing by
  one byte) produce different digests — a basic correctness check on the
  digest computation itself. (M2.2)
- **L3.** A query exceeding `max_search_query_bytes` is rejected
  INVALID_REQUEST. **Corrected round 6.1** (ties to E4's same correction —
  this previously asserted "no `context_disclosure_events` row at all is
  written," which contradicted the M2 CONTRACT's own audit-every-attempt
  rule). Assert instead: a `context_disclosure_events` row IS written,
  with `outcome=invalid_request` — computing `query_digest`/
  `query_byte_count` from the oversized bytes before rejecting is cheap
  hashing, not a "resource/content lookup" in the sense E4 forbids, so
  this test does not require the row to omit them; what it DOES require
  is that no `rag`/`memory`/`context_addressable_resources.content` read
  is ever attempted, and the row's `outcome` is `invalid_request`, never
  `operational_failure` or a bare absence of any row. (M2.4)
- **L4.** `search_text` computed from a `SourceRecord` that itself would
  have been rejected by upstream content policy (i.e. the underlying
  `rag`/`memory` ingestion path already refuses to persist a record
  containing a raw credential, per `internal/contentpolicy`'s existing
  integration in those packages) cannot exist in the first place — assert
  by construction (no `SourceRecord` with policy-rejected content ever
  reaches `Assemble`) rather than by adding a second redaction pass inside
  M2a itself; this test documents and confirms that M2a correctly relies
  on upstream policy rather than re-implementing it. (M2.1)
- **L5.** No `orgctl`/audit-inspection surface built on
  `context_disclosure_events` ever exposes a raw `query_text`-shaped field
  — a schema/API-shape test asserting the read model never resurrects the
  round-3 `query_text` column name or an equivalent, only
  `query_digest`/`query_byte_count`. (M2.5)
- **L6.** `search_text`'s `data_class` inheritance (DESIGN.md §12B.1): a
  `sanitized`-classified `context_addressable_resources` row's
  `search_text`, when surfaced through `context.search`'s snippet output,
  is still treated as `sanitized`-tier by anything consuming it — assert
  the `ContextResource`/`SearchResult` shape carries `data_class` alongside
  the snippet, never dropping it. (M2.4) **IMPLEMENTATION ERRATA (M2.0
  round 8):** already closed at the M2.0 layer —
  `internal/contextdisclosure.SearchResult` carries `data_class` per
  DESIGN.md's own errata note on `context.search`'s OUTPUT line; M2.4
  inherits an already-compliant shape, not a gap.

## M. Harness tool-result framing (round 6 addition — beyond the original
A-J list)

> DESIGN.md §9C's headline P1 finding: a literal `return err` from
> `contextdisclosure.ToolExecutor.Execute` for a well-defined M2 outcome
> (FORBIDDEN, NOT_FOUND, etc.) would silently kill the entire Harness run
> instead of letting the model see and react to it. This category proves
> the corrected framing on both sides of the `error == nil` boundary.

- **M1.** Every outcome DESIGN.md §17 enumerates (`ok`, `invalid_request`,
  `not_found`, `forbidden`, `stale_drift`, `operational_failure`) is
  returned from `contextdisclosure.ToolExecutor.Execute` as
  `ToolExecutionResult{Content: <JSON ContextToolResult>}` with a **nil**
  `error`, for each of `context.inspect`/`fetch`/`slice`/`search`/
  `aggregate` — a table-driven test asserting `err == nil` and
  `ContextToolResult.Code` matches the expected string, for every
  (operation, outcome) pair that's reachable. (M2.2/M2.3)
- **M2.** Given a `nil`-error `ContextToolResult` with `Code=="forbidden"`
  (or any other non-`"ok"` code), assert `executionharness.Runtime.Execute`
  behaves exactly as it does for a successful tool call: the result is
  recorded via `EventToolResultRecorded` (never `EventRunFailed`), the run
  does NOT terminate, and the model's next turn sees the structured
  `ContextToolResult` in `VisibleHistory` and can issue a further
  `context.*` call in the same run. This is the test that would have
  caught round 5's gap: assert specifically that the run's final status
  is NOT `StatusToolError` for any of these cases. (M2.3)
- **M3.** A genuinely terminal executor failure (e.g. `ToolExecutionResult.Content`
  fails to marshal, or an injected programming-invariant violation) DOES
  return a non-`nil` `error` from `Execute`, and DOES correctly terminate
  the run via `EventRunFailed`/`StatusToolError` — proving the narrow
  remaining `error != nil` case still exists and still behaves as
  `executionharness` already expects, so the fix doesn't overcorrect into
  "no context.* error path ever fails the run at all." (M2.3)

## Implementation-slice test ordering (cross-reference to DESIGN.md §27)

- **M2.0** (contract + domain types): no persistence-dependent tests yet;
  pure unit tests for handle encode/decode round-tripping and
  `ContextResource` shape validation belong here, ahead of A-J. **D6**
  (round 3) also belongs here, or as early in M2.3 as possible — it is a
  `contextcompiler`-side invariant check, not dependent on any M2
  persistence existing yet.
- **M2.1** (durable addressable resources): A1, A2, A6, A7 (round 5/6 —
  segment-ordinal FK / no-live-read), C5, C6, C7, C8 (round 4, corrected
  round 6 — the seal-immutability/advisory-lock-then-check concurrency
  test, including the corrected rollback-produces-FK-failure assertion),
  D2 (rescoped, round 2/4), D4, D4a-j (round 4/6 — DB CHECK sweep, D4j
  added round 6), E8 (round 6 — oversized-omitted-source eligibility
  fallback), G2, G3, G4, L4 (round 4 — upstream-content-policy reliance).
- **M2.2** (fetch/inspect/slice + auth chain): A3, A4, A5, B1, B3, B4, C1,
  C2, C3 (revised round 6 — no rag read-path dependency), C4, D1 (partial,
  wrapping deferred to M2.3), D3, D7, D8, D9, D10, D11, D12 (round 6 —
  `ActionDigest` distinctness) (capability matrix, round 4/6), E1, E2, E6,
  F1, F2, G1, G6 (round 3 — `BindingResolver`), I1 (partial), J1, J2, J3,
  J5 (partial), J6, J7 (round 3, revised round 6 — audit-store-unavailable
  split, no longer referencing a live rag/memory read), L1 (revised round
  6 — scoped correctly, no system-wide-absence claim), L2, L3 (round 4 —
  query digest persistence, partial: L3's oversized-query rejection).
- **M2.3** (Harness tool wiring): D1 (full), D5, D6 (if not already run in
  M2.0), F3, G5, **M1, M2, M3 (round 6 — `ContextToolResult` framing; M2
  in particular is the direct regression test for round 6's headline P1
  and MUST be green before this slice is considered done)**. This slice
  is also where `ToolExecutionContext` (DESIGN.md §9A, revised round 3 —
  opaque refs, not typed IDs) is introduced — G6 (M2.2, above) already
  covers the construction/resolution contract; this slice's own tests
  focus on end-to-end wiring through a real `contextdisclosure.ToolExecutor`
  and the mechanical `executiveToolExecutor{}` signature update (round 3
  factual correction — compiles, never reached, no behavior change).
- **M2.4** (search/aggregate): B2, B5, E3, E4, E5, E7 (round 3 —
  `search_text`), J4, J5 (search-specific), K1-K9 (round 4/5 —
  determinism suite, fully frozen tie-break per K3's round-5 revision),
  L3 (query-size rejection, search-specific), L6 (round 4 —
  `search_text` data-class inheritance through search output). Also
  includes the `context.aggregate` canonical-order test implied by D12's
  `ActionDigest` distinctness (round 6, DESIGN.md §11's aggregate
  correction) — same-set-different-request-order producing byte-identical
  concatenated output.
- **M2.5** (telemetry): H1-H5, K5 (search-algorithm-version auditability,
  partial), L5 (round 4 — no raw query text in any inspection surface).
- **M2.6** (integration/historical): I1-I5.

No slice widens scope until its own listed tests pass; M2.3 in particular
must not begin until M2.2's authorization-chain tests (A3-A5, B1, B3-B4,
C1-C4, G6) are green, since M2.3 is the first point at which a real model
can reach this code path at all — and M2.3 itself must not be considered
done until M2 (round 6) passes, since that is what proves the model
actually receives M2's documented outcomes instead of the run silently
dying.
