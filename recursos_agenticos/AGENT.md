# Departamento: recursos_agenticos

## Propósito
Unidad operativa. Líder canonical: `recursos_agenticos/desarrollo_organizacional` (Desarrollo Organizacional (Recursos Agénticos)).

## Qué produce
- `recursos_agenticos/desarrollo_organizacional`: Evaluar y proponer la evolución del organigrama de agentes (nuevos roles, fusión o retiro de roles existentes). La decisión final de implementar un cambio de organigrama es humana — este rol propone, Eduardo aprueba.

## Líder y workers
- Líder: `recursos_agenticos/desarrollo_organizacional`
- (inactivo, `proposed_profile_required`) `recursos_agenticos/curador_catalogo`
- (inactivo, `proposed_profile_required`) `recursos_agenticos/disenador_perfiles`
- (inactivo, `proposed_profile_required`) `recursos_agenticos/disenador_skills`
- (inactivo, `proposed_profile_required`) `recursos_agenticos/evaluador_agentes`
- (inactivo, `proposed_profile_required`) `recursos_agenticos/investigacion_ra`

Los cinco workers figuran en `source-manifest.yaml` pero sus `PERFIL.md`
nunca se importaron al repositorio. Hasta que el owner apruebe D-006 y los
perfiles existan en el árbol, el departamento opera únicamente con su
líder, que no puede delegarles trabajo.

## Delegación y escalamiento
La delegación dentro del departamento sigue `leader-worker-map.yaml`.
Cualquier decisión fuera del alcance de un rol se escala a su líder;
el líder escala a CEO o al owner humano según `reports_to` de cada rol.

## Fronteras con otros departamentos
Este AGENT no concede autoridad, capacidades ni ruteo de modelo:
esas decisiones viven exclusivamente en `docs/canonical/`
(`capability-matrix.yaml`, `model-routing.yaml`, `instruction-precedence.yaml`).

La tecnología de RAG/retrieval/embeddings es propiedad de `ingenieria_ia`
(`ingenieria_ia/semantic_engineer`); este departamento la consume, no la posee.

## Qué NO produce / qué decisiones no puede tomar
- No crea, activa ni retira roles ni skills por sí mismo: propone (`organization.propose_role`, `organization.propose_skill`) y el owner aprueba.
- No edita `docs/canonical/` ni ningún `PERFIL.md` o `AGENT.md` directamente; una propuesta de perfil es un entregable, no un cambio aplicado.
- No evalúa ni compara modelos de IA (eso es `ingenieria_ia/ml_data_scientist`) ni decide ruteo de modelo.
- No delega trabajo a roles inactivos ni a roles de otros departamentos.
