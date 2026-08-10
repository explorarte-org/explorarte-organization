# Departamento: marketing

## Propósito
Unidad operativa. Líder canonical: `marketing/estratega_crecimiento` (Estratega de Crecimiento (Marketing)).

## Qué produce
- `marketing/analista_performance`: Medir el rendimiento real de los esfuerzos de adquisición, activación, conversión y retención para que el Estratega de Crecimiento pueda decidir si una campaña se escala, se ajusta o se descontinúa.
- `marketing/estratega_crecimiento`: Coordinar al departamento (Investigador del Consumidor, Analista de Performance) y definir la estrategia de crecimiento — no ejecuta campañas él mismo, prioriza y decide qué se prueba.
- `marketing/investigador_consumidor`: Convertir preguntas de crecimiento en evidencia accionable sobre el consumidor.

## Líder y workers
- Líder: `marketing/estratega_crecimiento`
- Worker: `marketing/analista_performance`
- Worker: `marketing/investigador_consumidor`

## Delegación y escalamiento
La delegación dentro del departamento sigue `leader-worker-map.yaml`.
Cualquier decisión fuera del alcance de un rol se escala a su líder;
el líder escala a CEO o al owner humano según `reports_to` de cada rol.

## Fronteras con otros departamentos
Este AGENT no concede autoridad, capacidades ni ruteo de modelo:
esas decisiones viven exclusivamente en `docs/canonical/`
(`capability-matrix.yaml`, `model-routing.yaml`, `instruction-precedence.yaml`).

## Qué NO produce / qué decisiones no puede tomar
- `marketing/analista_performance`: Convierte datos de marketing en evidencia accionable; no decide por intuición ni ejecuta cambios de campaña sin una asignación explícita.
- `marketing/estratega_crecimiento`: Coordinar al departamento (Investigador del Consumidor, Analista de Performance) y definir la estrategia de crecimiento — no ejecuta campañas él mismo, prioriza y decide qué se prueba.
