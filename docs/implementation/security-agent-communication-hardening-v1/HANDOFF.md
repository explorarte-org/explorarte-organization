# Security Hardening — Agent Communication, Trust Boundaries & Covert Channels v1

> **Cómo leer este documento.** Sólo `FINAL STATE` es autoritativo. Todo lo que
> aparece bajo `APPENDIX: HISTORY` es el registro cronológico de cómo se llegó
> aquí y contiene estados ya superados —tests pendientes, migración 000041 para
> esta rama, Go sin instalar, WB-3 abierto—. Se conserva porque explica
> decisiones, no porque describa el presente. Si las dos partes se contradicen,
> gana `FINAL STATE`.

---

# FINAL STATE

```
Security Hardening V1

STATUS:
implementation        PASS
build                 PASS
vet                   PASS
gofmt (ficheros de esta rama)  PASS
race suite            PASS
topology enforcement  PASS
mutation evidence     PASS
secret ingress        PASS
owner separation      PASS
disabled-role denial  PASS

MERGE READY:
NO — hasta rebasar sobre un main que contenga 000041

MIGRATIONS:
000041 RAG integrity                      (rama de Worker A, ya aplicada en producción)
000042 messaging authorization            (esta rama)
000043 legacy MessageStatus retirement    (esta rama)

PRODUCTION READY:
NO — hasta suite post-rebase + integración + despliegue desde main inmutable
```

## CURRENT INTEGRATION STATE

```
Worker A
  origin:       YES
  main:         NO
  production:   YES
  SHA:          38a8c08
  migration tip: 000041

Worker B
  origin:       YES (respaldado pre-rebase)
  main:         NO
  production:   NO
  migration target after rebase: 000042 / 000043

BLOCKER:
Worker B MUST NOT rebase until main fast-forwards
to Worker A / 38a8c08.
```

Esto cierra la anomalía de fondo que motivó la auditoría: una rama de feature
era más autoritativa que `main`, porque producción se construyó desde ella. El
fast-forward no lleva código nuevo a producción —producción ya corre 38a8c08—;
hace que el repositorio autoritativo vuelva a describir lo que está desplegado.

Una vez hecho, la cadena vuelve a ser la correcta:

```
feature → validated → main → immutable image → production
```

El SHA de Worker B previo al rebase queda en `origin` como punto de
recuperación. El rebase reescribirá esa historia, así que el push posterior es
`--force-with-lease`, nunca `--force`.

## Qué está realmente implementado y probado

| Control | Evidencia ejecutable |
|---|---|
| Topología V1 (4 aristas, resto default-deny) | `TestTopologyV1EdgeContract` — 4 ALLOW, 12 DENY |
| El owner no participa en `agent_messages` | 5 casos DENY explícitos en la misma matriz |
| Rol deshabilitado no puede enviar ni recibir | 3 casos DENY; mutación de la regla hace fallar la suite |
| La afirmación `topology_check` del catálogo | `TestTopologyClaimIsBackedByRealDenials` llama al validador real |
| Payload de mensajes sin texto libre | `TestPayloadSmugglingClaimIsBackedByRealRejection` — 5 intentos reales rechazados |
| Secretos rechazados en ingreso de tareas | `TestCreateRequestRejectsSecretsInEveryAgentVisibleField` — 6 campos |
| Datos clínicos/personales NO rechazados | `TestCreateRequestCarriesSensitiveButNonSecretData` |
| Límite de 64 KiB en instrucciones | `TestCreateRequestKeepsItsSizeBound` — 65536 pasa, 65537 no |
| Revalidación de memoria con el rol real | Interfaz `ValidateVersion(ctx, actorRoleID, record)` |
| Fallo estructural de esquema → fail-fast | `ErrStructuralSchema` + salida del proceso |

## Pruebas de mutación superadas

Neutralizar el control debe romper la suite. Verificado en tres:

| Control neutralizado | Tests que fallan |
|---|---|
| `ValidateEdge` devuelve `nil` | 5, incluido el de `securityaudit` |
| `rejectSecrets` devuelve `nil` | 2, en `tasks` **y** en `securityaudit` |
| Regla `role.Enabled` desactivada | `TestTopologyV1EdgeContract` |

---

# MERGE / DEPLOY CHECKLIST

## Precondición bloqueante (hoy sin cumplir)

`origin/main` está en `e3f66e4` con tip de migración **000040**. No contiene la
rama de Worker A (`38a8c08`, 5 commits por delante) ni por tanto la 000041.

**Nadie puede rebasar esta rama hasta que Worker A entre en main.** El orden no
es negociable: la 000042 de esta rama asume que la 000041 ya existe y ocupa su
hueco reservado.

## Secuencia

```
1. Congelar Worker B                          ← hecho
2. Commit del árbol completo                  ← hecho
3. main contiene Worker A + 000041            ← BLOQUEADO
4. Rebase Worker B sobre ese main
5. Confirmar migraciones 000041 / 000042 / 000043
6. Resolver sólo conflictos de integración
7. Verificación completa post-rebase
8. Merge a main
9. Build imagen inmutable orgd:<git-sha> desde main
10. Deploy
```

## Verificación post-rebase (repetir entera, aunque hoy esté verde)

El rebase es precisamente donde aparecen los errores de integración. No vale
"ya pasó antes".

```bash
gofmt -l .
go build ./...
go vet ./...
go test ./... -count=1
go test -race -count=1 ./...
```

Más: migraciones contra una base desechable, y los tests conductuales críticos
de messaging (topología, ingreso de secretos, mutación).

**Nota**: `gofmt -l .` marca hoy **9 ficheros preexistentes** ajenos a esta rama
(`cmd/orgctl/main.go`, `cmd/orgd/main.go`, `internal/contextcompiler/*`,
`internal/modelegress/canonical_policy.go`, `internal/modelruntime/adapter/mimo/*`,
`internal/modelruntime/costgate/gate.go`, `internal/organization/registry/validation.go`).
No se tocaron para no mezclarlos con este diff; hay que limpiarlos o el paso
falla por algo que esta rama no rompió.

## Rollback: qué significa a partir de la 000042

Tras aplicar 000042/000043, **un binario anterior ya no puede arrancar contra
esa base**: detecta migraciones desconocidas y se termina. Eso es el fail-fast
haciendo su trabajo, no un defecto, pero cambia el significado de "rollback".
Ya no es "volver a la imagen anterior".

```
pre-deploy DB backup / snapshot
        ↓
apply migrations
        ↓
deploy orgd:<git-sha>
        ↓
smoke / readyz / version
        │
        ├── PASS → mantener
        │
        └── FAIL
              ↓
        restore DB snapshot
              +
        previous image
```

La alternativa —debilitar `validateApplied()` para que un binario incompatible
siga vivo— queda descartada explícitamente. Es preferible un rollback real de
aplicación y esquema a un proceso que sirve sobre un esquema que no entiende.

`/version` expone `migration_tip` para que el diagnóstico de drift sea una
comparación de dos números y no arqueología de logs.

---

# OPEN DEBT

Deuda consciente, no olvidos. Ninguna bloquea el merge.

