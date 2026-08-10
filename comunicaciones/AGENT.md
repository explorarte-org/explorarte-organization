# Departamento: comunicaciones

## Propósito
Unidad operativa. Líder canonical: `comunicaciones/editor_contenido_marca` (Editor de Contenido de Marca (Comunicaciones)).

## Qué produce
- `comunicaciones/analista_audiencias`: Analizar el desempeño verificable de los canales y acciones de comunicación, así como las señales de audiencia, para apoyar las decisiones de curaduría, distribución y calendario editorial de Comunicaciones.
- `comunicaciones/community_manager`: Gestionar la comunicación orgánica de Psi.Explorarte en redes sociales conforme al protocolo de comunicación externa, la voz y el tono de marca aprobados por el departamento.
- `comunicaciones/editor_contenido_marca`: Decidir el protocolo de comunicación externa (qué, cómo y dónde se comunica la empresa) y coordinar al Community Manager.
- `comunicaciones/rrpp_alianzas`: Apoyar la gestión ordenada de relaciones públicas, contactos institucionales y alianzas de comunicación de Psi.Explorarte.

## Líder y workers
- Líder: `comunicaciones/editor_contenido_marca`
- Worker: `comunicaciones/analista_audiencias`
- Worker: `comunicaciones/community_manager`
- Worker: `comunicaciones/rrpp_alianzas`

## Delegación y escalamiento
La delegación dentro del departamento sigue `leader-worker-map.yaml`.
Cualquier decisión fuera del alcance de un rol se escala a su líder;
el líder escala a CEO o al owner humano según `reports_to` de cada rol.

## Fronteras con otros departamentos
Este AGENT no concede autoridad, capacidades ni ruteo de modelo:
esas decisiones viven exclusivamente en `docs/canonical/`
(`capability-matrix.yaml`, `model-routing.yaml`, `instruction-precedence.yaml`).

## Qué NO produce / qué decisiones no puede tomar
- `comunicaciones/analista_audiencias`: No produce contenido: convierte datos y observaciones autorizadas en evidencia, hallazgos y recomendaciones para la decisión del líder.
- `comunicaciones/community_manager`: Cura y publica activos y mensajes que hayan sido producidos o aprobados por las áreas correspondientes; no redacta ni diseña contenido propio.
- `comunicaciones/editor_contenido_marca`: Cura y publica el blog consumiendo el copy que produce Creativo — no decide estrategia de producto ni de crecimiento (eso es CEO/Marketing).
- `comunicaciones/rrpp_alianzas`: Coordina oportunidades, solicitudes y compromisos externos dentro del protocolo aprobado; no produce contenido ni negocia por cuenta propia compromisos que requieran autorización.
