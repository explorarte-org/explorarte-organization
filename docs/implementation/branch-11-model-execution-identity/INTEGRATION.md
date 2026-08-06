# Branch 11 — Cryptographic model execution identity

## Base and scope

Branch 11 is based exactly on `f1a9ffacd8b3401ed56fd4e95ba95520b28b025a`
(`feat(models): add durable dispatcher assignments (#6)`). It replaces the
Branch-10 trust assumption that knowing `ORG_MODEL_EXECUTION_PRINCIPAL_KEY` is
enough to act as an execution principal. The variable remains only a local,
non-secret locator. Dispatch authority now also requires an Ed25519 assertion
created with a private key whose public key is durably registered for the
principal pinned to the invocation.

This branch does not add a provider adapter, provider credentials, HTTP, a
persistent worker, polling, scheduling, tool execution, memory, RAG, skills, or
self-improvement. `FakeAdapter` remains the only executable adapter and all real
providers remain unavailable and dispatch-disabled.

## Trust boundary

The identity flow is:

1. A new invocation pins an immutable execution-identity policy version and
   canonical hash in addition to its dispatcher assignment and execution
   principal.
2. The local process resolves its principal with the non-secret
   `ORG_MODEL_EXECUTION_PRINCIPAL_KEY` locator.
3. The process loads an Ed25519 private key from
   `ORG_MODEL_EXECUTION_IDENTITY_KEY_FILE`. On POSIX systems the file must be a
   regular PKCS#8 PEM file without group or other permissions (normally `0600`).
4. The public-key fingerprint must resolve to an active durable key belonging
   to the invocation's pinned principal.
5. PostgreSQL issues a short-lived, one-use challenge bound to organization,
   organization revision, invocation, dispatcher assignment, execution
   principal, policy version/hash, key, action digest and request hash.
6. The process signs the canonical challenge payload.
7. The application verifies the signature as an early check. The PostgreSQL
   claim transaction then independently rebuilds the canonical payload from
   durable state and verifies the Ed25519 signature again. An internal caller
   cannot bypass the boundary by fabricating a pre-verified assertion object.
8. Challenge consumption, assertion persistence and the principal-bound claim
   are committed atomically.
9. The existing authorization, egress and assignment quota checks still apply.
   At pre-send, PostgreSQL locks and revalidates the assertion and key together
   with the invocation and attempt before assignment consumption and
   `send_started`.

A valid signature proves possession of the private key for this one dispatch
attempt. It does not grant `model.invoke`, alter egress policy, choose a model or
provider, change the task assignment, extend a lease, increase an assignment
quota, or enable a provider.

## Canonical policy

`docs/canonical/model-execution-identity-policy.yaml` is strict, versioned and
default-deny. The initial algorithm is fixed to Ed25519. The canonical policy
controls the challenge TTL and bounded clock skew. Unknown YAML fields,
duplicate keys, aliases, multiple documents, non-deny defaults and unknown
algorithms are rejected.

New invocations resolve the active materialized policy and pin its database ID
and canonical hash. Existing pinned invocations may continue to use their
immutable superseded policy version; unpinned legacy invocations are readable
but cannot be dispatched and return `execution_identity_unpinned` before an
adapter call.

## Durable schema

Migration `000010_create_model_execution_identity` adds:

- `model_execution_identity_policy_versions`
- `model_execution_identity_keys`
- `model_execution_identity_challenges`
- `model_execution_identity_assertions`

It extends `model_invocations` with nullable expand-and-contract identity policy
pins, and extends `model_dispatch_attempts` with the key, assertion and verified
time used by authenticated claims. New service-layer writes require all pins;
legacy rows remain nullable and are never backfilled with invented identity.

PostgreSQL stores public keys, fingerprints, opaque private-key references,
nonce hashes, payload hashes, signature hashes and assertion hashes. It does
not store private keys, raw nonces, raw signatures, signed payloads, prompts,
context, model output, hidden reasoning, provider payloads or credentials.

## Key lifecycle

Key management is administrative and governed by these high-risk capabilities:

- `model.execution_identity_key.register`
- `model.execution_identity_key.rotate`
- `model.execution_identity_key.retire`
- `model.execution_identity_key.revoke`

The owner wildcard can administer keys. `execution_service` has explicit hard
denies for all four capabilities, so a dispatcher cannot register, rotate,
retire or revoke its own trust root.