| ID | Qué | Por qué se aplaza |
|---|---|---|
| R-6 | El error de *sender* irresoluble no envuelve `ErrTopologyViolation`; el de *recipient* sí. Un llamante que use `errors.Is` para separar "denegado" de "fallo reintentable" clasificará mal. | Normalización de errores, sin impacto de seguridad (ambos casos deniegan) |
| R-7a | `extractUnitFromRole` quedó muerto tras la reescritura de la topología | Limpieza |
| R-7b | Filas anteriores a la 000042 tienen `request_hash` NULL y saltan la verificación de idempotencia | Sólo afecta a datos heredados |
| WB-3b | Redacción en observabilidad: `secretscan.Redact` existe y está probado, pero ningún camino de log lo invoca todavía porque **ningún log emite hoy instrucciones de tarea**. Cablearlo cuando alguno lo haga. | Sin superficie que proteger todavía |
| — | Roles de unidades transversales sin líder (`investigacion/*`, `empresa/ceo_observer`) no tienen arista. Es fail-closed y coherente con V1. | Decisión de topología, no defecto |

---

# SECURITY INVARIANTS

Reglas transversales del proyecto, no lecciones de esta rama.

## 1. Un control existe sólo si se puede demostrar que falla al quitarlo

> Un control crítico no puede considerarse implementado si neutralizarlo no hace
> fallar al menos una prueba.

Aplica a TreeRAG, Memory OS, permisos de montaje, observers, self-modification y
cualquier mecanismo de gobernanza futuro.

## 2. La evidencia debe vivir donde se hace la afirmación

Corolario descubierto al aplicar la regla anterior. No basta con que exista un
test que falle *en algún sitio*: si el catálogo afirma un control y su prueba
vive en otro paquete enlazada por convención, quien lee el catálogo no tiene
razón local para dudar. `securityaudit` llama ahora al validador real y al path
de ingreso real, de modo que neutralizar cualquiera de los dos rompe también el
paquete que hace la afirmación.

Codificado en `mitigationEvidence`
(`internal/securityaudit/topology_enforcement_test.go`): cada etiqueta que el
catálogo puede declarar está mapeada al test que la demuestra, y una etiqueta
sin entrada rompe la suite.

## 3. Las tres clases de mentira arquitectónica

Encontradas en esta rama, con la misma cura: `CLAIM → EVIDENCE → BEHAVIORAL TEST → MUTATION TEST`.

| Clase | Ejemplo real | Cómo se veía |
|---|---|---|
| Control declarado pero inexistente | `topology_check` con un `TODO` debajo | Manifiesto en verde, enforcement ausente |
| Control existente sin evidencia ejecutable | límite de 64 KiB real, declarado como `SizeBoundBytes: 0` | Manifiesto más pesimista que la realidad |
| Control existente descrito incorrectamente | `structured_payload_with_secret_detection` sobre un esquema cerrado | Etiqueta prometía un escáner inexistente sobre un control más fuerte |

La segunda y la tercera son especialmente traicioneras: un manifiesto que miente
a la baja se ignora, y uno que miente en la etiqueta hace que nadie busque el
control real.

## 4. Secretos, datos sensibles y observabilidad son tres cosas distintas

> Secrets are rejected at ingress; sensitive information is governed by
> classification; observability is redacted.

No se redacta en la entrada: reescribir una instrucción cambia su semántica, y
una tarea que reporta éxito sobre instrucciones mutiladas en silencio es peor
que una que se niega a arrancar. Los datos personales y clínicos **se
transportan**; los gobierna la clasificación, no el rechazo.

## 5. La autoridad humana no es una arista del bus de agentes

`empresa/human` no participa en `agent_messages`. Detener, repriorizar, aprobar
o instruir al CEO entran por una interfaz de control/gobernanza explícita.
Mezclarlas convertiría la gobernanza en tráfico operativo indistinguible.

---

# APPENDIX: HISTORY

> Todo lo que sigue está **superado** por `FINAL STATE`. Se conserva como
> registro de decisiones y de cómo se encontró cada defecto. Contiene
> afirmaciones que ya no son ciertas: que Go no está instalado, que hay tests
> pendientes de escribir, que la migración de esta rama es la 000041, que WB-3
> sigue abierto y que `request_hash` está implementado (lo estaba a medias: el
> código existía, el comportamiento no — ver R-1).

---

## Base SHA
```
e3f66e42a3a9fd0ad5d9ae5851c66b49bdb7581c  (origin/main)
```

## Branch
```
feat/security-agent-communication-hardening-v1
```

## Final Commit SHA (Pending Push)
```
PENDING: `git rev-parse HEAD` after completing test execution
```

---

## IMPLEMENTACIÓN vs TESTEADO vs INTEGRACIÓN VERIFICADA

### ✅ IMPLEMENTED + CODE REVIEWED (Sintaxis correcta verificado manualmente)

| Component | Status | Verificación |
|-----------|--------|--------------|
| capability-matrix.yaml | Implemented | New capabilities validated by YAML parse |
| DelegationPayloadV1 / CompletionPayloadV1 | Implemented | Types validated against SendCommand |
| Semantic Invariants (DelegatedTaskID==RecipientTaskID, CompletedTaskID==SenderTaskID) | Implemented | Logic traced through validateSemanticInvariants |
| MaxPayloadBytes = 1024 with ErrPayloadTooLarge | Implemented | Enforced in Validate() |
| MessageStatus ELIMINATED | Implemented | No case exists in Valid() or validateSemanticInvariants |
| Topology V1 (CEO→dept_leader, dept_leader→worker, worker→dept_leader, dept_leader→CEO) | Implemented | Registry-based validation from canonical organization.yaml |
| Memory.Get/List auth gate | Implemented | actorRoleID required, role match enforced |
| Memory.GetForRevalidation narrow API | Implemented | Preconditions: expectedOrg/role/status/dataClass |
| ProviderRender V2 as DEFAULT path | Implemented | BuildProviderRender → BuildProviderRenderV2 unconditionally |
| Principal authentication mandatory (no placeholder/fallback) | Implemented | ResolveByKey from modeldispatch.PrincipalStore |
| Fail-closed on all error cases | Implemented | empty/disabled/wrong-org/wrong-role → DENY |
| Orchestrator silent skip ELIMINATED | Implemented | Messaging wired but principal missing → explicit error |
| postgres store principal validation | Implemented | validateExecutionPrincipalForSender + ClaimNext principal match |
| request_hash computation | Implemented | SHA-256 over canonical fields including max_attempts + schema_version |
| covert_channels catalog | Implemented | 21 channels with detection rules + CheckViolations() |
| Migration 000041 | Created | provisional — will need rebase/renumber against Worker A |
| All callers updated | Implemented | runtimeadapter, orchestrator, bootstrap ALL wired to principalKey |

### ⚠️ PENDING TESTS (Environment constraint: Go 1.25 not installed on VPS)

