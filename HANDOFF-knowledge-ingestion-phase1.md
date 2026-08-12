# HANDOFF — feat/knowledge-ingestion-object-storage — Fase 1 CERRADA

## Estado: conector completo, probado en vivo, corpus real ya cargado.

### Resolución del bloqueo (para que quede el registro real)
No era IAM. El bucket que existe se llama `bucket-20260810-0027` (nombre
automático de OCI) — el nombre custom `explorarte-org-knowledge-source` que
se había planeado nunca se aplicó al crear el bucket, y OCI no permite
renombrar un bucket después de creado. `compose.yaml` e
`internal/objectstorage/config.go` ya apuntan al nombre real.

De paso, en el camino se agregaron dos policies IAM que en retrospectiva no
eran el problema, pero no está de más tenerlas:
- `Allow service objectstorage to manage object-family in tenancy`
- `Allow group Administrators to manage objects in tenancy where target.bucket.name='bucket-20260810-0027'`
  (ojo: esta última quedó escrita con el nombre viejo del bucket en el
  `where` — si en algún momento el acceso vuelve a fallar, revisar que la
  condición de esta policy tenga el nombre correcto del bucket, no
  `explorarte-org-knowledge-source`.)

### Verificado en vivo, extremo a extremo
- `objectstorage whoami` → namespace correcto.
- `objectstorage list --prefix raw/` → bucket vacío, sin error.
- `objectstorage put` + `get` de un objeto de prueba → byte-idéntico.
- `objectstorage seed` → **1418 documentos** de `rag_curado/v4` subidos a
  `raw/rag-curado-v4/<corpus>/<id>.{txt,md}`, confirmado por `list` (count
  1418).
- Manifest original de curación (`manifest.jsonl`, con SHA-256/word_count/
  provenance por documento) subido a
  `manifests/rag-curado-v4-source-manifest.jsonl` — su SHA-256 en el bucket
  (`365fd7fd91bac...`) coincide exacto con el `manifest_sha256` que el
  propio proceso de curación (`rag-ai-curator-v3`) había registrado en
  `summary.json`, confirmando que no hubo corrupción en la subida.
- `summary.json` (censo/estadísticas del corpus) subido a
  `manifests/rag-curado-v4-summary.json`.

### Lo que esto ya cubre del roadmap original de 16 fases
Para este corpus específico (no como capacidad general reutilizable
todavía):
1. Object Storage connector — hecho.
2. Corpus census — ya viene en `summary.json` (by_corpus, by_reason, etc.),
   generado por la curación previa, no por este pipeline.
3. Manifest/provenance — `manifest.jsonl`, ya subido.
4. SHA-256 + deduplicación — ya aplicado por la curación previa
   (`source_sha256`/`clean_sha256` por documento).
5-6. Parsing/normalización — el corpus ya viene aplanado a texto limpio
   (`.txt`), no quedan PDF/EPUB que parsear para este lote.

Lo que **no** está hecho todavía y sí requiere trabajo nuevo: 7
(clasificación), 8 (chunking estructural), 9-10 (RAG candidate/approval —
el paquete `internal/rag` ya tiene el lifecycle `candidate→approved`
implementado, falta conectarlo a este corpus), 11 (Gemini Embedding 2
Batch — el adapter `internal/embeddingruntime/adapter/gemini` ya existe,
falta invocarlo sobre estos documentos), 12 (pgvector), 13 (hybrid
retrieval), 14 (Context Engine), 15 (canary 3-5 docs), 16 (ingesta masiva).

### Sidecar BGE-M3
Ver `HANDOFF-bgem3-sidecar.md` — corriendo, probado en vivo, listo para
cuando se conecte al flujo de embeddings.

### Próximo paso sugerido
Fase 7-9: definir cómo se clasifica cada documento (posiblemente reusando
`by_corpus` de `summary.json` como clasificación inicial: books_ingenieria_ia,
curso_golang_espanol, etc.) y diseñar el chunking estructural antes de
tocar `internal/rag`. Esto es una decisión de diseño real, no mecánica —
mejor con el owner en la conversación, no de madrugada y sin supervisión.
