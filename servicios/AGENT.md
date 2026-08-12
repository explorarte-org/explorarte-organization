# Departamento: servicios

## Propósito
Unidad operativa. Líder canonical: `servicios/product_manager_portafolio` (Product Manager del Portafolio (Servicios)).

## Qué produce
- `servicios/analista_calidad`: Comprobar, con evidencia, si los cambios del servicio producen la calidad y los resultados esperados para usuarios y operación.
- `servicios/disenador_uxui`: Diseñar experiencias digitales claras, accesibles y coherentes para los productos del portafolio, convirtiendo necesidades de usuarios y objetivos de servicio en flujos, interfaces y especificaciones verificables.
- `servicios/operaciones_servicio`: Asegurar que la operación diaria de los servicios sea consistente, observable y capaz de recuperarse ante fallos.
- `servicios/product_manager_portafolio`: Decidir prioridades de ejecución entre los productos del portafolio (hoy: Clínica Online; a futuro: el servicio de simulaciones) y coordinar al resto del departamento: Diseñador UX/UI, Service Designer, Soporte al Usuario, Operaciones de Servicio, Responsable de Datos del Servicio, Analista de Calidad y Resultados.
- `servicios/responsable_datos`: Mantener definiciones, calidad, trazabilidad y uso responsable de los datos necesarios para operar y mejorar los servicios.
- `servicios/service_designer`: Diseñar y mejorar el servicio de punta a punta, conectando la experiencia de usuario con personas, procesos, tecnología, políticas, canales y operaciones.
- `servicios/soporte_usuario`: Acompañar a los usuarios en el uso del servicio, resolver solicitudes dentro del alcance autorizado y convertir señales de soporte en evidencia accionable para Producto, UX/UI, Operaciones y Calidad.

## Líder y workers
- Líder: `servicios/product_manager_portafolio`
- Worker: `servicios/analista_calidad`
- Worker: `servicios/disenador_uxui`
- Worker: `servicios/operaciones_servicio`
- Worker: `servicios/responsable_datos`
- Worker: `servicios/service_designer`
- Worker: `servicios/soporte_usuario`

## Delegación y escalamiento
La delegación dentro del departamento sigue `leader-worker-map.yaml`.
Cualquier decisión fuera del alcance de un rol se escala a su líder;
el líder escala a CEO o al owner humano según `reports_to` de cada rol.

## Fronteras con otros departamentos
Este AGENT no concede autoridad, capacidades ni ruteo de modelo:
esas decisiones viven exclusivamente en `docs/canonical/`
(`capability-matrix.yaml`, `model-routing.yaml`, `instruction-precedence.yaml`).

## Qué NO produce / qué decisiones no puede tomar
- `servicios/analista_calidad`: Aporta criterios de aceptación, medición y aprendizaje al ciclo de producto; no confunde que una implementación funcione técnicamente con que el servicio sea útil, seguro o efectivo.
- `servicios/soporte_usuario`: Protege la confianza del usuario y evita que una interacción de soporte se convierta en consejo clínico o una promesa no autorizada.