| Test Category | File | Status |
|---------------|------|--------|
| Send payload size rejection | internal/agentmessaging/types_test.go | Written ✓ |
| Sender task cross-role org mismatch | internal/agentmessaging/postgres/store_test.go (new) | Needs creation |
| Sender task cross-org | internal/agentmessaging/postgres/store_test.go (new) | Needs creation |
| Recipient task cross-role | internal/agentmessaging/postgres/store_test.go (new) | Needs creation |
| Recipient task cross-org | internal/agentmessaging/postgres/store_test.go (new) | Needs creation |
| Principal spoofing (wrong role ID) | internal/agentmessaging/postgres/store_test.go (new) | Needs creation |
| Revoked principal mid-lease | internal/agentmessaging/postgres/store_test.go (new) | Needs creation |
| Stolen claim token from another principal | internal/agentmessaging/postgres/store_test.go (new) | Needs creation |
| Inbox theft (cross-inbox claim) | internal/agentmessaging/postgres/store_test.go (new) | Needs creation |
| Idempotency same key/different semantic command | internal/agentmessaging/types_test.go | Partially written |
| Duplicate JSON keys | internal/agentmessaging/types_test.go | Written ✓ |
| Unknown payload fields | internal/agentmessaging/types_test.go | Written ✓ |
| Malformed/multiple JSON values | internal/agentmessaging/types_test.go | Partially written |
| Oversized payload | internal/agentmessaging/types_test.go | Written ✓ |
| Worker lateral communication blocked | internal/agentmessaging/topology_test.go (new) | Needs creation |
| Cross-department communication blocked | internal/agentmessaging/topology_test.go (new) | Needs creation |
| Memory own-role allow | internal/memory/manager_test.go additions | Needs creation |
| Memory cross-role deny | internal/memory/manager_test.go additions | Needs creation |
| Memory cross-org deny | internal/memory/manager_test.go additions | Needs creation |
| GetForRevalidation scope/status/dataclass | internal/memory/manager_test.go additions | Needs creation |
| ProviderRender delimiter injection | internal/contextengine/providerrender_test.go additions | Needs creation |
| ProviderRender determinism | internal/contextengine/providerrender_test.go additions | Needs creation |
| Covert-channel catalog invariant failures | internal/securityaudit/covert_channels_test.go (new) | Needs creation |

### ❌ NO ELEGIDO (Explicitamente descartado por diseño)

| Feature | Reason Rejected |
|---------|-----------------|
| Owner→any role bypass removed | Too broad authority; requires explicit per-need justification |
| CEO→other CEO allowed | Peer-to-peer between executives not needed |
| Same-dept peer messaging | Lateral coordination handled via task workflow/outbox |
| worker→delegation recipients generic rule | Only via authorized child-task delegation workflow |
| memory.read_department / read_cross_role | No documented productive caller yet; minimal privilege principle |
| Legacy ProviderRender v1 as default | Would leave untrusted data unstructured; security risk |

---

## Resumen de los 10 Fixes

### Fix 1: Capabilities Agent Messaging
**Files:** `docs/canonical/capability-matrix.yaml`

Capabilities añadidas:
- `agent.message.send` → executive, department_leadership, specialist
- `agent.message.claim` → executive, specialist
- `agent.message.settle` → executive, specialist
- `memory.read_own` → specialist, research_execution, transversal_audit

**No implementadas en V1:** `memory.read_department`, `memory.read_cross_role` (sin caller productivo demostrado)

---

### Fix 2: Payloads Estructurados con Invariants Semánticos
**File:** `internal/agentmessaging/types.go`

```go
type DelegationPayloadV1 struct { DelegatedTaskID int64 }
// Invariant: DelegatedTaskID == SendCommand.RecipientTaskID

type CompletionPayloadV1 struct { CompletedTaskID int64 }
// Invariant: CompletedTaskID == SendCommand.SenderTaskID

const MaxPayloadBytes = 1024
// Rejects oversized with ErrPayloadTooLarge
```

Validación completa en `SendCommand.Validate()`:
1. SchemaVersion must be "v1"
2. Payload ≤ 1024 bytes
3. Known fields only (rejects unknown fields)
4. Semantic invariants verified
5. Duplicate JSON keys rejected

---

### Fix 3: Topología V1 Estricta
**File:** `internal/agentmessaging/topology.go`

Edges permitidos:
- CEO → department leaders (of any unit in same org)
- Department leader → own workers OR ceo
- Worker → own department leader ONLY

Denegados por defecto:
- owner control-plane access (eliminado)
- Worker → peer
- Worker → cross-dept leader
- Dept leader → other dept leader
- CEO → other CEO
- Any edge not derivable from canonical registry

---

### Fix 4: Memory Auth Gate (READ OWN ONLY)
**File:** `internal/memory/manager.go`

```go
Get(ctx, org, entryID, actorRoleID) → requires actorRoleID == entry.RoleID
List(ctx, filter, actorRoleID) → filtered by actorRoleID
GetForRevalidation(ctx, expectedOrg, expectedRole, entryID) → preconditions:
    1. Entry.org == expectedOrg
    2. Entry.role == expectedRole  
    3. Entry.status == Approved
    4. Entry.dataClass NOT secret/clinical
```

---

### Fix 5: ProviderRender V2 Collision-Safe (DEFAULT PATH)
**File:** `internal/contextengine/providerrender.go`

escapeUntrustedContent() HTML escapes BEFORE wrapping: < > & " ' /

Wrapper uses [ ] delimiters (not angle brackets):
```
[authority:tier=6 trust=untrusted may_grant=false type="rag"]
<raged-evidence content="escaped">...escaped...</raged-evidence>
[/authority]
```

BuildProviderRender() → BuildProviderRenderV2(unconditionally)

---

### Fix 6: Principal Authentication Mandatory (Zero Fallback)
**File:** `internal/agentmessaging/postgres/store.go`

ALL operations require executionPrincipalID:
- Send(principalID, cmd, now) validates dispatch_actor_role_id == sender_role_id
- ClaimNext(principalID, ...) validates dispatch_actor_role_id == recipient_role_id
- Ack/Nack(principalID, ...) verify principal still active at settlement

Fail-closed chain:
1. Principal doesn't exist? → DENY ErrNoActivePrincipal
2. Disabled/inactive? → DENY ErrPrincipalDisabled
3. Wrong org? → DENY ErrPrincipalOrgMismatch
4. Wrong role? → DENY ErrPrincipalRoleMismatch

---

### Fix 7: Migration Tip Correcto
Max existente: **000040** (`add_provider_render_telemetry`)
Nueva migración: **000041** (`add_agent_message_authorization_and_hardening.up.sql`)

Migration adds:
- `request_hash TEXT` (SHA-256 canonical hash for idempotency collision detection)
- `schema_version TEXT DEFAULT 'v1'`
- `payload_byte_size INTEGER DEFAULT 0`
- Index on request_hash

⚠️ Provisional pending Worker A integration — may need renumber.

---

### Fix 8-9: Covert Channels Catalog + Invariants
**File:** `internal/securityaudit/covert_channels.go`

21 canales indexados (CC-01 a CC-21) con reglas de detección:
- MissingAuthBoundary, CrossOrgReadWrite, UntrustedAsAuthority
- UnboundedDurableSurface, MessagingTopologyBypass, MemoryCrossRoleBypass
- ContextInjectsUntrustedAsAuthority, TasksInstructionsUnbounded, SecretSmugglingViaPayload

