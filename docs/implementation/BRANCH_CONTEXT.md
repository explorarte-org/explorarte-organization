# Contexto contractual para ramas de implementación

## Rama base

`docs/00-canonical-spec`

## Regla

Cada rama futura debe partir del commit que aprueba los documentos en `docs/canonical/`. Ningún módulo puede redefinir departamentos, modelos, capacidades, jerarquías, memoria o fronteras de células dentro de código hardcodeado.

## Archivos canónicos obligatorios

- `organization.yaml`
- `role-catalog.yaml`
- `leader-worker-map.yaml`
- `model-routing.yaml`
- `capability-matrix.yaml`
- `instruction-precedence.yaml`
- `memory-policy.yaml`
- `reasoning-assurance.yaml`
- `cell-boundaries.yaml`
- `architecture-characteristics.yaml`

## Contrato de entrega por rama

Cada rama crea un `INTEGRATION.md` con: commit base, módulos requeridos, interfaces provistas, migraciones, configuración, pruebas y siguiente rama compatible.
