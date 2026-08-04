# Branch 05 — isolated staging, immutable artifacts, and explicit promotion

## Base and scope

- Base commit: `de5ac792cb67489b9f35e3b52cbec9ca8f63c7c7`.
- Branch: `feat/05-staging-promotion-engine`.
- Implements ADR-0005 without changing `docs/canonical/**`.
- Extends the durable task engine only through public task interfaces.
- Does not execute LLMs, arbitrary shell, deployment, remote Git operations, memory, RAG, cells, or schedulers.

The module is a safety boundary around code-producing tasks. A worker receives a durable execution lease, writes only in a detached Git worktree, seals a deterministic candidate commit, and stores a canonical manifest and binary patch in immutable content-addressed storage. The target ref remains unchanged until an explicit, capability-authorized promotion applies a Git compare-and-swap.

## PostgreSQL migration 000004

Migration `000004_create_staging_promotion_engine` adds:

- `staging_workspaces`
- `staging_artifacts`
- `staging_workspace_artifacts`
- `staging_checks`
- `staging_promotions`
- `staging_reviews`
- `staging_events`

Large blobs never enter PostgreSQL. The database stores IDs, Git object hashes, SHA-256 digests, opaque storage references, statuses, and audit metadata. Every durable staging transition writes a staging event, outbox event, and audit event in the same PostgreSQL transaction.

## Interfaces

`internal/staging` publishes:

- `RepositoryCatalog`
- `CapabilityAuthorizer`
- `WorkspaceReader`
- `WorkspaceService`
- `ArtifactStore`
- `GitBackend`
- `PromotionService`
- `StagingReconciler`
- `Persistence`

`internal/tasks` additionally publishes:

- `ExecutionLeaseVerifier.VerifyActiveExecutionLease`
- `RequirementVerifier.RecordRequirementVerification`

The staging package does not import `internal/tasks/postgres`; adapters are assembled in `internal/staging/bootstrap`.

## State machines

Workspace states:

```text
provisioning -> active -> sealed -> cleanup_pending -> cleaned
             \-> failed -> cleanup_pending
active -> abandoned -> cleanup_pending
active -> failed
cleanup_pending -> failed
```

`sealed`, `abandoned`, and `cleaned` never return to `active`. A cleaned workspace key is not reused. Cleaning removes the worktree but not immutable artifacts.

Promotion states:

```text
requested -> awaiting_gates -> approved -> applied
                         \-> rejected
approved -> conflicted | failed | cancelled
awaiting_gates -> cancelled | failed
requested -> cancelled | failed
```

`applied`, `rejected`, `conflicted`, `cancelled`, and `failed` are terminal.

## Authorization

Authorization loads `docs/canonical/capability-matrix.yaml`, validates it under default deny, and binds its semantic hash to the active organization revision.

- Workspace create/seal/abandon/cleanup: `code.stage_write`
- Check result recording: `code.run_tests`
- Promotion request/apply/cancel: `code.commit`
- Review: `task.review`

Hard denies win over grants. Unknown capabilities, unknown authority classes, stale revisions, changed capability-matrix hashes, disabled roles, and ambiguous state all deny. Owner wildcard remains subject to global hard denies.

Lease tokens enter only through stdin in the CLI. They are compared to the task engine's SHA-256 lease hash and are not included in flags, staging tables, events, outbox payloads, or audit payloads.

## Configuration

The module is disabled by default.

```text
ORG_STAGING_ENABLED
ORG_STAGING_REPOSITORIES_FILE
ORG_STAGING_WORKSPACE_ROOT
ORG_STAGING_ARTIFACT_ROOT
ORG_STAGING_QUARANTINE_ROOT
ORG_STAGING_COMMAND_TIMEOUT
ORG_STAGING_MAX_ARTIFACT_BYTES
ORG_STAGING_MAX_CHANGED_FILES
ORG_STAGING_STALE_AFTER
ORG_STAGING_RECONCILE_INTERVAL
ORG_STAGING_RECONCILE_BATCH_SIZE
ORG_STAGING_GIT_BINARY
```

Repository allowlisting is external and contains no credentials or remotes. See `config/repositories.example.yaml`. Paths must be absolute, canonical, non-symlink roots. Repository, workspace, artifact, and quarantine roots may not overlap. Target refs must be explicit full refs under `refs/heads/`.

Production `compose.yaml` deliberately does not mount host repositories, workspace roots, artifact roots, Git, or Docker sockets into `orgd`. Enabling staging without a valid local configuration blocks readiness but not `/healthz`.

## Safe Git behavior

The Git backend uses `exec.CommandContext` with argument vectors, never a shell. It forces:

- `GIT_TERMINAL_PROMPT=0`
- `GIT_CONFIG_NOSYSTEM=1`
- an empty global config
- an empty `core.hooksPath`
- no remote access

It rejects symlink repository roots, bare repositories, nested repositories, submodules, local filters, `filter=` attributes, and `working-tree-encoding` in this first version.