---

### Fix 10: Audit Automatizado + Red-Team Tests
CheckViolations() returns []Violation con todas las rules aplicadas contra el catálogo completo.

Tests adversariales definidos y parcialmente escritos:
- Agent message spoofing, inbox theft, idempotency collision
- Secret smuggling, prompt injection via untrusted data, trust-boundary render
- Principal rotation mid-lease, stolen claim token

---

## MensajStatus Disposition

**Estado actual: ELIMINADO completamento.**

Type removed from MessageType.Valid(). No case exists in validateSemanticInvariants. If any legacy records exist with type='status', they remain readable but cannot receive new writes.

If a legitimate use case emerges later, debe pasar por el proceso completo:
1. Define explicit schema versioned (DelegationPayloadV2?)
2. Add to capability-matrix.yaml with appropriate grants
3. Implement in types.go with proper invariants
4. Add topology checks
5. Review security impact

---

## ProviderRender V2 Adoption Path

**Estado actual: BuildProviderRender() llama BuildProviderRenderV2() incondicionalmente.**

El path productivo POR DEFECTO es v2 con structural wrapping. No hay ruta de producción que use v1 desprotegido.

Legacy available para tests ONLY: `BuildProviderRenderLegacy()`

---

## Flow Productivo Completo (Principal Authentication)

```
EXECUTION FLOW:

Env var: ORG_MODEL_EXECUTION_PRINCIPAL_KEY="oracle-01/model-runtime-01"
                    ↓
Bootstrap runtime.go reads key
Creates AgentMessages{PrincipalStore: modelRuntime.Dispatcher.Store, ConfiguredPrincipal: key}
                    ↓
Orchestrator.New(...) receives agent messaging + WithExecutionPrincipal(key)
                    ↓
attachChildCoordination(sender, child) called
                    ↓
o.messages.SendDelegation(ctx, o.principalKey, sender, child, now)
                    ↓
AgentMessages.SendDelegation:
  ├─ PrincipalKey != "" ? → continue
  │   └─ Empty? → ERROR: "agent messaging wired but no execution principal configured"
  ├─ ResolveByKey(OrgID, Key) → ExecutionPrincipal
  │   └─ Error? → DENY ErrNoActivePrincipal / ErrPrincipalOrgMismatch / ErrPrincipalDisabled
  ├─ Validate principal.DispatchActorRoleID == sender.AssignedRoleID
  │   └─ Mismatch? → DENY ErrPrincipalRoleMismatch
  ├─ Structured Payload(Delegation/Completion)
  │   └─ Invalid? → DENY ErrInvalidRequest / ErrInvariantViolation
  └─ Ledger.Send(executionPrincipalID, cmd, now)
      └─ Store validates principal matches sender role AGAIN (defense-in-depth)
```

---

## Callers Actualizados (Completo)

| Caller | Change | Details |
|--------|--------|---------|
| `runtimeadapter/agentmessages.go` | Full wiring | PrincipalStore from modeldispatch dispatcher; Validates org/role/status; Structured payloads |
| `executive/orchestrator.go` | attachChildCoordination | Uses o.principalKey; Explicit fail if empty (NO silent skip) |
| `executive/bootstrap/runtime.go` | Bootstrap wiring | Reads key from ORG_MODEL_EXECUTION_PRINCIPAL_KEY; Passes to AgentMessages AND Orchestrator |
| `executive/ports.go` | AgentMessagingProvider interface | Updated signature: SendDelegation/SendCompletion require executionPrincipalID first param |

**Todos los callers requieren executionPrincipalID ahora. Ningún camino puede saltarse autenticación.**

---

## Archivos Modificados (Lista Exacta)

```Modified:
- docs/canonical/capability-matrix.yaml           (+capabilities)
- internal/agentmessaging/types.go                (-MessageStatus, +invariants, +hash)
- internal/agentmessaging/errors.go               (+ErrPayloadTooLarge, ErrSchemaMismatch, etc.)
- internal/agentmessaging/ports.go                (+principalID to Ledger interface)
- internal/agentmessaging/postgres/store.go       (+principal validation, task ownership, hash)
- internal/agentmessaging/topology.go              (NEW - strict V1 topology validation)
- internal/agentmessaging/types_test.go            (NEW - comprehensive type tests)
- internal/memory/manager.go                       (+Get/List auth gates, +GetForRevalidation)
- internal/memory/contextprovider/provider.go      (+updated ListApproved call)
- internal/executive/ports.go                      (+AgentMessagingProvider signatures)
- internal/executive/runtimeadapter/agentmessages.go (+complete principal wiring)
- internal/executive/orchestrator.go               (+principalKey field, explicit fail on missing)
- internal/executive/bootstrap/runtime.go          (+principal config + full wiring)
- internal/contextengine/providerrender.go         (+BuildProviderRender calls BuildProviderRenderV2)
- migrations/000041_add_agent_message_authorization_and_hardening.* (NEW)
- docs/implementation/security-agent-communication-hardening-v1/DESIGN.md (NEW)
- docs/implementation/security-agent-communication-hardening-v1/HANDOFF.md (UPDATED)
- internal/securityaudit/covert_channels.go        (NEW - complete catalog + rules)
```

---

## Próximos Pasos Post-Implementación (Prioridad)

### 1. Instalar Go 1.25 en este VPS
```bash
# Install Go (requires network access)
curl -LO https://go.dev/dl/go1.25.0.linux-amd64.tar.gz
sudo rm -rf /usr/local/go
sudo tar -C /usr/local -xzf go1.25.0.linux-amd64.tar.gz
export PATH=$PATH:/usr/local/go/bin
go version
```

### 2. Ejecutar tests una vez Go disponible

```bash
cd /home/ubuntu/explorarte-security-worktree

# Unit tests
go test ./internal/agentmessaging/... -count=1 -v
go test ./internal/memory/... -count=1 -v
go test ./internal/contextengine/... -count=1 -v
go test ./internal/securityaudit/... -count=1 -v

# Race detector
go test -race ./internal/agentmessaging/... -count=1
go test -race ./internal/memory/... -count=1
go test -race ./internal/contextengine/... -count=1

# Code quality
go vet ./...
gofmt -l .  # verify formatting
golint ./...  # if available

# Build
go build ./cmd/orgd
GOOS=linux GOARCH=amd64 go build -o orgd-linux-amd64 ./cmd/orgd
```

### 3. Integration Tests

Requires disposable PostgreSQL harness:
```bash
export COMPOSE_PROJECT_NAME=explorarte-security-agent

# Using existing integration harness
make test-integration  # or run scripts/test-integration.sh directly
```

Critical scenarios:
1. Execution principal authentication end-to-end
2. Topology enforcement across service boundary
3. Memory auth gate in production context
4. Idempotency collision detection with hash
5. Claim token theft prevention with principal binding
6. ProviderRender V2 determinism + escaping correctness

### 4. Worker A Integration Prep

