# Rama 01 — Bootstrap del kernel

## Identidad

- Commit base requerido: `6e12c69526adfab007ed43399a7e762c11d3641c`
- Rama: `feat/01-bootstrap-kernel`
- Módulo Go: `github.com/Mireuz13/explorarte-organization`
- Arquitectura: monolito modular, un daemon (`orgd`) y una CLI operativa (`orgctl`).

## Proporciona

- Carga y validación de configuración por variables de entorno.
- Logging estructurado con `log/slog`.
- Servidor HTTP con `GET /healthz`, `GET /readyz` y `GET /version`.
- Apagado graceful mediante `SIGINT` y `SIGTERM`.
- Binarios `orgd` y `orgctl`.
- Imagen Docker ARM64/AMD64 reproducible y no privilegiada.
- Compose local sin PostgreSQL todavía.
- Quality gates mediante Makefile y GitHub Actions.

## No proporciona

- PostgreSQL, migraciones ni repositorios.
- Motor durable de tareas.
- Registro organizacional.
- Adaptadores de modelos.
- CEO, observador, líderes o workers.
- RAG, memoria, Prolog ni integración con células.

Esos elementos pertenecen a ramas posteriores. Esta rama no debe inventar contratos de dominio.

## Interfaces públicas

### Daemon

```text
orgd
```

### CLI

```text
orgctl version
orgctl health --url http://127.0.0.1:8080
orgctl health --ready --url http://127.0.0.1:8080
```

### HTTP

```text
GET /healthz
GET /readyz
GET /version
```

## Configuración

| Variable | Default |
|---|---|
| `ORG_APP_NAME` | `explorarte-organization` |
| `ORG_ENVIRONMENT` | `development` |
| `ORG_HTTP_ADDR` | `:8080` |
| `ORG_HTTP_READ_HEADER_TIMEOUT` | `5s` |
| `ORG_HTTP_READ_TIMEOUT` | `15s` |
| `ORG_HTTP_WRITE_TIMEOUT` | `30s` |
| `ORG_HTTP_IDLE_TIMEOUT` | `60s` |
| `ORG_SHUTDOWN_TIMEOUT` | `15s` |
| `ORG_LOG_LEVEL` | `info` |
| `ORG_LOG_FORMAT` | `json` |

## Verificación

```bash
make verify

go run ./cmd/orgd
# En otra terminal:
go run ./cmd/orgctl health --ready

docker compose up --build -d
docker compose ps
docker compose exec orgd /usr/local/bin/orgctl health --ready
docker compose down
```

## Dependencias de la rama siguiente

La Rama 02 puede añadir PostgreSQL sin cambiar los entrypoints:

1. Crear `internal/platform/postgres`.
2. Crear `internal/platform/migrations`.
3. Extender `config.Config` con `Database`.
4. Inyectar el pool desde `internal/app.New`.
5. Hacer que `/readyz` dependa de una comprobación local del pool.
6. Agregar `postgres` al `compose.yaml` sin publicar el puerto al host.

## Commit sugerido

```text
feat(kernel): bootstrap modular organization daemon
```
