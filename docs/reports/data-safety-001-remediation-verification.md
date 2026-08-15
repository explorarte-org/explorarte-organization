# DATA-SAFETY-001 — remediation verification

```
MISSION:               ORGANIZATION-REDESIGN-001
PHASE:                 DATA-SAFETY-001-REMEDIATION-VERIFICATION
BASE_SHA:              4c847eb590e50a16fc4ee4bce82a4e5c771d1bf0
FINAL_STATUS:          DATA_SAFETY_REMEDIATION_VERIFIED
CODE_CHANGED:          false
PRODUCTION_DB_MUTATED: false
MODEL_SPEND:           $0.00
```

Verification only. No runtime or test code was changed, because no exploitable gap was
demonstrated. All proofs are behavioural, executed against disposable PostgreSQL instances.

---

## 1. Chronology

| | |
| --- | --- |
| 2026-08-12 | Destructive integration test reaches the shared development DB through a host-port collision; `TRUNCATE organizations … CASCADE` destroys org-scoped runtime history. No backup. |
| 2026-08-12T06:26:55Z | `324148d4b4cf0a937d4e1b88404bb0406d5195df` — *fix(integration-tests): fail-closed test-database guard after data-loss incident* introduces `internal/testdbguard`. |

```
guard_existed_at_incident = false
```

The guard postdates the incident, so it was never bypassed historically — it did not exist.
The question this phase answers is whether it holds **now**, adversarially.

The package's own doc comment records the mechanism verbatim: a test trusted
`ORG_TEST_DATABASE_URL` alone, and *"a port collision on the shared production compose file
silently redirected that URL at the development database"*.

---

## 2. Design under test

```
RequireTestDatabase(ctx, dsn, pool)
  1. DSN path == CanonicalDisposableDatabase          (hardcoded const, never env-read)
  2. live SELECT current_database()
  3. observed == CanonicalDisposableDatabase

RequireDestructive(ctx, dsn, pool)
  1. RequireTestDatabase must pass first
  2. ORG_TEST_DESTRUCTIVE_DATABASE == explorarte_test
```

Two properties matter and both were confirmed by reading the source, not assumed:
`CanonicalDisposableDatabase` is a hardcoded constant — a bad DSN cannot talk the package into
approving itself — and `RequireDestructive` delegates to `RequireTestDatabase` **before**
consulting the sentinel, so the sentinel is never an independent authorization.

---

## 3. Destructive-site inventory

Static analysis by file is invalid here: the guards live in helpers (`openXStore`,
`resetXSchema`) called from the tests. The inventory expands the real execution sequence from
each `Test*` entry point, following local calls up to depth 4.

```
packages with tests                              107
destructive sites reachable from a Test          292
GUARDED_BEFORE_ANY_MUTATION                      287
STATIC_ANALYSIS_FALSE_POSITIVE                     5
UNGUARDED_DESTRUCTIVE_SITE                         0
DESTRUCTIVE_GUARD_PRESENT_BUT_LATE                 0
TEST_DATABASE_GUARD_PRESENT_BUT_WRITE_PRECEDES_IT  0
```

The five false positives were each inspected individually:

| Site | Why it is not a destructive site |
| --- | --- |
| `internal/modelidentity/crypto_test.go:54` | `time.Truncate(time.Microsecond)` — Go time API |
| `internal/modelruntime/provider_request_test.go:35` | same |
| `internal/platform/migrations/runner_test.go:11,13` | `"DROP TABLE second;"` string literals in in-memory migration fixtures |
| `internal/secretscan/secretscan_test.go:89` | `truncate(text)` — a local Go helper for subtest naming |

None of those four packages opens a database connection.

### Ordering property

The invariant is not "a guard exists somewhere before the TRUNCATE" but **no database mutation
before database identity is verified**. In every guarded path the observed order is:

```
open connection → RequireTestDatabase → migrations Up → RequireDestructive → destructive op
```

Identity is verified *before* the migrations, which are themselves mutations. No site was found
where a migration, fixture insert or DDL precedes the identity check.

---

## 4. Adversarial matrix