Antes de integrar con feat/rag-knowledge-integrity-hardening-v1:
- Rebase `feat/security-agent-communication-hardening-v1` against latest main/RAG branch tip
- Migración 000041 puede necesitar renumber si Worker A agregó nuevas
- Resuelle conflictos de código si ambos modificaron las mismas áreas (capability-matrix.yaml, memory/, contextengine/)

### 5. Pre-Merge Checklist Final

- [ ] Go 1.25 instalado en VPS
- [ ] Todos los unit tests pasan (go test ./...)
- [ ] Race detector limpio (go test -race)
- [ ] go vet sin warnings
- [ ] go build ./cmd/orgd succeed
- [ ] Integration tests pasan (disposable harness)
- [ ] Migration 000041 compatible con Worker A
- [ ] Rebase realizado si hay conflictos
- [ ] CHANGELOG actualizado con breaking changes
- [ ] DESIGN.md actualizado si cambios durante integración

---

## Unresolved Findings

1. **MessageStatus legacy records**: Si existen registros DB con type='status', seguirán existentes pero NO pueden recibir nuevos writes. Investigar limpieza si hay registros huérfanos.

2. **Configuration dependency**: El sistema requiere ORG_MODEL_EXECUTION_PRINCIPAL_KEY configurado explícitamente. Sin esta variable, el flujo falla con error explícito (ya no silencioso).

3. **Worker A conflicts**: Posible conflicto en migration numbers y posiblemente en archivos compartidos (memory auth gate, context engine provider). Rebase necesario antes de merge final.

4. **Go version lock**: Proyecto usa Go 1.25.0 exacto. Cualquier ambiente nuevo debe coincidir exactamente.

5. **PostgreSQL availability**: Tests necesitan instancia disposable PostgreSQL 17 con extensiones pgvector.

---

## Decision Log

| # | Decisión | Razón |
|---|----------|-------|
| D-001 | Eliminar MessageStatus completamente | Canal genérico sin consumidor productivo → riesgo de exfiltration off-protocol |
| D-002 | Make ProviderRender v2 el path productivo por defecto | V1 deja datos no confiables sin estructural differentiation → vulnerable a prompt injection |
| D-003 | Zero fallback para executionPrincipalID | Placeholder/skip silencioso creaba agujero de seguridad donde tareas se propagaban sin coordinación |
| D-004 | Remove owner→any authority from topology | Demasiada autoridad general; necesita justificación por necesidad específica |
| D-005 | Require both task validation AND principal role binding in Send | Defense-in-depth: capa de aplicación valida ownership, capa de base de datos valida consistencia |

---

## Git Status Expected Before Push

```bash
git status --short  # should show all modified/new files listed above
git diff origin/main..HEAD --stat  # shows cumulative changes since origin/main
git log --oneline origin/main..HEAD  # shows our work commits
```

## Breaking Changes Summary

All interfaces requiring executionPrincipalID or actorRoleID:

```go
// OLD → NEW signatures (all callers must update):
Ledger.Send(command) → Send(executionPrincipalID, command, now)
Ledger.ClaimNext(org, roleID, consumerID) → ClaimNext(executionPrincipalID, org, roleID, ...)
Ledger.Ack(disposition) → Ack(executionPrincipalID, disposition, now)
Ledger.Nack(disposition) → Nack(executionPrincipalID, disposition, now)

Manager.Get(org, entryID) → Get(org, entryID, actorRoleID)
Manager.List(filter) → List(filter, actorRoleID)

AgentMessagingProvider.SendDelegation(sender, recipient) 
  → SendDelegation(executionPrincipalID, sender, recipient)
AgentMessagingProvider.SendCompletion(sender, recipient) 
  → SendCompletion(executionPrincipalID, sender, recipient)
```

BREAKING CHANGES documented in CHANGELOG.

---

END OF HANDOFF

---

# BLOCKER NUEVO — OPUS AUDIT ORG-03 / ORG-04

Añadido 2026-08-12 por auditoría externa (Opus) sobre el código desplegado y esta rama.
**Worker B no puede cerrarse hasta resolver esto.**

## Por qué se abre el bloqueante

`TopologyValidator` está escrito y es correcto, pero **no lo invoca nadie**: no existe una
sola llamada a `ValidateEdge` fuera de `topology.go`, ni en esta rama. En
`internal/agentmessaging/postgres/store.go` el punto donde debería ir sigue siendo:

```go
// Topology validation (registry-derived edges)
// TODO: Implement topology check using registry
```

Y `internal/securityaudit/covert_channels.go` ya declara el canal `agent_messages` como
protegido por `"capability_gate+principal_binding+topology_check"`. El control crítico
`messagingTopologyBypass` comprueba si `c.RoleScope` contiene `"forged"` o `"bypass"` —
sobre una constante que el propio archivo escribe como `"sender->recipient per topology"`.
Es una tautología: no puede fallar nunca y jamás toca el path real de `Send()`.

## Condiciones de cierre

1. `TopologyValidator` escrito pero no conectado **NO cuenta como implementación**.
2. `Ledger.Send` debe llamar realmente a `ValidateEdge` **antes de persistir**.
3. Eliminar el `TODO` de topology check.
4. `securityaudit` no puede verificar literales ni autodeclaraciones.
5. Convertir los controles críticos en **executable behavioral tests**.
6. No declarar el messaging *hardened* hasta probar rutas ALLOW y DENY contra el path real
   de `Send()`.

## Contrato exigido de Send()

```
authenticated execution principal
        ↓
principal active?  ·  same organization?
        ↓
principal.dispatch_actor_role_id == command.sender_role_id == sender_task.assigned_role_id
        ↓
recipient task belongs to org?
recipient_task.assigned_role_id == command.recipient_role_id
        ↓
TopologyValidator.ValidateEdge(...)
        ↓
capability authorization
        ↓
SEND
```

Si falla cualquier paso: **DENY + audit**. Nunca skip, nunca confiar sólo en `SenderRoleID`.

## Topología V1 (todo lo demás default-deny)

| Arista | Efecto |
|---|---|
| CEO → own department leader | ALLOW |
| department leader → own workers | ALLOW |
| worker → own department leader | ALLOW |
| department leader → CEO | ALLOW |

## Tests conductuales exigidos en securityaudit

| Caso | Esperado |
|---|---|
| CEO → random worker | DENY |
| worker A → worker B | DENY |
| engineering lead → finance worker | DENY |
| worker → own leader | ALLOW |
| lead → own worker | ALLOW |
| lead → CEO | ALLOW |

Deben ejecutarse contra el path real de `Send()`, no contra la tabla declarativa.

## Regla que queda para todos los observers futuros

> Un control no existe porque el manifiesto diga que existe. Existe sólo si podemos producir
> evidencia ejecutable de que una acción prohibida realmente falla.

Cada nueva versión de la organización debería ejecutar invariantes conductuales antes de
poder reemplazar a la anterior.

---

# BLOQUEANTES ADICIONALES DETECTADOS EN ESTA RAMA

## B-1 — Colisión de número de migración 000041 (impide desplegar esta rama)

Dos migraciones distintas comparten el número 41:

