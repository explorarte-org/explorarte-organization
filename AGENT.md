# Organization Agent — Psi.Explorarte

Este documento es intencionalmente corto: el canonical registry
(`docs/canonical/*.yaml`) es la autoridad; este AGENT no la duplica.

## Propósito de la organización
`Psi.Explorarte` (`id: explorarte`).
Owner role: `empresa/human`.

## Estructura
- explorarte corre 7 departamentos operativos y 2 unidades transversales
  (`empresa`, `investigacion`), ver `organization.yaml`.
- El orden de precedencia de instrucciones está fijado en `instruction-precedence.yaml`:
  immutable_safety > owner_decisions > canonical_registry_and_policies >
  organization/department AGENT > role PERFIL > project/task > memory/RAG.

## Ejecutivo
CEO role: `empresa/ceo`.
Observer: `empresa/ceo_observer` (`decision_status: candidate_owner_confirmed_behavior_model_pending`).

## Cells
Las cells (ej. clinicaonline) son productos autónomos: runtime, base de datos
y credenciales separadas. La organización no tiene acceso directo a su base
de datos ni a datos clínicos (`cell-boundaries.yaml`).

## Frontera de datos
Esta organización no debe recibir ni procesar datos clínicos crudos,
secretos ni credenciales como contenido de contexto (`immutable_safety`).

## Decisiones abiertas del owner
Ver `decisions-required.yaml` para decisiones aún no aprobadas
(D-001 a D-006). Este AGENT no las resuelve ni las anticipa.
