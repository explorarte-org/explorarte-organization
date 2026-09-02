# G4-005 — Local Fallback Architecture: Design Proposal

Status: **DESIGN ONLY, NOT AUTHORIZED TO IMPLEMENT.** Per the finding's own
`validation_required`: "A concrete local-fallback design proposal, reviewed
against this finding before any implementation begins." This document is
that proposal. It does not integrate local fallback and authorizes no code
change against `internal/modelruntime`, `internal/executive`, or
`internal/objectstorage`.

Builds directly on `reports/local-fallback-readiness.md` (Phase 23), which
classified the gap as `ARCHITECTURAL_CHANGES_REQUIRED` across exactly three
properties. This proposal addresses each in turn, then states what is
explicitly deferred.

## Gap 1 — Cross-process resume

**Problem.** `Dispatch()` is one call, one attempt, one process. If the
calling process dies mid-call, the attempt becomes `ambiguous`, and the only
recovery path (`internal/executive/ambiguity_resolution.go`) is scoped to
`pure_model` — safe only because a stateless API retry has no side effect to
duplicate. A local STRONG-profile job that runs for minutes-to-hours cannot
be "retried from scratch" safely (the original job may still be running and
will eventually produce a real result no one is waiting for) nor can it be
silently abandoned (real compute was spent).

**Proposal.** Split local dispatch into two calls instead of one:

- `Submit(ctx, req) (JobHandle, error)` — starts the job on the local node
  and returns immediately with a durable handle. Does not block for the
  job's duration.
- `Poll(ctx, JobHandle) (JobStatus, *RawResponse, error)` — checks status;
  returns the result once `JobStatus == Complete`. Can be called by ANY
  process, not just the one that called `Submit` — this is what makes resume
  possible: after an orgd/model-worker restart, whichever process picks the
  attempt back up calls `Poll` with the persisted handle instead of
  resubmitting.

This is additive: `ProviderAdapter`'s existing synchronous `Dispatch(ctx,
req) (RawResponse, error)` interface is untouched for every current adapter.
A new, optional `ResumableAdapter` interface (`Submit`/`Poll`) is implemented
only by the local adapter. `DispatchService` type-asserts for it and takes
the two-call path only for adapters that support it — gemini/xai/deepseek/
openaicompat/openairesponses are unaffected.

## Gap 2 — First-class job/attempt identity

**Problem.** Today's schema has no field for "this attempt has a durable,
external, resumable reference" — `provider_id`/`provider_model_id` alone
would have to encode a local job as a naming convention, with nowhere to
persist the handle itself.

**Proposal.** One new table, `model_local_job_handles`:

```
dispatch_attempt_id  BIGINT PRIMARY KEY REFERENCES model_dispatch_attempts(id)
node_id              TEXT NOT NULL          -- which local node accepted the job
job_handle           TEXT NOT NULL          -- the node's own resumable reference
submitted_at         TIMESTAMPTZ NOT NULL
last_polled_at       TIMESTAMPTZ
```

One row per attempt that took the `Submit`/`Poll` path. `Poll` is looked up
by `dispatch_attempt_id`, not by re-deriving anything from `RawResponse` —
this is the first-class identity the current schema is missing. No change
to `model_dispatch_attempts`/`model_invocations` themselves.

**Ambiguity-resolution's effect-class taxonomy must grow a new class.**
Today: `pure_model` (safe to retry from scratch) is the only authorized
class. This proposal adds `local_resumable`: authorized to **reattach**
(call `Poll` again), never to **resubmit** (call `Submit` again) while a
`model_local_job_handles` row exists for the attempt with no terminal
status recorded. Resubmission is a materially different, higher-risk
action (duplicate compute, possibly duplicate side effects if the local
job is not itself idempotent) and this proposal deliberately does not
authorize it automatically under any condition — a stuck job with no
terminal status after some operator-defined ceiling is a case for
`ReasonModelAuthorityViolation`-style permanent blocking (the same pattern
this session already established for AUTH-001), not an automatic retry.

## Gap 3 — Artifact transfer

**Problem.** `RawResponse.Content` is a bounded in-memory `[]byte`. A local
job producing a large generated artifact (code, a document, a dataset) has
no channel except stuffing it into that same bounded field.

**Proposal.** Extend `RawResponse` with an optional field:

```go
type RawResponse struct {
    Content          []byte           // unchanged: bounded, existing meaning
    ArtifactRef      *ArtifactRef     // new, nil for every non-local adapter
    ProviderRequestID string
    // ... existing fields unchanged
}

type ArtifactRef struct {
    ObjectKey string  // internal/objectstorage key
    Digest    string  // sha-256, verified by the caller after fetch
    Bytes     int64
}
```

`internal/objectstorage` (OCI) already exists and is proven for the RAG
corpus transfer — this proposal wires it as the transfer channel rather
than inventing a new one. The local node uploads its own artifact directly
to a job-scoped object path (credentials scoped to that single write, not
a shared write-anywhere credential) and returns the reference via `Poll`;
the calling process fetches and digest-verifies it before treating the
result as real. `Content` stays the field for anything that already fits
the existing bound — `ArtifactRef` is additive, not a replacement.

## Non-authoritative boundary (unchanged from Phase 23's own framing)

`VPS → Runtime Router → API PRIMARY → LOCAL FALLBACK`. The local node:
- never talks to Postgres directly (all durable state is written by
  orgd/model-worker, based on what `Submit`/`Poll` report);
- is reached only outbound-authenticated (the calling process authenticates
  to the node, not the reverse);
- has its object-storage write credential scoped per-job, not standing.

This proposal does not change that boundary; it only fills in what
`Submit`/`Poll`/`ArtifactRef` need to carry across it.

## Explicitly deferred (not part of this proposal)

- Choosing an actual local node runtime/model (nothing about this proposal
  is model-specific).
- The `ReasonModelAuthorityViolation`-style permanent-block ceiling for a
  `local_resumable` job that never reaches a terminal status — the shape is
  named above, the actual duration/threshold is an operator decision.
- Any change to `internal/executive`'s CEO/department-worker routing to
  ever select a local adapter — this proposal is purely about the model
  runtime's own capability to support one if routed to it.

## What would need to be true before implementation starts

1. A real local node exists to implement `Submit`/`Poll` against — this
   proposal is validated against the *interface shape*, not a running
   system, matching Phase 23's own scope.
2. Owner sign-off on this document specifically (per the finding's
   `validation_required`).
3. `local_resumable`'s reattach-only semantics are unit- and integration-
   tested the same way `pure_model`'s retry-authorization already is,
   before any real local job is ever submitted against production.
