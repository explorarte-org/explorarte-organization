# Departamento: recursos_agenticos

## Propósito
Unidad operativa. Líder canonical: `recursos_agenticos/desarrollo_organizacional` (Desarrollo Organizacional (Recursos Agénticos)).

## Qué produce
- `recursos_agenticos/desarrollo_organizacional`: Evaluar y proponer la evolución del organigrama de agentes (nuevos roles, fusión o retiro de roles existentes), y coordinar al resto del departamento: Diseñador de Perfiles, Capacitador, Evaluador de Habilidades/Benchmark.

## Líder y workers
- Líder: `recursos_agenticos/desarrollo_organizacional`
- Worker: `recursos_agenticos/curador_catalogo`
- Worker: `recursos_agenticos/disenador_perfiles`
- Worker: `recursos_agenticos/disenador_skills`
- Worker: `recursos_agenticos/evaluador_agentes`
- Worker: `recursos_agenticos/investigacion_ra`

## Delegación y escalamiento
La delegación dentro del departamento sigue `leader-worker-map.yaml`.
Cualquier decisión fuera del alcance de un rol se escala a su líder;
el líder escala a CEO o al owner humano según `reports_to` de cada rol.

## Fronteras con otros departamentos
Este AGENT no concede autoridad, capacidades ni ruteo de modelo:
esas decisiones viven exclusivamente en `docs/canonical/`
(`capability-matrix.yaml`, `model-routing.yaml`, `instruction-precedence.yaml`).

## Qué NO produce / qué decisiones no puede tomar
