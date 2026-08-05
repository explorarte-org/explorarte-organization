# Branch 06 validation record

## Scope

This record closes `feat/06-capability-policy-engine` against canonical base commit:

`fb30a4352697ed9cf1f7ef183285954450d1e71d`

The implementation adds the durable capability policy engine, PostgreSQL approval lifecycle, CLI, migration `000005`, tests, fitness checks, configuration, staging compatibility, audit events, and outbox events.

No file under `docs/canonical/**` was modified.

## Validation environment

- GitHub Actions hosted runner: Ubuntu 24.04
- Go: 1.25.0
- PostgreSQL: 17
- Repository: `Mireuz13/explorarte-organization`
- Branch: `feat/06-capability-policy-engine`

## Successful runs

### Ordinary CI

- Workflow run: `30969951981`
- Result: success
- Validated unit tests, fitness checks, native and cross builds, multi-architecture Docker build, PostgreSQL integration, registry integration, durable tasks, staging, authorization, migrations, and CLI smoke tests.

### Exact required validation sequence

- Workflow run: `30969952010`
- Job: `92191916647`
- Result: success
- Validated implementation commit: `0823ccd969735aed2deb62f189e3f7bda0a04c32`
- The temporary validation workflow was removed after the successful run.

The following sequence completed successfully in the required order:

```text
gofmt -w on changed Go files
git diff --exit-code
git diff --check
go test -short ./...
go test -race -short ./internal/authorization/...
make test-authorization-fitness
make test-task-fitness
make test-staging-fitness
make verify
make build-cross
make registry-validate
make test-integration
make test-authorization-integration
make test-staging-integration
make verify-all
git diff --exit-code fb30a4352697ed9cf1f7ef183285954450d1e71d -- docs/canonical
```

## Verified invariants

- Default-deny authorization with structured `allow`, `deny`, and `approval_required` effects.
- Global and authority-class hard denies take precedence over grants and approvals.
- Owner wildcard remains subject to hard denies.
- Human approval is explicit, scoped, expiring, one-time, and durable.
- Approval consumption uses row locking and permits exactly one durable use.
- Repeated identical consumption is idempotent and cannot authorize a second action.
- Organization revision, capability matrix hash, role state, scope, digest, and approval mode are revalidated before consumption.
- PostgreSQL failures and cancelled contexts remain operational errors rather than authorization denials.
- Request, decision, use, audit event, and outbox event changes commit atomically.
- Authorization reuses the existing global `audit_events` and `outbox_events` tables.
- `internal/authorization` does not import `internal/staging` or `internal/tasks`.
- Legacy staging authorization remains compatible for `code.stage_write`, `code.run_tests`, `code.commit`, and `task.review`.
- Migration `000005` applies, is idempotent, rolls down in a disposable environment, and reapplies successfully.
- `docs/canonical/**` is unchanged from the canonical base.

## Operational notes

The draft pull request used to trigger validation was temporary and must not be merged. The branch should be integrated only after reviewing this record and the final commit status.
