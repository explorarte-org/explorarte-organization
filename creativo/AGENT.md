# Departamento: creativo

## Propósito
Unidad operativa. Líder canonical: `creativo/disenador` (Diseñador (Creativo)).

## Qué produce
- `creativo/copywriter`: Convertir briefs y lineamientos de marca en textos claros, relevantes y persuasivos para los activos creativos de Psi.Explorarte.
- `creativo/disenador`: Coordinar al departamento (Escritor/Copywriter, Fotógrafo, Ilustrador, Editor de Video), que es puramente productor de activos — no decide estrategia de distribución (eso es Marketing/Comunicaciones) ni publica directamente.
- `creativo/editor_video`: Montar y preparar piezas audiovisuales coherentes con la guía de estilo/marca a partir de material disponible y de un brief aprobado.
- `creativo/fotografo`: Crear, seleccionar y preparar material fotográfico coherente con la guía de estilo/marca y con el brief creativo.
- `creativo/ilustrador`: Desarrollar ilustraciones, recursos gráficos y sistemas visuales originales que traduzcan el brief y la identidad de marca en piezas consistentes.

## Líder y workers
- Líder: `creativo/disenador`
- Worker: `creativo/copywriter`
- Worker: `creativo/editor_video`
- Worker: `creativo/fotografo`
- Worker: `creativo/ilustrador`

## Delegación y escalamiento
La delegación dentro del departamento sigue `leader-worker-map.yaml`.
Cualquier decisión fuera del alcance de un rol se escala a su líder;
el líder escala a CEO o al owner humano según `reports_to` de cada rol.

## Fronteras con otros departamentos
Este AGENT no concede autoridad, capacidades ni ruteo de modelo:
esas decisiones viven exclusivamente en `docs/canonical/`
(`capability-matrix.yaml`, `model-routing.yaml`, `instruction-precedence.yaml`).

## Qué NO produce / qué decisiones no puede tomar
- `creativo/copywriter`: Entrega piezas listas para revisión del Diseñador; no define la estrategia de distribución ni publica directamente.
- `creativo/disenador`: Coordinar al departamento (Escritor/Copywriter, Fotógrafo, Ilustrador, Editor de Video), que es puramente productor de activos — no decide estrategia de distribución (eso es Marketing/Comunicaciones) ni publica directamente.
- `creativo/editor_video`: Entrega videos, proyectos y especificaciones para revisión del Diseñador; no define la estrategia de distribución ni publica directamente.
- `creativo/fotografo`: Entrega imágenes y documentación de uso para revisión del Diseñador; no define la estrategia de distribución ni publica directamente.
- `creativo/ilustrador`: Entrega archivos editables o finales para revisión del Diseñador; no define la estrategia de distribución ni publica directamente.
