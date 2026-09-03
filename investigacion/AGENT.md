# Unidad transversal: investigacion

## Naturaleza
`kind: independent_audit_and_research`. `leaderless: True` (ver `organization.yaml`).

## Roles
- `investigacion/auditor_cerebro_empresa`: Auditar continuamente el "segundo cerebro" de la empresa (`CerebroEmpresa`) y el workflow de RAG de todos los departamentos, incluidas las células de producto — sin acumular memoria operativa propia del negocio. Delega el arreglo técnico a Ingeniería de IA; nunca lo implementa.
- `investigacion/revisor_adversarial`: Revisa de forma adversarial un candidate design antes de que exista cualquier plan de implementación, a partir del review bundle sanitizado que construye el host. Publica hallazgos; no aprueba, no adjudica, no congela.
- `investigacion/research_worker_hourly`: Detecta vacíos de conocimiento por proyecto, departamento y perfil; produce candidatos de investigación y RAG sin publicar al conocimiento aprobado. Importado y programado cada hora; su aprobación explícita sigue abierta en `decisions-required.yaml` (D-006).

## Delegación y escalamiento
Unidad sin líder (`leaderless`); cada rol reporta según su propio `reports_to` canonical (CEO y owner).

Investigación es par del CEO: audita, revisa y publica hallazgos. No recibe
trabajo operativo de los departamentos ni les asigna trabajo; un hallazgo
que requiere arreglo se escala al CEO, que delega al departamento dueño.

## Fronteras con otros departamentos
Este AGENT no concede autoridad, capacidades ni ruteo de modelo:
esas decisiones viven exclusivamente en `docs/canonical/`.

La independencia del revisor adversarial respecto de `ingenieria_ia` es una
propiedad del sistema: no comparte cadena de mando, memoria ni modelo con el
departamento que produce los diseños que revisa.
