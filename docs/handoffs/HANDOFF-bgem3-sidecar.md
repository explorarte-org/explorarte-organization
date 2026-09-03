# HANDOFF — BGE-M3 sidecar (embeddings locales por CPU)

## Estado: corriendo, probado en vivo end-to-end (Go client real → sidecar real).

### Qué se hizo
- Modelo `BAAI/bge-m3` descargado de Hugging Face (commit
  `5617a9f61b028005a4858fdac845db406aefb181`, 4.3GB) a
  `/opt/explorarte/bgem3-sidecar/model` en el VPS de la organización.
- `artifact_sha256` pinneado: sha256 de `pytorch_model.bin` =
  `b5e0ce3470abf5ef3831aa1bd5553b486803e83251590ab7ff35a117cf6aad38`.
- Sidecar Python (`/opt/explorarte/bgem3-sidecar/server.py`, stdlib
  `http.server` + `FlagEmbedding`, sin FastAPI/uvicorn) implementando
  exactamente el wire protocol que `internal/embeddingruntime/adapter/bgem3`
  ya esperaba (`GET /v1/health`, `POST /v1/embed`, mismos nombres de campo
  JSON que `wire.go`) — no hubo que tocar el código Go existente, el
  contrato ya estaba bien especificado ahí.
- Corriendo como `systemd` service `bgem3-sidecar.service`: usuario sin
  privilegios dedicado (`bgem3`, `nologin`), `ReadOnlyPaths` sobre el
  modelo, `IPAddressDeny=any` + `IPAddressAllow=127.0.0.0/8 ::1` (sin red
  después del aprovisionamiento — el modelo se bajó ANTES de aplicar esta
  restricción), `NoNewPrivileges`, `ProtectSystem=strict`, `PrivateTmp`,
  límites de memoria/CPU. `enabled` (sobrevive reboot).
- Test de integración opt-in agregado:
  `internal/embeddingruntime/adapter/bgem3/live_sidecar_smoke_test.go`
  (skip por defecto, se activa con `ORG_BGE_M3_LIVE_SMOKE_TEST=1`) — corrido
  contra el sidecar real: health check, embed de 2 textos, valida dimensión
  1024 y normalización L2 real (no solo que no tire error). Pasó limpio.
- Verificado manualmente además con `curl` directo: vector de 1024
  dimensiones, norma L2 = 1.0 exacto.

### Cómo probarlo de nuevo
```
curl -s http://127.0.0.1:8901/v1/health
sudo systemctl status bgem3-sidecar.service
```
Test Go opt-in (requiere `--network host` si se corre desde un contenedor):
```
sudo docker run --rm --network host -e ORG_BGE_M3_LIVE_SMOKE_TEST=1 \
  -v $(pwd):/src -w /src golang:1.25 sh -c \
  'git config --global --add safe.directory /src && \
   go test ./internal/embeddingruntime/adapter/bgem3/... -run TestLiveSidecar -v'
```

### No se tocó / pendiente
- El adapter Go (`ORG_EMBEDDING_PROVIDER_BGE_M3_*`) sigue **deshabilitado**
  en `compose.yaml` — no lo prendí en `model-worker` todavía. El sidecar
  está listo y probado, pero conectarlo al flujo productivo (rag/memory)
  es una decisión de wiring separada, no algo que deba pasar sin que lo
  revises primero.
- No se automatizó el download/hash-pin en un script repetible (fue manual,
  paso a paso, documentado acá). Si el modelo necesita reprovisionarse en
  otra VPS, seguir los mismos pasos: `snapshot_download`, `sha256sum` del
  `pytorch_model.bin`, actualizar `MODEL_REVISION`/`MODEL_ARTIFACT_SHA256`
  en `server.py`.
- El servidor Python vive fuera del repo Git de la organización (en
  `/opt/explorarte/bgem3-sidecar/`, no en `/opt/explorarte/organization/`)
  — está documentado acá pero no versionado. Si querés que quede en el
  repo, decime y lo muevo/commiteo como parte del proyecto.