Executed against a disposable PostgreSQL 17 instance (`ds001-adv-db`, host port 55432, removed
afterwards) holding three identities: `explorarte_test`, `explorarte_org` and
`explorarte_prod_shaped`. The shared development and production databases were never targets.

The pool was wrapped in a recorder capturing every statement the guard issues.

| Case | DSN says | live DB | sentinel | Expected | Result |
| --- | --- | --- | --- | --- | --- |
| A | `explorarte_org` | `explorarte_org` | absent | DENY | **PASS** |
| B | `explorarte_prod_shaped` | same | absent | DENY | **PASS** |
| C | `explorarte_test` (forged) | `explorarte_org` | absent | DENY | **PASS** |
| D | `explorarte_test` (forged) | `explorarte_org` | **set** | DENY | **PASS** |
| E | `explorarte_org` | `explorarte_org` | **set** | DENY | **PASS** |
| F | `explorarte_test` | `explorarte_test` | absent | RequireTestDatabase ALLOW / RequireDestructive DENY | **PASS** |
| G | `explorarte_test` | `explorarte_test` | set | ALLOW | **PASS** |

Case D is the one that matters: a correct sentinel plus a forged DSN is defeated by the live
`current_database()` check, which reports `explorarte_org` and refuses.

### Early-fail proof

```
case | guard_result | first_sql                  | last_sql_before_deny       | mutations_before_deny
A    | DENY         | (none issued)              | (none issued)              | 0
B    | DENY         | (none issued)              | (none issued)              | 0
C    | DENY         | SELECT current_database()  | SELECT current_database()  | 0
D    | DENY         | SELECT current_database()  | SELECT current_database()  | 0
E    | DENY         | (none issued)              | (none issued)              | 0
F    | DENY (destr) | SELECT current_database()  | SELECT current_database()  | 0
```

`mutations_before_deny = 0` for every unsafe case, which was the requirement. A, B and E deny on
the DSN name alone and never open a query at all; C and D issue exactly one statement, the
identity probe itself.

---

## 5. Global sentinel attack

Simulating a CI that globally exports `ORG_TEST_DESTRUCTIVE_DATABASE=explorarte_test`, then
presenting unsafe identities:

| dsn | live | result |
| --- | --- | --- |
| `explorarte_org` | `explorarte_org` | DENY — *connection string names database "explorarte_org"* |
| `explorarte_prod_shaped` | same | DENY — same reason |
| `explorarte_test` | `explorarte_org` | DENY — *live connection reports database "explorarte_org"* |
| `explorarte_test` | `explorarte_prod_shaped` | DENY — same |

```
SENTINEL_GLOBAL_ENV_ATTACK = PASS
```

The sentinel cannot independently authorize destruction. Zero mutations before each denial.

---

## 6. Fitness analyzer

The script documents its own limit: it reasons per top-level function, not per `t.Run` closure.
Four synthetic fixtures were built, each in an isolated tree with its own copy of the script
(the script derives its root from its own location, so directory arguments are ignored):

| Case | Shape | Expected | Result |
| --- | --- | --- | --- |
| 1 | guarded subtest + unguarded subtest, same function | FAIL | **PASS (false negative)** |
| 2 | TRUNCATE before the guard, same function | FAIL | FAIL ✓ |
| 3 | destructive op in a helper called from a subtest | FAIL or uncovered | FAIL ✓ |
| 4 | guard before the operation, same path | PASS | PASS ✓ |

```
STATIC_ANALYZER_LIMITATION_PRESENT   = true   (case 1 confirmed empirically)
CURRENT_DESTRUCTIVE_SITE_UNPROTECTED = false
```

These are recorded separately and deliberately not conflated. The analyzer *can* be fooled by
the case-1 shape; the repository does not currently contain that shape — every destructive site
has a guard executing before it on the same pool, per §3.

### Two findings about the analyzer itself

**It fails on clean `main`.** Running it unmodified at `4c847eb` reports one violation:

```
migrations/r47_seed_openai_responses_idempotency_test.go:201: destructive SQL in this function,
but no testdbguard.RequireDestructive call anywhere in the same top-level function
```

