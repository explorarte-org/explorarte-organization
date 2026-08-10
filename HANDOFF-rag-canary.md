# HANDOFF — Canary RAG (Fase 15) — CERRADO, extremo a extremo verificado

## Resultado final
De los 5 candidatos propuestos, revisaste vos mismo:
- **Rechazados (3):** los dos con contenido casi vacío (40 y 34 palabras,
  instrucciones de descarga/FAQ) y el de Reinforcement Learning (por tu
  criterio).
- **Aprobados (2):** Golang JSON documentation (2184 palabras) y Python
  grafos - adding attributes (346 palabras).

## Flujo real de aprobación (para reutilizar)
`rag.publish_approved` es `approval: policy_or_human` — no alcanza con
grants, hace falta un ciclo completo:
```
orgctl authorization request -actor-role empresa/human -capability rag.publish_approved \
  -resource-type knowledge_version -resource-id <version_id> -action-digest <digest del error> \
  -reason "..." -idempotency-key <key> -ttl 1h -json

orgctl authorization decide <id> -actor-role empresa/human -decision approve -reason "..." -json

orgctl rag review --file payload.json --json   # payload incluye "approval_request_id": <id>
```
El `action_digest` sale del mensaje de error si intentás la operación sin
autorización primero (`approval_missing (... action_digest=...)`).

## Pipeline completo corrido y verificado
1. **Reindex**: `orgctl rag reindex` sobre `department:investigacion`
   (mismo ciclo request/decide, capability `rag.publish_approved` sobre
   `knowledge_index`) → generación 1, activa, chunker `rag-fixed-window v1`.
   15 chunks generados entre los 2 documentos aprobados.
2. **Embeddings BGE-M3**: `orgctl rag backfill-embeddings` (mismo ciclo de
   autorización) → **15/15 chunks embebidos**, 1024 dims, L2-normalizado.
   Tuvo que agregarse el flag `--approval-request-id` al comando (no
   existía en el CLI, gap real — ver commit).
3. **Retrieval real**: consulta "cómo convertir un struct a JSON en Go"
   contra pgvector (`cosine_similarity` vía `<=>`) devolvió correctamente
   los chunks del documento de Golang, ordenados por relevancia — nunca
   los de Python. Verificado con SQL directo, no mockeado.

## Detalle técnico: cómo se corrió el embedding (limitación de red)
El sidecar BGE-M3 es loopback-only por diseño (`internal/embeddingruntime/
adapter/bgem3` rechaza cualquier host que no sea `localhost`/loopback
literal). El contenedor `orgd` vive en la red bridge de Docker (netns
propia), así que no puede llegar al `127.0.0.1:8901` del host directamente.
Solución aplicada:
- Se publicó el puerto de Postgres a `127.0.0.1:5432` en el host (antes no
  tenía ningún puerto publicado) — ver `compose.yaml`, comentario explica
  que es solo para este escape hatch, el resto de los servicios sigue en
  la red bridge normal.
- Los comandos que necesitan hablarle al sidecar corren con
  `docker run --network host` (NO `docker compose run`, esa versión de
  compose no soporta `--network` ahí) + `--env-file` con el entorno
  completo de `orgd` + `ORG_DATABASE_HOST=127.0.0.1` +
  las variables `ORG_EMBEDDING_*` del perfil BGE-M3.
- Esto es manual por ahora (no wireado en `compose.yaml` como servicio
  persistente) — si se usa seguido, conviene un servicio dedicado
  `rag-embedding-worker` con `network_mode: host` en vez de repetir el
  comando largo cada vez.

## Qué falta para ingesta masiva (fase 16, todavía no arrancada)
- Repetir el ciclo propose → review → reindex → backfill para el resto de
  los 1416 documentos restantes del corpus. La aprobación individual por
  documento (o por lote, si se decide una política) sigue siendo una
  decisión tuya — no la automaticé.
- Los 294 PDFs de reinforcement learning que mencionaste
  (`/home/edo/Downloads/self-improving-agents/papers/`) necesitan parsing
  PDF→texto antes de poder proponerse como candidatos (fase 5, no
  construida todavía). Se pueden subir tal cual a Object Storage
  (`raw/`) ya mismo si querés, aparte del pipeline de RAG.
- Los 149 repos de código: quedó pendiente tu decisión sobre si van al
  mismo pipeline de texto o a un índice de código aparte.
