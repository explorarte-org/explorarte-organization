# HANDOFF — feat/knowledge-ingestion-object-storage — Fase 1 (OCI Object Storage connector)

## Estado: conector completo y probado. Bloqueado por IAM, no por código.

### Qué quedó hecho y verificado
- `internal/objectstorage`: cliente OCI Object Storage hand-rolled (sin SDK,
  mismo patrón que el resto del proyecto). Implementa OCI Request Signing
  Version 1 (RSA-SHA256/PKCS1v15) desde cero.
  - `config.go`: carga/valida config desde env (`ORG_OBJECT_STORAGE_OCI_*`).
  - `signer.go`: construye el signing string y firma. 5 tests, incluyen
    verificación criptográfica independiente de la firma contra la llave
    pública (no solo "no tira error").
  - `client.go`: `ListObjects` (con paginación real vía `nextStartWith`),
    `GetObject`, `PutObject`, `HeadObject`, cap de tamaño de respuesta,
    `ErrNotFound`/`ErrRequest` tipados. 6 tests con `httptest`.
  - `DebugRequestNamespace`: diagnóstico de solo namespace (sin bucket) —
    quedó como subcomando `orgctl objectstorage whoami`, útil como health
    check permanente, no solo para este debug puntual.
- `cmd/orgctl/objectstorage.go`: subcomandos `list`, `get`, `put`, `seed`
  (sube un directorio local completo a `raw/<prefix>/` y escribe un
  manifest JSON en `manifests/`), `whoami`.
- `compose.yaml`: credenciales y config montadas en `orgd` y `model-worker`
  (reutilizables por ambos), llave privada en
  `/etc/explorarte/secrets/oci-object-storage-api-key.pem` (permisos 600,
  igual que el resto de los secrets del proyecto). `rag_curado` montado
  read-only en `orgd` en `/opt/explorarte/knowledge-source/rag_curado`.
- `go build ./...` y `go test ./internal/objectstorage/... ./cmd/orgctl/...`
  verdes.

### La firma funciona — verificado en vivo contra la API real
`orgctl objectstorage whoami` (GET /n/{namespace}, no toca el bucket) devolvió
respuesta correcta de OCI:
```
{"defaultS3CompartmentId":"ocid1.tenancy.oc1..aaaaaaaa4fugjamty...",
 "defaultSwiftCompartmentId":"ocid1.tenancy.oc1..aaaaaaaa4fugjamty...",
 "namespace":"axkhdnwe6r1c"}
```
Esto aísla el problema: la autenticación (firma RSA, keyId, fingerprint)
está perfecta. El namespace es correcto.

### El bloqueo real: IAM
`orgctl objectstorage list --prefix raw/` devuelve:
```
{"code":"BucketNotFound","message":"Either the bucket named
'explorarte-org-knowledge-source' does not exist in the namespace
'axkhdnwe6r1c' or you are not authorized to access it"}
```
Dado que el namespace responde bien con las credenciales correctas, esto es
casi seguro una política IAM faltante — el usuario de la API key no tiene
permiso sobre el bucket (o su compartment), no un problema de nombre de
bucket ni de firma.

### Qué hacer para destrabarlo (consola de OCI → Identity & Security → Policies)
Crear una policy en el compartment donde vive el tenancy/bucket con algo como:
```
Allow user ocid1.user.oc1..aaaaaaaagdrjnpcecaqx7dzybkdvn5fvdelcm7a7fhrskywitssx5q74ezta to manage objects in tenancy where target.bucket.name='explorarte-org-knowledge-source'
```
(Ajustar `in tenancy` a `in compartment <nombre>` si el bucket vive en un
compartment específico, no en la raíz — revisar en Storage → Buckets →
`explorarte-org-knowledge-source` → qué compartment aparece arriba.)

Una vez agregada la policy (los cambios IAM en OCI tardan unos segundos a
minutos en propagar), correr de nuevo:
```
sudo docker compose run --rm --entrypoint orgctl orgd objectstorage list --prefix raw/
```
Debería devolver `{"count":0,"objects":null}` (bucket vacío, sin error).

### Siguiente paso una vez destrabado (no hecho todavía)
Subir el corpus real ya curado (`rag_curado/v4`, 1418 documentos, 2.31M
palabras, ya tiene manifest.jsonl con SHA-256 y provenance por documento —
esto ya cubre gran parte de las fases 2-4 del roadmap para este corpus
específico):
```
sudo docker compose run --rm --entrypoint orgctl orgd objectstorage seed \
  --local-dir /opt/explorarte/knowledge-source/rag_curado/v4/documents \
  --object-prefix rag-curado-v4 \
  --manifest rag-curado-v4-seed-manifest.json
```
(Probar primero con `--dry-run` para ver qué subiría sin tocar el bucket.)

### No se tocó
- Fases 5-16 del roadmap (parsing multi-formato, normalización,
  clasificación, chunking, RAG candidate/approval, Gemini Embedding 2 Batch,
  pgvector, hybrid retrieval, Context Engine, canary, ingesta masiva) —
  ninguna tiene sentido empezar hasta que el bucket sea accesible y se
  pueda verificar contra datos reales, no mocks. El corpus ya "pre-resuelve"
  buena parte de fases 2-6 (census/manifest/dedup/parsing/normalización) por
  venir de `rag-ai-curator-v3`, así que probablemente fases 7+ (clasificación,
  chunking, candidatos RAG) sean el punto real de arranque una vez destrabado
  el bucket.
- Descarga del modelo BGE-M3 de Hugging Face para CPU (pendiente, mencionado
  por el owner, no bloqueante para esto).
