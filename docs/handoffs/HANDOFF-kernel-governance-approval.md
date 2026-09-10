# HANDOFF — Aprobación de Kernel Governance para Dynamic Canonical Model Routing — DECISIÓN DEL OWNER REGISTRADA

## Qué se aprobó

El owner de Explorarte Organization aprobó explícitamente, el 2026-09-10,
los cambios de Kernel Governance que introdujeron la autoridad de pool
routing para Dynamic Canonical Model Routing. Estos cambios tocan
superficies que `scripts/check-kernel-governance-fitness.sh` protege
(`internal/modeldispatch`, `internal/modelegress`, `internal/organization`)
y por eso exigen esta aprobación explícita antes de integrarse a `main`.

Commits cubiertos por esta aprobación (identificados por búsqueda real
sobre `KERNEL_PATHS`, no asumidos):

- `dae3f8a` — `fix(registry): understand canonical pool routing documents`.
  Extiende el mirror YAML estricto de `internal/organization/registry`
  para entender `routing_mode: pool` (campos, selector, allow_paid,
  capabilities, candidates), normalizando capabilities/candidates como
  colecciones sin orden/duplicado para que los hashes de política
  estática permanezcan byte-idénticos. No duplica selección de rutas.
- `a2c5aa3` — `fix(modeldispatch): recognize pool-routed roles in
  authorized attempts`. Un rol pool-routed no tenía fila en
  `role_model_bindings` (correcto: su candidato lo resuelve
  `RouteResolver` per-invocation), lo que rechazaba todo intento de
  worker pool-routed antes de llegar al Harness. Agrega
  `RoutingPolicyReader.GetRoutingPolicy`, que responde solo "¿es esta
  política un pool materializado?" — nunca candidato, proveedor ni
  modelo.
- `361c8e4` — `fix(modeldispatch): make pool attempt authority explicit
  and fail closed`. Reemplaza el binding sintético temporal por
  `RoleRoutingAuthorityRef` explícito (`Kind: static_binding |
  pool_policy`). `Store.GetRoleRoutingAuthority` falla cerrado en todos
  los bordes: rol ausente, revisión obsoleta, retirado, deshabilitado,
  no ejecutable, sin `model_policy`, binding+pool simultáneos, ninguno
  presente, pool sin candidatos materializados.

## Qué NO cambió (verificado, no supuesto)

`internal/modelruntime`/`RouteResolver` sigue siendo la única autoridad
para selección de candidato, capacidad y ejecución — ninguno de estos
tres commits duplica esa lógica, confirmado en sus propios mensajes y
en la auditoría de `STACK_INTEGRATION_MERGE_PREPARATION_V1`.
`docs/canonical/capability-matrix.yaml` no fue tocado por ninguno de
estos commits. Ningún pool productivo existe hoy en
`docs/canonical/model-routing.yaml` — `research.worker` sigue en
enrutamiento estático plano.

## Qué NO autoriza esta decisión

Explícitamente, por instrucción directa del owner:

- NO activar `research.worker` como pool.
- NO habilitar ningún pool productivo.
- NO modificar `capability-matrix.yaml`.
- NO habilitar nuevos providers.
- NO deploy.
- NO canary.
- NO tráfico productivo.

La activación productiva es un round separado, todavía no solicitado.

## Por qué este commit existe

Los tres commits originales (`dae3f8a`, `a2c5aa3`, `361c8e4`) son
historia cerrada en `kernel/dynamic-canonical-model-routing` y
`kernel/model-retry-failover-v1` — ninguno fue ni será modificado,
amendado ni rebaseado. Este commit es un registro nuevo, honesto y
auditable de una decisión que realmente ocurrió, en la branch de
integración correspondiente (`integration/dynamic-routing-final-review`,
la única de las branches de integración que contiene los tres commits
en su historia). El trailer que acompaña este commit refleja
exactamente eso: la aprobación fue dada por el owner, no autogenerada.
