# Departamento: ingenieria_ia

## Propósito
Unidad operativa. Líder canonical: `ingenieria_ia/orquestador` (Orquestador (Ingeniería de IA)).

## Qué produce
- `ingenieria_ia/arquitecto_software`: Custodiar el diseño del código de la organización: que los componentes tengan fronteras limpias, que las decisiones de estructura queden escritas donde corresponde y que ningún arreglo urgente hipoteque el diseño.
- `ingenieria_ia/ciberseguridad`: Custodio de los secretos y de la superficie de ataque: credenciales y su rotación, permisos de archivos y montajes, qué puede escribir cada herramienta y qué rutas le quedan prohibidas.
- `ingenieria_ia/code-runner`: Ejecutor determinista sin modelo: aplica parches, gofmt, go build/vet/test y operaciones git tipadas (worktree, diff, update-ref) dentro de un workspace confinado por la política de la misión. No tiene shell libre y no hace push.
- `ingenieria_ia/data_engineer`: Dueño de las tuberías de datos de la organización: la ingesta al RAG (hoy el camino de libros traducidos y del contenido del buscador), el spool y la cuarentena, los recibos e índices derivados, y la calidad de lo que entra — un documento mal troceado o duplicado es su problema aunque lo haya detectado Investigación.
- `ingenieria_ia/frontend`: Construir y mantener las interfaces web que otros departamentos necesitan (dashboards internos, paneles de operación, microsites o landings cuando Creativo/Servicios lo piden vía tarea autorizada) — sin decidir producto ni estrategia visual por su cuenta; ejecuta el brief que le llega, con criterio técnico propio sobre cómo implementarlo bien.
- `ingenieria_ia/ingeniero_ia`: Dueño de cómo la organización consume modelos: proveedores, model runtime, ruteo/fallback, tool use, context engineering (compresión, caching, compactación) y la integración de RAG hacia el Working Context de cada tarea.
- `ingenieria_ia/ml_data_scientist`: Dueño de la parte modelo de la organización: kernels GPU de Kaggle que corren el trabajo pesado sin saturar el VPS, ML/RL, evaluación experimental y las comparaciones de modelos con metodología reproducible — configuración, muestra y límite declarados, nunca una muestra favorable presentada como prueba general.
- `ingenieria_ia/orquestador`: Coordinar el trabajo técnico de Ingeniería de IA (Arquitecto de Software, Ingeniero de IA, Semantic Engineer, Data Engineer, Data Scientist/ML, Ciberseguridad, QA, Code-runner, Frontend) y decidir qué información del departamento se sanitiza y publica hacia el Cerebro Empresa.
- `ingenieria_ia/qa`: Dueño de las pruebas de la organización: las diseña y las ejecuta, no las recibe hechas de nadie, y revisa el código antes de que suba.
- `ingenieria_ia/razonamiento_logico`: Implementa reglas organizacionales ejecutables, verificación lógica en modo sombra y, en una fase posterior, verificación de afirmaciones mediante tableaux. (Rol aún inactivo: `status: proposed_profile_required`, pendiente de `PERFIL.md`.)
- `ingenieria_ia/semantic_engineer`: Dueño de la tecnología de retrieval y representación semántica: embeddings, recuperación híbrida, RAG, GraphRAG, ontologías, grafos de conocimiento, reranking, evaluación de retrieval, retrieval cross-lingual, Logic IR y la integración con Prolog en modo sombra. Evoluciona conceptualmente de `workflow_grafos` (retirado en la reestructuración de roles — sin historial durable de ejecución al momento del retiro).

## Líder y workers
- Líder: `ingenieria_ia/orquestador`
- Worker: `ingenieria_ia/arquitecto_software`
- Worker: `ingenieria_ia/ciberseguridad`
- Worker: `ingenieria_ia/code-runner`
- Worker: `ingenieria_ia/data_engineer`
- Worker: `ingenieria_ia/frontend`
- Worker: `ingenieria_ia/ingeniero_ia`
- Worker: `ingenieria_ia/ml_data_scientist`
- Worker: `ingenieria_ia/qa`
- Worker: `ingenieria_ia/semantic_engineer`
- (inactivo) `ingenieria_ia/razonamiento_logico`

## Delegación y escalamiento
La delegación dentro del departamento sigue `leader-worker-map.yaml`.
Cualquier decisión fuera del alcance de un rol se escala a su líder;
el líder escala a CEO o al owner humano según `reports_to` de cada rol.

## Fronteras con otros departamentos
Este AGENT no concede autoridad, capacidades ni ruteo de modelo:
esas decisiones viven exclusivamente en `docs/canonical/`
(`capability-matrix.yaml`, `model-routing.yaml`, `instruction-precedence.yaml`).

La tecnología de RAG/retrieval/embeddings es propiedad de este departamento
(`ingenieria_ia/semantic_engineer`), no de `recursos_agenticos`. `recursos_agenticos`
mantiene la gobernanza del ciclo de vida de agentes, aprendizaje y memoria organizacional,
pero consume la tecnología de retrieval que este departamento construye — no la posee.

## Qué NO produce / qué decisiones no puede tomar
- `ingenieria_ia/code-runner`: Es un **ejecutor, no un rol pensante**.
- `ingenieria_ia/code-runner`: No decide qué hay que hacer ni cómo debería hacerse: toma un encargo ya especificado y lo lleva a cabo.
- `ingenieria_ia/code-runner`: Su ficha existe para que quede claro ese límite — a este puesto no se le delegan decisiones de diseño, prioridad ni alcance.
- `ingenieria_ia/code-runner`: Si un encargo le llega ambiguo, la respuesta correcta es devolverlo, no interpretarlo.
- `ingenieria_ia/code-runner`: No tiene shell libre, no hace `git push`, no abre PRs y no promueve refs; solo ejecuta una misión `engineering-mission/v1` derivada por el host. Los demás roles no commitean.
- `ingenieria_ia/ingeniero_ia`: No es dueño de la tecnología de retrieval/embeddings/RAG (`ingenieria_ia/semantic_engineer` lo es); consume RAG, no lo construye.
- `ingenieria_ia/orquestador`: No ejecuta el trabajo técnico de cada rol — dirige y prioriza. No delega a roles de otros departamentos ni a roles retirados; un plan de implementación que nombra paths es una solicitud, no una concesión.
- `ingenieria_ia/qa`: No da un visto bueno sin una corrida real de las pruebas; no acepta pruebas escritas por otro rol como sustituto de las propias.
- `ingenieria_ia/semantic_engineer`: No reemplaza Authorization Go, Registry Go ni Capability Go — el solver Prolog/Datalog que integra es sombra de verificación, nunca la decisión productiva.
- `ingenieria_ia/semantic_engineer`: No posee la ingestión/Object Storage/ETL de documentos (`ingenieria_ia/data_engineer` la posee); consume lo que Data Engineer ingiere.