That site is `resetSchema`, which runs `DROP SCHEMA public CASCADE; CREATE SCHEMA public;` —
more destructive than a TRUNCATE. It has exactly two callers, both inside
`openMigrationTestStore`, and both run **after** `RequireTestDatabase` *and* `RequireDestructive`
have been executed on the same store. The site is protected in every reachable path; the
analyzer cannot see across the call boundary. This is a false positive of the same per-function
rule, in the opposite direction from case 1.

**Nothing runs it.** `check-testdbguard-fitness` appears in no workflow under `.github/workflows/`
and in no Makefile target. The second line of defence exists but is not wired into CI, so its
current failure has gone unnoticed. This is reported, not fixed: it does not permit a
vulnerability, and §11 restricts changes to demonstrated gaps.

---

## 7. Compose isolation

The incident's mechanism was a host-port collision. The overlay closes it structurally:

```
compose.yaml              postgres ports: "127.0.0.1:5432:5432"
compose.integration.yaml  postgres ports: !reset []      → publishes no host port at all
```

The integration harness reaches its database over the internal `postgres` DNS name, so there is
no host port for a stray `127.0.0.1:5432` DSN to land on. Volumes are separated the same way:
`compose.yaml` pins a fixed volume name, and the integration overlay removes that pin so Compose
allocates a project-scoped volume instead of the durable production one.

```
COMPOSE_ISOLATION = PASS
```

Defense in depth only — `testdbguard` passed the adversarial matrix independently of this.

---

## 8. Mutation fitness

Each safety condition was removed in a mutant and confronted with a case the real guard denies.
A condition only counts as verified if its mutant *allows* what the real guard refuses.

| Condition removed | Probe | Real guard | Mutant | Detection |
| --- | --- | --- | --- | --- |
| DSN identity check | dsn=`explorarte_org`, live=`explorarte_test` | DENY | ALLOW | **yes** |
| live `current_database()` | dsn forged=`explorarte_test`, live=`explorarte_org` | DENY | ALLOW | **yes** |
| destructive sentinel | dsn+live=`explorarte_test`, no sentinel | DENY | ALLOW | **yes** |

```
MUTATION_FITNESS = PASS
```

All three conditions have demonstrated detection power. None is decorative. Mutants were not
committed.

---

## 9. Tests and build

| Command | Exit | Postcondition |
| --- | --- | --- |
| `go test ./internal/testdbguard/` | 0 | package unit tests pass |
| `go vet ./internal/testdbguard/` | 0 | clean |
| `go build ./...` | 0 | whole module builds |
| `bash scripts/check-testdbguard-fitness.sh` | 1 | one violation, analysed in §6 as a false positive |
| adversarial matrix (`-tags ds001adversarial`) | 0 | 7/7 cases as required |
| mutation fitness (`-tags ds001adversarial`) | 0 | 3/3 conditions detect |

### Live integration closure (phase DATA-SAFETY-001-LIVE-INTEGRATION-CLOSURE)

Two disposable instances of the project's own `pgvector/pgvector` image: `ds001-safe`
(`explorarte_test`, :55433) and `ds001-unsafe` (`explorarte_org` + `explorarte_production`,
:55434). The shared development and production databases were never targets.

**Real destructive paths against `explorarte_test`** — all five PASS, 103 tables materialised
by the migration path:

| Package | Test | Result |
| --- | --- | --- |
| `internal/organization/registry` | `TestCanonicalRegistryAgainstPostgreSQL` | PASS |
| `internal/rag/postgres` | `TestApprovedKnowledgeRAGPostgresRepository` | PASS |
| `internal/contextengine/postgres` | `TestContextEnginePostgreSQL17` | PASS |
| `migrations` | `TestMigration047_NoPreexistingRows` | PASS |
| `internal/cellworker/postgres` | `TestCellWorkerPostgresWorkSource` | PASS |

**Same entrypoints against unsafe identities, sentinel always set** — 9/9 DENY, schema unchanged
(`tables_before == tables_after`) in every case:

```
identity=explorarte_org         DENY  testdbguard: connection string names database "explorarte_org"
identity=explorarte_production  DENY  testdbguard: connection string names database "explorarte_production"
```