Creating a workspace uses:

```text
git worktree add --detach <derived-workspace-path> <exact-base-commit>
```

Sealing creates a deterministic candidate whose single parent is the exact base, creates `refs/explorarte/workspaces/<workspace-id>`, and emits a canonical JSON manifest plus binary patch. It never changes the target ref.

Before promotion, the manifest artifact is decoded with unknown fields denied and compared against the persisted workspace, task, attempt, base, candidate, tree, target ref, and changed-file count. Both manifest and patch references must match the recorded SHA-256 digests, and the sealed worktree must still be clean.

Promotion uses:

```text
git update-ref <target-ref> <candidate-commit> <expected-base-commit>
```

Applying an already-applied promotion is idempotent and returns the durable `applied` record without touching Git again. No merge, rebase, cherry-pick, fetch, push, or automatic conflict resolution occurs. The target ref must not be checked out in any worktree.

## Immutable artifacts

The local store layout is:

```text
<artifact-root>/sha256/ab/cd/<digest>
```

Writes are streamed through SHA-256 to a restricted temporary file, bounded by configured size, fsynced, and atomically published with a same-filesystem hard link. Existing digest paths are never overwritten. Files and directories use restrictive permissions and symlinks are rejected. Public references are opaque:

```text
artifact://sha256/<digest>
```

## Full flow

```text
task running
  -> workspace create with active lease
  -> changes only inside detached worktree
  -> workspace seal
  -> artifact requirement satisfied
  -> attempt succeeded
  -> task awaiting_verification
  -> promotion request
  -> controlled check result
  -> explicit independent review
  -> promotion approved
  -> atomic update-ref
  -> optional result evidence
  -> explicit task finalize completed
```

Promotion is not deployment. A successful process exit is not evidence of completion. Promotion does not finalize the task.

## Crash recovery

Git and PostgreSQL cannot share one transaction. The boundary is recovered idempotently:

- If `update-ref` succeeded and PostgreSQL still says `approved`, a retry or reconciler sees the target already at the candidate and records `applied` once.
- If the target is still at the expected base, no automatic promotion is performed by the reconciler.
- If the target moved to a third commit, the promotion becomes `conflicted` and requires a new workspace.
- Missing active workspace directories become `failed` with `workspace_missing`.
- Unknown workspace directories are moved to quarantine, never silently deleted.
- Explicit `orgctl staging workspace cleanup` performs physical worktree removal.

`orgd` may run only the safe reconciler. It cannot create, seal, check, approve, promote, or finalize tasks. Reconciler goroutines recover panics at the module boundary.

## CLI

```text
orgctl staging repo list|get|validate
orgctl staging workspace create|get|list|inspect|seal|abandon|cleanup
orgctl staging check record
orgctl staging promotion request|get|list|review|apply|cancel
orgctl staging reconcile
```

JSON output is stable. Lease-token commands require `--lease-token-stdin` and reject terminal stdin. Flags can appear before or after positional IDs.

## Tests and fitness checks

Unit coverage includes validation, state terminality, capability default deny, hard denies, owner wildcard, revision/hash mismatch, deterministic manifests, artifact corruption and limits, repository safety, real worktree sealing, and token-safe CLI helpers.

Integration coverage uses PostgreSQL 17 and real local Git repositories with no remotes. The primary test exercises task creation, active lease verification, isolated worktree creation, deterministic seal, immutable artifacts, awaiting-verification transition, check, independent review, atomic promotion, and cleanup with artifact survival.

Commands:

```bash
make verify
make build-cross
make test-integration
make test-staging-integration
make test-task-fitness
make test-staging-fitness
```

## Applying on AWS

1. Verify the exact base commit and a clean working tree.
2. Apply the generated package with `apply.sh`; it creates the branch but does not commit.
3. Run all validation commands above with Go 1.25, PostgreSQL 17, Docker, and real Git.
4. Inspect `git diff --check` and confirm canonical documents plus migrations 000001–000003 are unchanged.
5. Commit only after all tests pass.

For production, keep staging disabled until separate host roots and a controlled toolchain are provisioned. Do not mount the live repository into the distroless `orgd` container.

## Rollback

Before production data exists, migration 000004 can be rolled down in a disposable or explicitly approved environment. After staging records exist, prefer disabling the module and retaining audit state; do not automatically delete artifacts or workspaces. Git promotion is never automatically reverted.

## Known limitations

- Local content-addressed filesystem is the first artifact backend; S3-compatible storage is deferred.
- Only direct fast-forward-like ref replacement from exact base to its single-parent candidate is supported.
- No remote Git, PR creation, deployment, merge, rebase, cherry-pick, or automatic rollback.
- The reconciler does not promote refs.
- A controlled external toolchain must make code changes and run checks; the service records their verifiable results.

## Next compatible branch

A controlled execution/toolchain branch can consume the staging interfaces without granting arbitrary shell to `orgd`. The later logical-assurance branch can add policy verification in shadow mode without changing the promotion CAS contract.