Registration creates version 1 only when the principal has no active key.
Rotation creates a new immutable version and changes the previous active key to
`retiring`. A retiring key can verify already-issued work but is not selected
for new dispatch. Retirement moves a retiring key to `retired`. Revocation is
immediate for an active or retiring key. Pre-send locks the key row, so a
revocation racing with send has deterministic semantics: either revocation wins
and send is denied, or pre-send wins and the already authenticated send is
committed before the later revocation affects future work.

Private material is not accepted by the administrative CLI. Registration and
rotation accept only a base64 public key and an opaque `secret_ref`; the
reference is metadata and does not resolve provider credentials.

## Challenge and replay semantics

A challenge is short-lived and unique. Issuing a replacement invalidates any
prior open challenge for the same invocation. PostgreSQL persists only the hash
of the random nonce. The claim transaction locks the challenge and rejects an
expired, invalidated or consumed challenge. Its conditional consume update must
affect exactly one row. Consequently, concurrent consumers of the same
challenge produce exactly one winner and all later attempts are replay denials.

The canonical signed payload uses ordered JSON fields and UTC RFC3339Nano
timestamps. It contains no maps, floating-point values, prompt text, context,
response, clinical content or hidden reasoning.

## Transaction boundaries

Authenticated claim transaction:

- lock the requested invocation;
- enforce the pinned principal and concurrency limit;
- lock the challenge and key;
- load the pinned policy version;
- recompute the action digest and canonical payload;
- verify nonce hash, payload hash and Ed25519 signature;
- create the dispatch attempt;
- persist the assertion hash ledger;
- consume the challenge once;
- bind the key/assertion/verification time to the attempt;
- write sanitized audit events;
- mark the invocation claimed;
- commit.

Pre-send transaction remains the single Branch-10 coordinator for egress allow,
assignment use, quota increment, possible exhaustion, attempt/invocation
`send_started` and audit. Branch 11 adds locked revalidation of the identity
assertion, consumed challenge and key to that same transaction.

## CLI

Policy commands:

```text
orgctl model identity policy validate [--json]
orgctl model identity policy diff [--json]
orgctl model identity policy status [--json]
orgctl model identity policy sync --apply [--json]
```

Key commands:

```text
orgctl model identity key register --file command.json --actor-role ROLE [--json]
orgctl model identity key rotate --file command.json --actor-role ROLE [--json]
orgctl model identity key get ID [--json]
orgctl model identity key list [--principal-id ID] [--limit N] [--json]
orgctl model identity key retire ID --actor-role ROLE [--json]
orgctl model identity key revoke ID --actor-role ROLE --reason CODE [--json]
```

The command file contains an organization ID, execution-principal key, base64
public key, opaque secret reference, optional validity deadline and idempotency
key. There is no flag for a private key, signature, raw nonce, algorithm,
provider, model, dispatcher assignment or arbitrary dispatch principal.

Normal `orgctl model invocation dispatch ID` creates, signs, verifies and
consumes its challenge internally.

## Audit and outbox

Identity policy/key/challenge/allow/deny/replay events use `audit_events` with
bounded identifiers and hashes only. No new outbox event is introduced for the
internal identity lifecycle. Existing terminal model-invocation outbox behavior
is unchanged.

## Configuration

```text
ORG_MODEL_EXECUTION_PRINCIPAL_KEY
ORG_MODEL_EXECUTION_IDENTITY_ENABLED
ORG_MODEL_EXECUTION_IDENTITY_KEY_FILE
```

The principal key is a locator, not an authentication secret. When identity is
disabled, dispatch is denied; there is no fallback to label-only identity. The
key file is required only for dispatch, not policy validation or administrative
key operations.

## Threat model and residual risk

Covered threats include knowing a principal label without the private key,
replay, cross-organization/cross-principal/cross-assignment/cross-invocation
assertions, payload or action-digest tampering, expired assertions, revoked or
retired keys, concurrent challenge consumption and revocation racing pre-send.

Not covered in this branch: root/host compromise, remote process
authentication, mTLS, hardware-backed keys, remote attestation, external secret
managers, provider credential custody and provider-side security. Those remain
mandatory design work before a real adapter is enabled.

## Validation and rollback

The branch includes unit, race, fitness and PostgreSQL 17 integration coverage
for strict policy parsing, cryptography, private-key file safety, key lifecycle,
policy materialization, replay prevention, authenticated claim, tenant-safe
foreign keys, legacy denial and preservation of Branch-10 quota atomicity.

The down migration removes attempt identity constraints and columns first,
then assertions and challenges, invocation policy pins, keys and policy
versions. It uses child-to-parent order and no `CASCADE`. Migrations
`000001`–`000009` remain byte-identical to the Branch-11 base.
