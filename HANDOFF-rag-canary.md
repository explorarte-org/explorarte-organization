# HANDOFF — Canary RAG (Fase 15) — 5 candidatos esperando tu revisión

## Qué se hizo
Se activó el rol `investigacion/research_worker_hourly` (único rol de la
organización con permiso `rag.propose_candidate`; estaba
`proposed_profile_required`/deshabilitado desde antes de esta sesión —
sin él, ningún rol podía proponer nada al RAG). Aprobaste activarlo
explícitamente antes de que lo tocara.

Con el rol activo, se propusieron 5 documentos reales del corpus ya
subido a Object Storage (uno por curso, tamaños chicos para revisión
rápida) como candidatos RAG vía `orgctl rag propose`. Confirmado
directamente en Postgres (`rag_knowledge_versions`, `lifecycle=candidate`,
`revision=1` los 5).

| version_id | documento | corpus |
|---|---|---|
| `rag-canary-markdown-41ca928c7f4c` | 135 - Descargue los archivos de inicio | curso_fullstack_javascript |
| `rag-canary-markdown-1e3eb6e678dc` | 189 - JSON documentation | curso_golang_espanol |
| `rag-canary-markdown-a1e44ac7270e` | 10 - Adding attributes to graphs-01 | curso_grafos_python |
| `rag-canary-markdown-4696a28eb932` | 03 - Downloadable Resources and Tips | curso_python_100dias |
| `rag-canary-markdown-e2e3ce912d81` | 39 - Plan de Ataque | curso_reinforcement_learning_python |

## Por qué me detuve acá
`rag.publish_approved` (el paso que mueve un candidato a `approved`, lo
que lo hace realmente indexable/consultable) está marcado en
`docs/canonical/capability-matrix.yaml` como `approval: policy_or_human`.
El gate de autorización (`internal/rag/authz/gate.go`, vía el evaluador
general de `internal/authorization`) lo hace cumplir sin importar qué
permisos tenga el rol — es exactamente el checkpoint humano que el
roadmap original pedía antes de "ingesta masiva". No lo intenté saltear.

## Cómo revisar/aprobar (o rechazar) cada uno
Para cada `version_id` de la tabla, generar un JSON como:
```json
{
  "version_id": "rag-canary-markdown-41ca928c7f4c",
  "expected_revision": 1,
  "actor_role_id": "empresa/human",
  "reason": "revision de canary inicial del corpus rag_curado v4",
  "outcome": "approve"
}
```
(`outcome` puede ser `approve`, `reject`, `deprecate` o `archive`.)

Y correrlo con:
```
sudo docker compose run --rm -v /ruta/al/archivo.json:/tmp/review.json:ro \
  --entrypoint orgctl orgd rag review --file /tmp/review.json --json
```

`actor_role_id: empresa/human` es tu rol (`authority_class: owner`, tiene
`'*'` en capabilities) — probablemente el más directo para decidir esto,
aunque la policy podría requerir un flujo de `orgctl authorization
request`/`decide` explícito según cómo esté configurado
`policy_or_human` en el evaluador (no lo probé para no forzar una
aprobación sin que la vieras primero).

## Qué falta después de aprobar
- `orgctl rag reindex` sobre el namespace `department:investigacion` —
  chunkea automáticamente todo lo `approved` (usa `ChunkBody` con el
  chunker por defecto ya implementado en `internal/rag/chunking.go`, no
  hay que diseñarlo).
- `orgctl rag backfill-embeddings` — embebe los chunks pendientes (Gemini
  Batch o BGE-M3 según qué identidad de embedding esté configurada en
  `SemanticSearchDeps`; el sidecar BGE-M3 ya está corriendo y probado, ver
  `HANDOFF-bgem3-sidecar.md`).
- `orgctl rag query` para probar retrieval real sobre los 5 candidatos
  aprobados.
- Recién ahí, con el canary funcionando extremo a extremo, tendría sentido
  proponer (y eventualmente aprobar) el resto de los 1418 documentos —
  fase 16, todavía no hecha.