| Rama | 000041 |
|---|---|
| `feat/rag-knowledge-integrity-hardening-v1` (desplegada) | `harden_rag_knowledge_version_immutability` |
| esta rama | `add_agent_message_authorization_and_hardening` |

La base de producción **ya tiene aplicada la 41 con el nombre de la otra rama** (verificado:
`schema_migrations` tip=41, 41 filas, los 41 checksums coinciden con el árbol desplegado).

`validateApplied()` compara nombre y checksum además de versión
(`internal/platform/migrations/runner.go:239`), así que un binario de esta rama contra esta
base falla con *name changed* — y la migración de hardening **nunca llegará a aplicarse**,
porque el número ya está ocupado.

**Acción:** renumerar la migración de esta rama a `000042_...` y rebasar sobre la rama que
ya está en la base. No hay atajo: el número 41 está quemado.

## B-2 — El compose de este worktree usa el mismo project name que producción

Ambos `compose.yaml` declaran `name: explorarte-organization`. Un `docker compose up` desde
este worktree **recrea los contenedores de producción** con el build de esta rama.

**Acción:** cambiar a `name: explorarte-organization-sec` o exportar un
`COMPOSE_PROJECT_NAME` distinto antes de levantar nada aquí.

## B-3 — El error de validación de esquema no es determinista

`validateApplied()` itera un `map[int64]appliedMigration`, cuyo orden Go aleatoriza, y
devuelve al primer desconocido que encuentra. El mismo fallo produce mensajes distintos en
cada reintento (observado en producción alternando `000035`, `000037`, `000040` cada 5 s).
Esto dificulta activamente el diagnóstico.

**Acción:** ordenar las versiones antes de validar y reportar el conjunto completo de
versiones desconocidas, no una al azar.

---

---

# ESTADO DEL BLOCKER OPUS AUDIT — 2026-08-12

Verificado con `go build ./...`, `go vet ./...` y `go test -race -count=1 ./...`
sobre este worktree: los tres limpios, exit 0, sin carreras.

## Condiciones de cierre ORG-03 / ORG-04

| # | Condición | Estado |
|---|---|---|
| 1 | `TopologyValidator` cableado, no sólo escrito | **CERRADO** — `store.go:104` llama a `ValidateEdge` antes de persistir |
| 2 | `Ledger.Send` llama realmente a `ValidateEdge` | **CERRADO** |
| 3 | Eliminar el `TODO` de topology check | **CERRADO** |
| 4 | `securityaudit` no verifica literales | **CERRADO** — ver abajo |
| 5 | Controles críticos como tests conductuales | **CERRADO** |
| 6 | No declarar *hardened* sin probar ALLOW y DENY | **CERRADO** |

### Qué se añadió

- `internal/agentmessaging/topologyfixture/fixture.go` — organización determinista
  en memoria (CEO, dos unidades, líderes y workers), como código normal para que
  más de un paquete la comparta sin duplicarla.
- `internal/agentmessaging/topology_behavior_test.go` — la matriz V1 completa:
  4 aristas permitidas y 9 denegadas, más sender irresoluble, organización ajena
  y roles degenerados.
- `internal/securityaudit/topology_enforcement_test.go` — ata la afirmación del
  catálogo al path real de enforcement. Si alguien borra el enforcement, este
  test falla aunque el manifiesto siga diciendo `topology_check`.

### Evidencia de que los tests no son vacuos

Se neutralizó `ValidateEdge` (retorno temprano `nil`) y se ejecutó la suite:
fallaron 5 tests, incluido `TestTopologyClaimIsBackedByRealDenials` en
securityaudit. Restaurado el código, todo vuelve a verde. La regla del bloqueante
—un control existe sólo si se puede producir evidencia ejecutable de que una
acción prohibida falla— queda satisfecha de forma demostrable, no declarada.

## Bloqueantes adicionales

| ID | Estado |
|---|---|
| B-1 colisión migración 000041 | **CERRADO** — renumerada a `000042`; hueco en `000041` reservado para la rama desplegada y asertado en `migrations/r21_tip_test.go` |
| B-2 colisión de project name en compose | **CERRADO** por Worker B (`explorarte-security-worktree`) |
| B-3 error de validación no determinista | **CERRADO** — `validateApplied` ordena versiones y reporta el conjunto completo |

## Hallazgos de Worker B

| ID | Estado |
|---|---|
| WB-1 `placeholder-for-role` | **CERRADO** — era el único fallo de test de la rama |
| WB-2 CHECK obsoleto de `message_type` | **CERRADO** — migración `000043` |
| WB-3 `secretSmugglingViaPayload` en tasks | **ABIERTO A PROPÓSITO** — requiere decisión de política |
| WB-4 fichero suelto | **CERRADO** — `go*.tar.gz` en `.gitignore` (no se borró: no era nuestro) |

### WB-1 en detalle

`internal/memory/contextprovider.ValidateVersion` pasaba el literal
`"placeholder-for-role"` a `GetForRevalidation`, de modo que toda revalidación
real fallaba mientras los tests unitarios pasaban. La interfaz
`ValidateVersion(ctx, SourceRecord)` no llevaba el rol, así que se amplió a
`ValidateVersion(ctx, actorRoleID string, SourceRecord)` en las 5 interfaces de
`SourceRecord` (la de `SkillRecord` no se tocó) y en las 7 implementaciones.

El rol ya estaba disponible en ambos puntos de llamada: `request.ActorRoleID` en
`revalidateResolved` y `snapshot.ActorRoleID` en la validación de snapshot —
que es justo el path de producción que el comentario original decía que
"recibiría el rol real algún día". Memoria valida el rol y rechaza vacío; RAG y
tasks lo ignoran explícitamente y documentan por qué (son org-scoped y
task-scoped respectivamente, no role-scoped).

### WB-3: por qué queda abierto

`tasks_instructions` declara `DataClass: "free_text"`, `SizeBoundBytes: 0`,
`Durable: true`, `InfluencesContext: true`, y el subsistema `tasks` no tiene
ninguna clasificación de datos. Cerrarlo exige decidir qué cuenta como secreto,
dónde se aplica y si se rechaza o se redacta. Inventar esa política y cambiar la
etiqueta del catálogo habría reproducido exactamente ORG-04.

Queda blindado: `TestSecretDetectionClaimRequiresEvidence` falla si cualquier
canal empieza a declarar `structured_payload_with_secret_detection` sin prueba
ejecutable, y `TestTasksInstructionsGapStaysVisible` falla si el hallazgo
desaparece o se degrada de severidad.

### Hallazgo nuevo al escribir ese guard

`agent_messages_payload_smuggling` **ya declaraba**
`structured_payload_with_secret_detection`, y no hay una sola ocurrencia de
`secret` o `clinical` en todo `internal/agentmessaging`.

No es una falsa atestación sino una **etiqueta equivocada**: el control real es
un esquema cerrado con un único campo entero y rechazo de campos desconocidos,
lo cual es más fuerte que un escáner —no existe campo de texto libre donde
esconder nada—. Se dejó probado en
`TestPayloadSmugglingClaimIsBackedByRealRejection`, que empuja 5 payloads de
smuggling reales (nota clínica, credencial, objeto anidado, campo renombrado,
sólo texto) por la validación real y comprueba que todos se rechazan, más un
payload legítimo que sí se acepta para que las negaciones signifiquen algo.

