# Rama 21 — Alibaba Token Plan via Claude Code CLI Adapter

Estado: implementación core en progreso; no registrada todavía en production bootstrap.

Base de rama: `661b31799a307c81ead84adfc3a226bcbf9060be` (main post R18/R19).

## Objetivo

Implementar el `ProviderAdapter` faltante para el provider canónico `alibaba_token_plan_via_claude_code` sin introducir un camino HTTP directo. La frontera es Claude Code en modo no interactivo; Claude Code es quien habla el protocolo Anthropic-compatible contra Alibaba Token Plan.

## Invariantes

1. `direct_http_forbidden` permanece verdadero. Este adapter nunca realiza HTTP.
2. El ejecutable se configura por path absoluto y se verifica por SHA-256 + salida exacta de `claude --version` antes de cada envío.
3. Las credenciales viven exclusivamente en un settings JSON dedicado, regular, acotado, con permisos privados y SHA-256 pinneado. El adapter no copia el token a argv, logs, errores ni variables del proceso padre.
4. El settings dedicado solo admite el bloque `env` requerido para Alibaba y un `ANTHROPIC_BASE_URL` igual al endpoint Token Plan aprobado. Coding Plan, PayG y endpoints arbitrarios fallan cerrado.
5. La ejecución usa argv directo (`exec.Command`), nunca shell interpolation.
6. Claude Code se ejecuta con `--bare -p`, tools deshabilitadas, MCP estricto/vacío, slash commands y Chrome deshabilitados, sin session persistence, con un system prompt fijo de provider.
7. El contexto renderizado se transmite solo por stdin. No aparece en argv, env, cwd, archivos temporales ni errores. Se rechaza por encima del límite de stdin de Claude Code.
8. `CLAUDE_CODE_MAX_OUTPUT_TOKENS` refleja `CanonicalRequest.MaxOutputTokens`. Retries internos (`CLAUDE_CODE_MAX_RETRIES`) y retries de structured-output (`MAX_STRUCTURED_OUTPUT_RETRIES`) son cero para conservar one-shot semantics.
9. stdout/stderr están acotados. stderr nunca se incorpora al error durable.
10. Una falla antes de que exista el proceso es `request_not_sent`. Desde `Start()` exitoso en adelante, timeout, cancelación, signal, exit != 0, overflow o contrato de salida inválido se clasifican conservadoramente `ambiguous_transport` y no son retryables automáticamente.
11. Cancelación termina el process group: SIGTERM, grace period acotado, luego SIGKILL. Enviar SIGTERM no prueba cancelación upstream y por eso no genera `cancelled_confirmed`.
12. Concurrencia local acotada con semaphore; esperar un slot respeta context/deadline.
13. `ProviderOutcome` distingue transporte y `process_exit_code`; HTTP legacy continúa funcionando con transport vacío interpretado como HTTP.

## Contrato Claude Code usado

El adapter depende de flags documentados de Claude Code: `--bare`, `-p`, `--output-format json`, `--json-schema`, `--tools ""`, `--strict-mcp-config`, `--disable-slash-commands`, `--no-session-persistence`, `--no-chrome`, `--permission-mode`, `--max-turns`, `--model`, `--settings`, `--system-prompt` y `--version`.

La respuesta text se extrae de `result`; structured output de `structured_output`. No se expone reasoning oculto.

## Gap de persistencia antes de wiring productivo

La migración R12 `000011_create_model_provider_adapter` modeló `model_provider_outcomes` con checks HTTP-céntricos (`response_received` exige HTTP 2xx). Guardar un éxito CLI como HTTP 200 sería evidencia falsa.

Por eso esta rama primero cambia el contrato Go a transport-aware y construye el adapter puro. **No registrar el adapter en `internal/modelruntime/bootstrap` hasta que una migración posterior agregue evidencia CLI (`transport`, `process_exit_code`) y relaje los checks por transporte.**

R20 está siendo desarrollado en paralelo desde el mismo main y tiene prioridad sobre el siguiente número de migración. R21 debe rebasearse luego de R20 y tomar el próximo número disponible; no reservar `000017` en paralelo.

## Egress / gobernanza

`docs/canonical/model-egress-policy.yaml` actualmente niega public/organizational/sanitized para Alibaba. R21 no modifica ese documento. La existencia del adapter no equivale a aprobación de egress.

Tampoco modifica bindings ni activa `executive.observer`; cualquier decisión canónica pendiente sigue fuera de R21.

## Fase de cierre posterior al merge de R20

- rebase sobre main;
- migración transport-aware del provider outcome ledger;
- Postgres integration tests para CLI evidence;
- wiring opt-in en runtime bootstrap (`Enabled=false` por default);
- fitness script y Makefile/CI específicos del adapter;
- prueba real en VPS con Claude Code pinneado y credencial Token Plan dedicada;
- `make verify` + race + `make verify-all`;
- `INTEGRATION.md` final.
