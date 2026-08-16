# V2 content-policy kernel — integration record

Base: `c81dc9958f279b7f94c64b49ae32b8f29cc6531c`  
Branch: `v2/content-policy-kernel-001`

## Scope

The branch implements the first item in
`docs/v2/architecture-delta-v1-to-v2.md`: one deterministic content-policy
engine replacing the duplicated `dataclassifier` and `secretscan` packages.

`contentpolicy.Analyze` returns credential and clinical findings as separate
typed risks with byte offsets and audit-safe kinds. It does not choose a
universal action. Call sites preserve their boundary semantics:

- task creation rejects credentials;
- memory validates both credential and clinical admission declarations;
- RAG and PDF/RAG ingestion block credentials, while clinical vocabulary alone
  is not treated as proof that the organizational corpus contains clinical
  data;
- embedding paths fail closed before provider invocation on credential signals;
- `RedactCredentials` removes credentials only.

The broader governed-knowledge credential-assignment detector is retained in
the shared inventory. Placeholder values are excluded to preserve the existing
false-positive contract.

## Non-goals

No production mutation, migration, provider call, model call, question
expansion, Q3 rerun, controller change, or V1 measurement rewrite. This branch
does not implement later workflow/model/accounting consolidations.

## Verification

Run:

```text
go test ./internal/contentpolicy ./internal/tasks ./internal/rag ./internal/memory ./cmd/orgctl
go test ./...
go vet ./...
```

The first command is the focused regression set. The latter two are the full
offline repository gates and must be recorded before the branch is frozen.