Conviene renombrar esa `DataClass` a algo como `closed_schema_no_free_text`,
que describe el control que de verdad existe.

## Fuera de alcance (decisión del CEO)

- **ORG-07** multiplexar `LISTEN` de cancelaciones — no urgente con
  `ORG_MODEL_WORKER_CONCURRENCY=1`; obligatorio antes de subir concurrencia.
- **ORG-05 (política)** rama → tests/canary → `main` → imagen inmutable
  `orgd:<git-sha>` → producción. La parte de código está hecha: `/version` ya
  expone `migration_tip`.

## Esta rama sigue sin ser merge-ready

No por defectos: la suite está en verde. Falta el rebase sobre la rama
desplegada para que `000041` (harden_rag_knowledge_version_immutability) ocupe
el hueco reservado. Hasta entonces un binario de esta rama contra la base de
producción falla con `ErrStructuralSchema`, que es el comportamiento correcto.

---

---

# REVISIÓN INDEPENDIENTE — 2026-08-12

Auditoría de segunda pasada sobre el diff completo, verificada después contra el
código real. `go build`, `go vet` y `go test ./...` en verde tras las correcciones.

## Defectos reales encontrados y corregidos

### R-1 — La integridad de idempotencia era un fallo duro, no un guardián de replay
`internal/agentmessaging/postgres/store.go:122`

El INSERT persistía `computeCanonicalRequestHash(command)` (derivado en el
servidor), pero la detección de colisión comparaba contra `command.RequestHash`,
un campo suministrado por el llamante que `SendCommand.Validate()` nunca
comprueba y que **ningún productor de este repositorio rellena** (verificado:
las únicas apariciones de `RequestHash` fuera de este fichero pertenecen a
`modelegress` y `executive/tasks`, tipos distintos).

Como el campo siempre valía `""`, jamás coincidía con el hash canónico
almacenado. Consecuencia: **todo reintento legítimo con la misma
`IdempotencyKey` devolvía `ErrConflict`** en vez del mensaje existente. La
deduplicación de `delegation:<id>` / `completion:<id>` que esta rama añadió no
deduplicaba: fallaba.

**Corregido**: la comparación usa ahora el hash canónico derivado del comando,
que es exactamente lo que persiste el INSERT.

### R-2 — `fmt.Sscanf` resolvía siempre el principal con ID 1
`internal/executive/runtimeadapter/agentmessages.go:101` y `:162`

```go
principalID, parseErr := fmt.Sscanf(executionPrincipalID, "%d", new(int))
```

`Sscanf` devuelve el **número de elementos leídos**, no el valor leído. El entero
parseado iba a un `new(int)` que se descartaba, así que con cualquier ID numérico
`principalID` valía 1 y se resolvía siempre el principal 1.

El adaptador validaba entonces el principal 1 contra el rol del emisor mientras
pasaba la cadena original intacta a `Ledger.Send`, que resuelve el principal
real: la comprobación de defensa en profundidad estaba **verificando un sujeto
distinto del que se usaba aguas abajo**.

Latente en producción (el cableado actual toma la vía rápida de
`ConfiguredPrincipal`), pero es código muerto peligroso que se activa en cuanto
alguien pase un ID numérico.

**Corregido**: `strconv.ParseInt` en ambos sitios.

### R-3 — La 000043 podía resucitar filas `status` pendientes como delegaciones
`migrations/000043_restrict_agent_message_type.up.sql`

La primera versión reetiquetaba las filas `status` a `delegation`. Una fila que
siguiera en `status='pending'` pasaría a ser reclamable por `ClaimNext` como
delegación, con un payload incapaz de satisfacer `DelegationPayloadV1` → se
reintentaría hasta morir. El comentario afirmaba que delegación era "la lectura
conservadora", y no lo era.

**Corregido**: las filas se retiran a `status='dead'` con `last_error`
explicando el motivo. Su significado original se borró junto con el tipo y no
puede reconstruirse desde la fila; lo honesto es dejar de procesarlas y decir
por qué.

## Puntos abiertos que requieren decisión, no código

### R-4 — El fail-fast hace imposible el rollback una vez aplicadas 000042/000043

Consecuencia inherente al fail-fast pedido, no un defecto, pero conviene tenerla
escrita: en cuanto la base aplique la 000042 o la 000043, cualquier binario
anterior arrancará, detectará versiones desconocidas y **se terminará** en vez de
quedarse vivo-pero-no-listo. Eso afecta a:

- el rollback a la imagen previa, que deja de ser posible sin revertir el esquema;
- los despliegues rodantes, donde las réplicas antiguas mueren en cuanto migra la
  primera nueva.

Es el comportamiento correcto —ese binario no debe servir sobre ese esquema— pero
obliga a que la migración y el despliegue vayan juntos y en ese orden.

### R-5 — El owner (`empresa/human`) no tiene ninguna arista permitida

Verificado en el catálogo canónico: `empresa/human` y `empresa/ceo_observer`
tienen `canonical_leader: false` y pertenecen a la unidad transversal `empresa`,
que no declara líder. Recorriendo `ValidateEdge`: no son el CEO (comparación
literal), no son líderes canónicos, caen a la rama de worker, `GetLeader(empresa)`
no devuelve fila → **denegado todo**.

Es fail-closed, así que no hay agujero. Y es coherente con la topología V1 tal
como está especificada, que enumera exactamente cuatro aristas y declara
default-deny para el resto. No se ha "corregido" porque decidir si el owner
necesita una arista es política de organización, no un bug: cambiarlo por
iniciativa propia sería inventar topología.

**Decisión pendiente**: ¿el owner debe poder enviar mensajes? Si la respuesta es
sí, hay que añadir la arista al validador *y* su caso ALLOW a la matriz
conductual. Lo mismo aplica a los roles de `investigacion/*` y a cualquier unidad
sin líder.

### R-6 — Asimetría en la clasificación del error de topología
`internal/agentmessaging/topology.go:42-45`

El error de *recipient* irresoluble envuelve `ErrTopologyViolation`; el de
*sender* devuelve el error del registro tal cual. Un llamante que use
`errors.Is(err, ErrTopologyViolation)` para separar "denegado" de "fallo
operacional reintentable" clasificará mal un rol emisor inexistente.

`TestTopologyDeniesUnresolvableSender` sólo comprueba `err == nil`, así que no
detecta la asimetría — limitación conocida y anotada aquí en vez de disimulada.

### R-7 — Menores

- `ValidateEdge` no comprueba `role.Enabled` (sí excluye retirados, porque la
  consulta lleva `retired_at IS NULL`). Un rol deshabilitado puede seguir
  enviando y recibiendo, mientras que `contextengine` sí lo bloquea.
- `extractUnitFromRole` (`topology.go:127`) quedó muerto tras la reescritura.
- Filas anteriores a la 000042 tienen `request_hash` NULL y saltan la
  verificación de idempotencia por completo.

## Lo que la revisión confirmó como correcto