The forged-DSN variant could not be reproduced faithfully at process level: a DSN naming
`explorarte_test` against the unsafe instance fails at connection because that database does not
exist there. The property it targets — DSN says test, live connection is not — was proven at
unit level in case D, where the pool and the DSN can be mismatched deliberately. Recorded as a
method limit, not as an additional pass.

**Broad suite** `go test -tags=integration ./...`: 94 ok, 27 FAIL, 15 no-test packages.
Re-running the 13 failing packages serially (`-p 1`) turned 12 into `ok`, so those failures are
**parallel-execution interference over one shared test database**, not defects — a real finding
about the suite's isolation, unrelated to the guard.

The last one, `cmd/orgctl`, failed because it reads `ORG_DATABASE_URL` rather than
`ORG_TEST_DATABASE_URL`; the guard refused with *connection string names database ""* — correct
fail-closed behaviour on an unset variable. With that variable also exported it passes.

Package-by-package: 13/13 of the previously failing set PASS once run serially with correct
environment. No package is left as `FAIL`; the interference ones are classified
`BLOCKED_UNRELATED_DEPENDENCY (shared-database parallelism)`.

Temporary fixtures were deleted; the worktree ends clean at `4c847eb…`. The disposable container
was removed. Production remains at 1 organization row, untouched.

---

## 10. Closure

| Criterion | Result |
| --- | --- |
| A — guard introduced post-incident | confirmed, `324148d4`, 2026-08-12T06:26:55Z |
| B — all destructive sites protected before mutation | 287/287 real sites; 5 false positives; 0 unguarded |
| C — unsafe identities fail before mutation | A–E deny, 0 mutations before deny |
| D — live `current_database()` defeats forged DSN | cases C and D |
| E — global sentinel alone cannot authorize | 4/4 unsafe combinations denied |
| F — compose isolation independent and effective | `ports: !reset []`, separate volume |
| G — analyzer limits not exploited by current repo | limitation real, repository path not present |
| H — mutation tests demonstrate detection power | 3/3 |

```
DATA_SAFETY_REMEDIATION_VERIFIED
```

### Recommended, not done here

Two items fall outside this phase's minimal-remediation rule and are left for a decision:

1. **Wire the fitness script into CI.** It currently runs nowhere, so its failure on `main` went
   unobserved. A gate nobody executes is not a gate.
2. **Teach the analyzer about call boundaries, or annotate the `r47` helper.** Its current output
   on `main` is a false positive, which trains readers to ignore it — the failure mode that makes
   a red check worthless.

Neither is a safety gap today. Both are reliability gaps in the *detection* layer, which is
exactly the category this campaign has repeatedly learned to take seriously.

---

## 11. Evidence

Hashes bind each artifact **as observed at manifest time**; re-verification from these
paths is possible only while the same byte stream remains there.

| Artifact | Path | Bytes | SHA-256 | Status |
| --- | --- | ---: | --- | --- |
| `ds001_inventory.json` | `/home/ubuntu/redesign-001/state/ds001_inventory.json` | 147,177 | `d9643ef99ef0ada664d4fd1689f358e897bbc3f9ae566045f3c0e1e4a94f7073` | `PRESENT_AT_MANIFEST_TIME` |
| `mutable_path_provenance.json` | `/home/ubuntu/redesign-001/state/mutable_path_provenance.json` | 1,072 | `ce3e59b1904abf83a78586f9f5bb41bfb145d27f8aa042056feb36e147b6a235` | `PRESENT_AT_MANIFEST_TIME` |

- **`ds001_inventory.json`** — expanded destructive-site inventory: 292 sites, per-site ordering classification
- **`mutable_path_provenance.json`** — mutable-path provenance search (carried over from RERUN-002 closure)

### Deliberately not retained

- `adversarial_ds001_test.go` — temporary fixture, deleted per mandate §12 (do not commit mutants)
- `mutation_ds001_test.go` — temporary fixture, deleted per mandate §12
- `ds001-safe / ds001-unsafe containers` — disposable PostgreSQL instances, removed after the run

The adversarial and mutation results in §4, §5 and §8 are therefore reproducible from the
procedure described, not from a retained binary. The disposable instances were created from
the project's own `pgvector/pgvector` image and destroyed afterwards.
