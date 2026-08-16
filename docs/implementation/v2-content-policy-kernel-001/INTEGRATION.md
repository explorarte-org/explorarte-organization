# V2 content-policy kernel — integration record

Canonical base: `588db11599d701fb1e2ecbae19aa00828663dc2b`

Branch: `v2/content-policy-kernel-001-clean`

Scientific evidence referenced, not inherited:
`c81dc9958f279b7f94c64b49ae32b8f29cc6531c`

Superseded stacked implementation: `5cca27c962f478763d2cdf2a1406ebe94cf69ea3`

## Scope

The branch implements the first item in
`docs/v2/architecture-delta-v1-to-v2.md`: one deterministic credential-content
kernel replacing the duplicated `dataclassifier` and `secretscan` packages.

`contentpolicy.Analyze` returns credential findings with byte offsets and
audit-safe kinds. It does not choose a universal action. Call sites preserve
their boundary semantics:

- task creation rejects credentials;
- memory validates credential admission declarations and continues to reject
  an explicit upstream `DataClinical` declaration through its existing data
  contract;
- RAG and PDF/RAG ingestion block credentials, while healthcare vocabulary is
  ordinary organizational/research knowledge;
- embedding paths fail closed before provider invocation on credential signals;
- `RedactCredentials` removes credentials and preserves surrounding text.

The broader governed-knowledge credential-assignment detector is retained in
the shared inventory. A bounded set of recognizable documentation placeholders
is excluded. Prefixes such as `my-` or `your-` are not generic exemptions.

## Organization boundary decision

`explorarte-organization` is the organization, not the clinical cell. It does
not infer clinical data from words such as “patient”, “diagnosis”, or
“treatment”. Such vocabulary is valid in research, architecture, and business
knowledge and does not establish that a payload is a clinical record.

An explicit upstream classification such as `DataClinical` remains a useful
sentinel: the organization can reject a payload already classified as clinical
without trying to discover that classification itself.

**REJECTED ALTERNATIVE:** heuristically classify clinical data from clinical
terminology inside the organization.

**Reason:** clinical vocabulary is valid organizational/research knowledge and
does not prove the presence of a clinical record. Clinical-data classification
belongs to the clinical-cell boundary or an explicit upstream attestation.

## Overlap and redaction invariant

`collapse` sorts findings by start byte, longer span, then kind. It unions every
partial overlap and preserves adjacency as separate spans. Consequently,
`RedactCredentials` always consumes ordered, non-overlapping spans and cannot
construct a backward slice such as `text[100:50]`.

## Non-goals

No production mutation, migration, provider call, model call, question
expansion, Q3 rerun, controller change, or V1 measurement rewrite. This branch
does not implement later workflow/model/accounting consolidations.

The branch does not remove the historical `DataClinical` values. It removes
only vocabulary-based inference from the organization.

## Verification

Run:

```text
go test ./internal/contentpolicy ./internal/tasks ./internal/rag ./internal/memory ./cmd/orgctl
go test ./...
go vet ./...
```

The first command is the focused regression set. The latter two are the full
offline repository gates and must be recorded before the branch is frozen.

## Append-only execution evidence

1. `2026-08-16 local focused run`: contentpolicy, tasks, RAG core, memory core,
   PDF ingest, security audit, and orgctl passed. The aggregate command failed
   only in `internal/rag/bootstrap` and `internal/memory/bootstrap` because the
   local managed sandbox denied `httptest` listener creation with
   `socket: operation not permitted`. No result was rewritten to PASS.
2. A new VPS execution event must demonstrate the focused and complete gates
   in an environment that permits loopback test listeners.

## Known limitations

- Text detectors cannot find a credential that exists only in image pixels or
  other unextracted binary content.
- Precision-first regexes intentionally do not attempt generic entropy-based
  secret discovery.
- Explicit upstream data-class correctness is trusted/enforced, not inferred
  by this package.

## Follow-ups outside this slice

- The remaining V2 workflow/model/accounting/knowledge consolidations stay in
  the architecture delta; none is implemented here.
- A future harness may wrap `Analyze` as a reversible pure plugin. No plugin or
  harness lifecycle is added in this branch.
