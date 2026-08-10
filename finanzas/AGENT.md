# Departamento: finanzas

## Propósito
Unidad operativa. Líder canonical: `finanzas/administrador_financiero` (Administrador Financiero (Finanzas)).

## Qué produce
- `finanzas/administrador_financiero`: Coordinar al departamento (Analista de KPIs, Estratega de Expansión) y mantener la salud financiera de la operación.
- `finanzas/analista_costos`: Dueño del registro y seguimiento de la economía real de la organización: qué se gasta y en qué (APIs, infraestructura, herramientas, publicidad), qué entra, y qué se debe.
- `finanzas/analista_kpis`: Preparar indicadores financieros y operativos con datos trazables, supuestos explícitos y criterios conservadores, para que el Administrador Financiero los revise antes de cualquier publicación o uso en decisiones.
- `finanzas/estratega_expansion`: Evaluar, junto con el Administrador Financiero, oportunidades de venta o expansión y alternativas de deuda con criterio financiero, explícito y conservador.
- `finanzas/ingeniero_industrial`: Optimizar los procesos de la organización misma — la disciplina de ingeniería industrial aplicada a una empresa de agentes: costo por unidad de trabajo, cuellos de botella, colas, capacidad, ventanas tarifarias y retrabajo.

## Líder y workers
- Líder: `finanzas/administrador_financiero`
- Worker: `finanzas/analista_costos`
- Worker: `finanzas/analista_kpis`
- Worker: `finanzas/estratega_expansion`
- Worker: `finanzas/ingeniero_industrial`

## Delegación y escalamiento
La delegación dentro del departamento sigue `leader-worker-map.yaml`.
Cualquier decisión fuera del alcance de un rol se escala a su líder;
el líder escala a CEO o al owner humano según `reports_to` de cada rol.

## Fronteras con otros departamentos
Este AGENT no concede autoridad, capacidades ni ruteo de modelo:
esas decisiones viven exclusivamente en `docs/canonical/`
(`capability-matrix.yaml`, `model-routing.yaml`, `instruction-precedence.yaml`).

## Qué NO produce / qué decisiones no puede tomar
- `finanzas/administrador_financiero`: El Analista de KPIs **prepara** los indicadores; Investigación los **audita** — este rol no se autoevalúa, es una separación deliberada de responsabilidades.
- `finanzas/analista_costos`: Eduardo es el principal proveedor — sus ingresos vienen de los pacientes de Clínica Online, así que la salud de la organización se mide contra esa fuente, y el capital mensual para publicidad se decide con esos números, no con optimismo.
- `finanzas/analista_kpis`: El objetivo es mostrar la salud financiera real de la operación, no producir cifras optimistas.
- `finanzas/estratega_expansion`: Su función es aportar análisis para decidir; no convertir una oportunidad en compromiso comercial o financiero sin las aprobaciones correspondientes.
- `finanzas/ingeniero_industrial`: La organización no tenía hasta hoy a nadie cuyo trabajo fuera mirarse a sí misma con esos lentes.
- `finanzas/ingeniero_industrial`: Como en el resto de la organización, no implementa — define qué se mide y cuál es el objetivo; la instrumentación y el código son de Ingeniería de IA.