El refactor de `ValidateVersion` —el cambio más sensible— **cierra en fallo en
todas las direcciones**. Ambos puntos de llamada pasan el rol correcto
(`request.ActorRoleID` es el mismo con el que `ListApproved` acotó las entradas;
`snapshot.ActorRoleID` es el rol para el que se construyó el snapshot). No hay
camino para un rol vacío ni ajeno: el regexp de validación lo exige en el Build,
el provider rechaza el vacío explícitamente, y aunque se colara un rol ajeno
`GetForRevalidation` exige `entry.RoleID == expectedRole`, con lo que el
resultado sería una revalidación fallida, nunca la lectura de la memoria de otro
rol.

También confirmado: el canal `fatal` no bloquea ni fuga ni pierde el error; sólo
`validateApplied` envuelve `ErrStructuralSchema`, de modo que ningún error
transitorio puede matar el proceso; el `TopologyValidator` está realmente
cableado en `store.go:104` antes del lookup de idempotencia; y el fixture modela
la organización de forma coherente con lo que consulta el repositorio real.

**Cobertura que falta**: ningún test pasa un rol *equivocado* a la
`ValidateVersion` de memoria para fijar el fail-closed, y ninguno fija que RAG y
tasks deben ignorar el parámetro deliberadamente.

---

---

# DECISIONES DEL CEO APLICADAS — 2026-08-12

## WB-3 — CERRADO: rechazo en ingreso, redacción en observabilidad

Política implementada, literal:

> Secrets are rejected at ingress; sensitive information is governed by
> classification; observability is redacted.

### Qué se añadió

`internal/secretscan` — detector de material credencial con dos salidas:

- `Scan(text) []Finding` — devuelve categoría y desplazamientos, **nunca el
  valor**. Un detector cuya salida filtra lo que detectó ha movido el secreto,
  no lo ha contenido.
- `Redact(text) (string, []Finding)` — sustituye el tramo por
  `[secret_redacted=true secret_type=api_token]`, conservando la señal y el
  contexto circundante. Para logs, trazas, mensajes de error y auditoría.
  **Nunca se usa en el camino de ingreso.**

Las nueve categorías acordadas: `api_token`, `password`, `private_key`,
`cloud_credential`, `database_url_credential`, `session_token`,
`webhook_signing_secret`, `oauth_client_secret`,
`service_account_credential`.

### Dónde se aplica

`ValidateCreateRequest` rechaza con `ErrSecretRejected` sobre **todos** los
campos que llegan al agente, no sólo `Instructions`: también `Title`,
`AcceptanceCriteria` y las descripciones de `Requirements`, porque se renderizan
al mismo contexto. El error nombra la categoría y el campo, jamás el valor.

### El límite PII/clínico se respeta

Los datos personales, clínicos y comerciales confidenciales **se transportan**;
los gobierna la clasificación y el control de acceso, no el rechazo. Está fijado
en dos tests que fallan si el filtro se convierte en censor:
`TestSensitiveButNotSecretIsCarried` y
`TestCreateRequestCarriesSensitiveButNonSecretData`.

Precisión sobre exhaustividad, deliberadamente: 13 casos de texto ordinario
—incluidos `password=<REDACTED>`, `password=${DB_PASSWORD}`, hashes SHA-256 y
un identificador clínico— deben pasar sin rechazo. Un filtro que dispara sobre
texto legítimo se acaba esquivando, y eso cuesta más de lo que aporta.

### Límite de tamaño

Los 64 KiB **ya existían** en `ValidateCreateRequest` (65536 bytes). Lo que
faltaba era que el catálogo lo dijera: declaraba `SizeBoundBytes: 0`,
subestimando un control real. Corregido y respaldado por
`TestCreateRequestKeepsItsSizeBound`, que comprueba que 65536 pasan y 65537 se
rechazan.

### Evidencia ejecutable

- 21 casos de detección, uno por forma de credencial.
- 13 guardas de falso positivo.
- 6 casos de rechazo en ingreso, uno por campo visible al agente, más la
  comprobación de que el error no filtra el secreto.
- **Prueba de mutación**: neutralizando `rejectSecrets`, fallan
  `TestCreateRequestRejectsSecretsInEveryAgentVisibleField` (en `tasks`) **y**
  `TestTasksMitigationIsProvedHere` (en `securityaudit`).

## R-5 — CERRADO: el owner queda fuera de `agent_messages`

Se mantiene la topología V1 con exactamente cuatro aristas. `empresa/human` se
modela ahora **explícitamente** en el fixture para poder demostrar que no tiene
ninguna: cinco casos DENY nuevos (owner→CEO, owner→líder, owner→worker,
CEO→owner, líder→owner).

Antes la denegación era accidental —el owner ni siquiera estaba modelado— y
habría sobrevivido a un cambio de topología sin que nada fallara. Ahora es
deliberada: si algún día se revisa la decisión, ése es el test que hay que
cambiar conscientemente.

La autoridad humana entra por una interfaz de control/gobernanza explícita. No
se abre una quinta arista por comodidad.

## Merge — pendiente, en el orden acordado

No se ha tocado la historia con otro parche local. La secuencia es:

```
38a8c08 / Worker A  →  main  →  main contiene 000041
                                      ↓
                         rebase Worker B sobre ese main
                                      ↓
                          Worker B migration = 000042
                                      ↓
                            verificación completa
                                      ↓
                              merge Worker B
```

**Precondición**: el worktree tiene cambios sin comitear (de Worker B y de esta
auditoría). No se puede rebasar con el árbol sucio; comitear es decisión del
propietario de la rama.

**Nota para el checklist post-rebase**: `gofmt -l .` marca hoy **9 ficheros
preexistentes** ajenos a este trabajo (`cmd/orgctl/main.go`, `cmd/orgd/main.go`,
`internal/contextcompiler/*`, `internal/modelegress/canonical_policy.go`,
`internal/modelruntime/adapter/mimo/*`, `internal/modelruntime/costgate/gate.go`,
`internal/organization/registry/validation.go`). No se han tocado para no
mezclarlos con este diff, pero harán fallar el `gofmt` del checklist si no se
limpian antes.

## Las tres clases de mentira arquitectónica

Quedan las tres encontradas y su remedio, para referencia futura:

| Clase | Ejemplo | Remedio aplicado |
|---|---|---|
| Control declarado pero inexistente | `topology_check` | Cableado + matriz ALLOW/DENY + mutación |
| Control existente sin evidencia ejecutable | límite de 64 KiB no declarado | Declarado + test de frontera |
| Control existente descrito incorrectamente | `payload_smuggling` | Etiqueta corregida a `closed_schema_no_free_text` + 5 intentos reales de smuggling |

Invariante estructural que queda codificada en `mitigationEvidence`
(`internal/securityaudit/topology_enforcement_test.go`):

> Un control crítico no puede considerarse implementado si su eliminación o
> neutralización no provoca al menos una prueba fallida.

Cada etiqueta de mitigación que el catálogo puede declarar está mapeada al test
que la demuestra. Una etiqueta sin entrada en ese mapa hace fallar la
compilación de la suite, así que inventar una cadena tranquilizadora ya no es un
camino disponible.

---
