# RAG/Knowledge/Memory/Embedding/Context Integrity Hardening V1 — TEST-EVIDENCE.md

Todos los comandos corridos dentro de `docker run --rm -v $(pwd):/src -w /src -e GOFLAGS=-buildvcs=false golang:1.25-bookworm` (el host del VPS no tiene `go` instalado directamente).

## Baseline (antes de modificar código)

```
go test -short ./...                    → 100% verde (todos los paquetes ok, 0 FAIL)
scripts/check-rag-fitness.sh             → PASS
scripts/check-memory-fitness.sh          → PASS
scripts/check-authorization-fitness.sh   → PASS (warning no relacionado: commit base histórico no disponible)
scripts/check-context-fitness.sh         → ERROR de tooling (regex del script + commit base no disponible) -- pre-existente, no relacionado
scripts/check-model-runtime-fitness.sh   → FAIL real pero no relacionado (adapter MiMo de R10.2 sin actualizar el allowlist del script)
scripts/check-model-egress-fitness.sh    → ERROR de tooling (commit base no disponible) -- pre-existente
scripts/check-embeddingruntime-fitness.sh → FAIL, falso positivo confirmado (grep de "API_KEY" matcheó un comentario, no una credencial real)
```

Ninguno de estos bloqueó el trabajo -- todos distinguibles como pre-existentes/no relacionados a RAG/Memory/Embedding/PDF/ObjectStorage/ProviderRender.

## Después de implementar (§6, §7, §13, §18.3, §21)

```
gofmt -l internal/embeddingruntime/ internal/objectstorage/ internal/contextengine/providerrender.go \
  internal/contextcompiler/contextcompiler_compiler.go internal/contextcompiler/contextcompiler_compiler_test.go \
  internal/rag/postgres/integration_test.go migrations/
→ sin salida (limpio)

go vet ./...
→ sin salida (limpio)

go build ./...
→ sin salida (compila limpio)

go test ./internal/embeddingruntime/... ./internal/objectstorage/... ./internal/contextengine/... ./internal/contextcompiler/... -v
→ 163 tests PASS, 0 FAIL

go test ./... (repo completo, unit + fitness embebidos, excluye integration build tag)
→ 100% ok, 0 FAIL (corrido 4 veces en distintos puntos de la sesión, siempre limpio tras cada fix)
```

## Test de integración de §21 (Postgres real, build tag `integration`)

Verificado por el subagente delegado, alternando la función del trigger entre la versión vieja (pre-000041) y la nueva:

```
con función VIEJA (pre-000041):
  UPDATE ... SET source_reference = ...            → SUCEDE (bug reproducido)
  UPDATE ... SET source_boundary = ...              → SUCEDE (bug reproducido)
  UPDATE ... SET sanitization_evidence_ref = ... (data_class=sanitized) → SUCEDE (bug reproducido)

con función NUEVA (000041):
  UPDATE ... SET namespace_id = ...                 → RECHAZADO
  UPDATE ... SET source_reference = ...              → RECHAZADO
  UPDATE ... SET source_boundary = ...               → RECHAZADO
  UPDATE ... SET sanitization_evidence_ref = ...     → RECHAZADO
  transición de lifecycle normal vía Store.Save       → PASA (sin regresión)

go test -tags integration ./internal/rag/postgres/... -run TestDatabaseRejectsMutation...
→ PASS, 7.5s
```

**Nota de contaminación de datos**: este test de integración corrió contra la DB de desarrollo compartida del VPS (no una instancia aislada), lo que causó pérdida de datos de runtime no relacionada al resultado del test en sí (ver `HANDOFF.md`, sección "Incidente"). El resultado del test (trigger funciona correctamente) es válido independientemente del incidente operacional.

## Migration tip

```
Antes de esta fase:  000040 (add_provider_render_telemetry)
Después de esta fase: 000041 (harden_rag_knowledge_version_immutability)
migrations/r21_tip_test.go actualizado para reflejar 41 migraciones / nombre correcto.
```

## Conteo de tests nuevos por finding

```
§6  Gemini vector validation:        4 tests nuevos + 3 fixtures corregidos
§7  BGE-M3 identity attestation:     fixtures actualizados en 4 archivos (adapter_test.go x1 en bgem3,
                                       bootstrap_bgem3_test.go x2 en rag/ y memory/)
§13 Object Storage immutable:        38 tests (client_test.go + keys_test.go, nuevo)
§18.3 Partition single source:       2 tests nuevos (cross-partition + byte-equivalencia)
§21 DB immutability trigger:         1 test de integración (4 rechazos + 1 aceptación)
```
