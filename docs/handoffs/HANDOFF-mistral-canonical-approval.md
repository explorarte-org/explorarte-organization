# HANDOFF — Aprobación canónica de Mistral — DECISIÓN DEL OWNER REGISTRADA

## Qué se aprobó

El owner de Explorarte Organization aprobó explícitamente, el 2026-09-10,
la incorporación canónica del provider Mistral introducida por
`canonical/mistral-provider@76cc3c1589569fee7428710f6623f45c10d64832`
(PR #199) y consolidada por los commits `00a7f8a`, `24eee8b`, `78185d2`,
`18b68cf`, `ce89b64` de esa misma branch, que hasta esta fecha declaraban
explícitamente en sus propios mensajes "NO canonical approval trailer
added -- Mistral canonical approval remains an owner decision".

Alcance exacto de la aprobación:

- `provider = mistral`
- `model = ministral-8b-2512` (pin versionado, nunca el alias
  `ministral-8b-latest`)
- `transport = http_adapter`
- Egress: `public = ALLOW`, `sanitized = ALLOW`, `organizational = DENY`,
  `clinical` y `secret` permanecen HARD DENY sin cambios.

La aprobación cubre el código y la política canónica del provider
(`docs/canonical/model-routing.yaml`, `docs/canonical/model-egress-policy.yaml`,
el adapter Mistral y su pricing en migration 000069).

## Qué NO autoriza esta decisión

Explícitamente, por instrucción directa del owner:

- NO habilitar Mistral en producción (`ORG_MODEL_PROVIDER_MISTRAL_ENABLED`
  permanece `false`).
- NO configurar un credit ceiling productivo
  (`MISTRAL_CREDIT_CEILING_USD` permanece vacío).
- NO enviar tráfico real al provider.
- NO mover `research.worker` a Mistral ni a ningún pool.
- NO crear ni activar ningún pool productivo.

La activación productiva de Mistral es una decisión y un round
completamente separados, todavía no solicitados ni autorizados.

## Por qué este commit existe

Los commits originales de `canonical/mistral-provider` son historia
cerrada — ninguno fue (ni será) modificado, amendado ni rebaseado. Este
commit es un registro nuevo, honesto y auditable de una decisión que
realmente ocurrió, en la branch de integración correspondiente
(`integration/mistral-000069-rollback-fix`), no una reescritura de la
branch original. El trailer que acompaña este commit refleja exactamente
eso: la aprobación fue dada por el owner, no autogenerada.
