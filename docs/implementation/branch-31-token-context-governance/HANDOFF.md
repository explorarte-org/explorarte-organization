# R31 — Handoff inicial

## Estado

- Rama: `branch-31-token-context-governance`.
- Base: `1e2b3966bbcba99724c31f3f95887858be721089` (`docs: documentar el cierre correctivo R30.1 en HANDOFF.md`).
- Worktree: `/opt/explorarte/worktrees/branch-31-token-context-governance`.
- `main` no fue cambiado ni cambiado de rama.
- Los archivos ajenos `compose.integration.bugreview07.yaml` y `organization canonical` no existen en este worktree y no fueron tocados.

## Qué contiene el primer slice

- `DESIGN.md`: contrato de arquitectura Go para medición de tokens/caché, renderer dual, divulgación progresiva, checkpoints, mensajería esparsa, autoauditoría y canario de ingesta.
- `EVIDENCE.md`: contraste de los dos informes 2026 contra fuentes primarias y contra el código real; distingue especificaciones, papers experimentales y evidencia comercial.
- Este handoff: estado reproducible, artifacts y orden de ejecución.

No contiene cambios de Go, SQL, configuración viva, routing, secretos ni adapters.

## Estado real heredado de R30.1

- Los seis hallazgos correctivos quedaron cerrados y documentados.
- Cada commit de R30.1 pasó `gofmt`, `go vet`, `go build`, unit tests, integración completa contra PostgreSQL real y `make verify`.
- BGE-M3 real sigue sin instalarse ni medirse; el adapter fue verificado con servidor fake.
- El catálogo contiene 14 fixtures, de los cuales 4 tienen runner real y 10 permanecen `pending`.

R31 conserva esos pendientes de forma explícita. No considera 4/14 equivalente a aprobación total.

## Corpus AI transferido, no ingerido

Ubicación:

```text
/opt/explorarte/artifacts/rag-ingestion/ai-corpus-v4/
```

| Archivo | SHA-256 | Tamaño observado |
|---|---|---:|
| `documents.jsonl` | `d7d620b534cd8a22836a2a5037d4e5b66c39b92a061e367448b73a41fba7912c` | 17,208,331 bytes |
| `manifest.jsonl` | `365fd7fd91bac170b5adf887341ab6255bbad6c3553a51922c04e56d86114e56` | 3,007,785 bytes |
| `summary.json` | `2253ef065e84655075e988b247b80e3cf7a6059c6d7de25520d18efdd6a22dd1` | 2,550 bytes |
| `CURATION_REPORT.md` | `343dedaaed7c2505ca0a6d3d97ff7fafbe202843ded5e84cf252e41f259b8e18` | 3,093 bytes |

- `documents.jsonl`: 1,418 registros/líneas.
- `manifest.jsonl`: 3,390 registros/líneas.
- El VPS tenía 4.0 GiB libres en `/` al verificar la transferencia (`29G`, 86% usado).
- No se ejecutó admisión, reindex, backfill ni llamada de embeddings.

## Decisiones resultantes del contraste

Se adoptan como patrones:

1. snapshot canónico íntegro separado de vista compacta de ejecución;
2. contabilidad de fresh/cache-read/cache-write/output/reasoning reportada por proveedor;
3. prefijo estable y cacheable, medido por endpoint real;
4. detalle externo recuperable por referencia autorizada;
5. checkpoints verificados sobre el DAG y logs append-only;
6. comunicación punto a punto por delta/artifact y revisión selectiva;
7. autoauditoría capaz de proponer, incapaz de autopromover.

Se rechazan o difieren:

- TOON como requisito (el informe incluso expande incorrectamente su nombre);
- MCP Tool Search sin existir catálogo MCP en el runtime actual;
- MixKV/VisPruner/VTW para LLMs consumidos por API;
- CA-MCP/WIMSE hasta existir una red cross-organización;
- porcentajes comerciales como gates de aceptación;
- LLMLingua sobre contexto autoritativo sin canario de fidelidad.

## Bloqueadores y orden siguiente

1. Fase 0: baseline reproducible y cobertura honesta `executed/runner_ready/catalog_total`.
2. Fases 1–4: telemetría real, renderer dual, caché y divulgación progresiva.
3. Implementar primero los runners RAG/Memory/namespace pendientes necesarios para evaluar ingesta.
4. Instalar y medir BGE-M3 real con identidad fijada; el disco actual obliga a verificar tamaño/margen antes de descargar pesos.
5. Ingerir un canario estratificado del corpus AI y ejecutar recuperación en español.
6. Completar 14/14 runners.
7. Solo entonces habilitar la autoauditoría organizacional sobre las implementaciones posteriores.

La ingesta completa y la autoejecución no son parte de este primer commit documental.

## Verificaciones de este commit

Ejecutadas en el worktree R31 antes de commitear:

| Comando | Resultado |
|---|---|
| `git diff --check` | PASS, sin salida |
| `gofmt -l .` | PASS, sin archivos |
| `go vet ./...` | PASS |
| `go build ./...` | PASS |
| `go test ./... -count=1` | PASS, exit 0 |
| `go test -tags=integration ./... -count=1 -p 1` | PASS, exit 0 contra `r23-integration-pg` en `127.0.0.1:35432` |
| `make verify` | PASS, incluido `check-webevidence-fitness.sh` |

La primera invocación de integración de esta sesión se ejecutó sin exportar `ORG_DATABASE_URL`/`ORG_TEST_DATABASE_URL`: tres tests de `cmd/orgctl` intentaron el default `127.0.0.1:5432` y fallaron con `connection refused`; el resto de paquetes continuó contra sus DSN de test. No se interpretó como éxito ni se ocultó. Se verificó que `r23-integration-pg` estaba `Up` y publicado exclusivamente en `127.0.0.1:35432`, y se repitió toda `./...` con ambos DSN y `ORG_CANONICAL_DIR` explícitos; esa corrida completa terminó en verde. Ningún comando apuntó a Postgres de producción.
