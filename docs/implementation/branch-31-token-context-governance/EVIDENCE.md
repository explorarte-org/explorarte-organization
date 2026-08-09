# R31 — Verificación de los informes de optimización de tokens y contexto (2026)

## Método

Las afirmaciones se clasifican así:

- **A — fuente primaria y aplicable:** especificación oficial, documentación del proveedor o paper del autor; resuelve una brecha existente.
- **B — investigación real, aún experimental:** paper/preprint real, pero no es una garantía productiva ni una norma consolidada.
- **C — evidencia comercial o caso particular:** puede orientar una hipótesis, no fijar un gate universal.
- **D — incorrecta, no demostrada o fuera de alcance:** no se incorpora a la arquitectura.

Los porcentajes de un benchmark, proveedor o corpus no se trasladan a esta aplicación. R31 debe reproducir el efecto con sus modelos, prompts, tokenizers, roles y datos.

## Veredicto ejecutivo

Los informes identifican correctamente el problema central: un agente de larga duración no debe anexar indefinidamente prompts, herramientas, logs y memoria. También aciertan en separar el estado auditable del contexto activo, medir el caché, recuperar detalle bajo demanda y usar comunicación multiagente esparsa.

No son, sin embargo, especificaciones listas para instalar. Mezclan papers recientes, borradores de protocolos, casos comerciales y cifras de marketing como si tuvieran el mismo grado de certeza. También contienen al menos un error objetivo —TOON— y varias generalizaciones dependientes del proveedor. La implementación debe extraer patrones y rechazarlos cuando no correspondan a la arquitectura actual.

El estado de partida tampoco debe exagerarse: R30.1 pasó su disciplina completa de Go/PostgreSQL, pero BGE-M3 real sigue sin instalarse y la cobertura ejecutable del catálogo es 4/14. Esas dos brechas se registran como precondiciones, no se rellenan con inferencias del informe.

## Afirmaciones verificadas y útiles

### Contexto de larga duración

| Afirmación | Grado | Verificación y decisión |
|---|---:|---|
| El full-append produce consumo acumulado cuadrático | A | Se deriva directamente de sumar el prefijo creciente por turno. Es aplicable y justifica medir tokens por trayectoria, no solo por llamada. |
| ACM externaliza mensajes crudos y permite `manage_context`/consulta posterior | B | Existe: [ACM, arXiv:2607.23809](https://arxiv.org/abs/2607.23809). Adoptar la separación lossless, no asumir que los modelos API saben decidir solos cuándo compactar. |
| AdaCoM usa un gestor externo para agentes congelados y observa un fidelity–reliability trade-off | B | Existe: [AdaCoM, arXiv:2605.30785](https://arxiv.org/abs/2605.30785). Útil como hipótesis de perfiles por capacidad; no entrenar un manager en R31. |
| MAGE representa memoria como árbol de estado con Grow/Compress/Maintain/Revise | B | Existe: [MAGE, arXiv:2606.06090](https://arxiv.org/abs/2606.06090). Encaja con el `decisiongraph` actual; adaptar checkpoints, no copiar el framework. |
| PRO-LONG mantiene logs completos y consulta programáticamente | B | Existe: [PRO-LONG, arXiv:2607.20064](https://arxiv.org/abs/2607.20064). Su principio append-only + búsqueda determinista es aplicable a logs y artifacts. |
| EvoSOP destila procedimientos reutilizables | B | Existe: [EvoSOP, arXiv:2607.07321](https://arxiv.org/abs/2607.07321). Candidato posterior para skills gobernadas, no requisito del renderer. |
| DeLM usa cola compartida y contexto admitido/verificado | B | Existe: [DeLM, arXiv:2606.10662](https://arxiv.org/abs/2606.10662). La admisión verificada coincide con la filosofía actual; descentralizar el CEO no es necesario en R31. |
| SDO evita reinyectar toda observación GUI si no cambió una señal relevante | B | Existe: [SDO, arXiv:2606.06708](https://arxiv.org/abs/2606.06708). Patrón útil si se añade computer-use; no aplica al flujo Go actual. |

### Retrieval y memoria

| Afirmación | Grado | Verificación y decisión |
|---|---:|---|
| xMemory desacopla componentes antes de agregarlos y recupera top-down | B | Existe: [xMemory, arXiv:2602.02007](https://arxiv.org/abs/2602.02007). Útil para diseño futuro de consolidación; R31 solo hace selección progresiva y referencias. |
| Mem0 combina memoria vectorial/grafo y reduce contexto en sus evaluaciones | B/C | El paper existe: [Mem0, arXiv:2504.19413](https://arxiv.org/abs/2504.19413); las cifras 92.5/6,956 de 2026 son [resultados publicados por Mem0](https://mem0.ai/research), no validación independiente del sistema local. No adoptar la plataforma. |
| Un top-k plano puede devolver evidencia redundante | A | Es un riesgo conocido y comprobable localmente. R31 medirá diversidad y deduplicación, conservando procedencia. |
| RAG documental y estado de ejecución no son intercambiables | A | Coincide con el código: RAG/Memory recuperan conocimiento, mientras `decisiongraph` modela decisiones. No guardar trazas fallidas como conocimiento aprobado. |

### Caché, costes y proveedores

| Afirmación | Grado | Verificación y decisión |
|---|---:|---|
| El prefijo estable mejora la probabilidad de prompt-cache hit | A | La documentación oficial de [OpenAI Prompt Caching](https://platform.openai.com/docs/guides/prompt-caching) describe coincidencia de prefijos y expone `cached_tokens`. Aplicar solo al endpoint real verificado. |
| GPT-5.6 ofrece entrada cacheada mucho más barata | A, condicionado | La [documentación oficial de GPT-5.6](https://openai.com/index/gpt-5-6/) anuncia la tarifa del modelo OpenAI; D-005 sigue impidiendo equiparar automáticamente `gpt-5.6-luna` con ese endpoint. |
| DeepSeek expone hits/misses de caché | A | La [API de DeepSeek](https://api-docs.deepseek.com/api/create-completion) reporta `prompt_cache_hit_tokens` y `prompt_cache_miss_tokens`; el adapter local hoy los descarta. Es una brecha directa de R31. |
| El precio cache hit de DeepSeek es distinto del miss | A | La [tabla oficial de precios](https://api-docs.deepseek.com/quick_start/pricing/) lo confirma y varía por modelo. El ledger debe usar contadores reales, no cero fijo. |
| GitHub redujo 62% en un workflow con instrumentación y poda | C | Es un caso real descrito por [GitHub Engineering](https://github.blog/ai-and-ml/github-copilot/improving-token-efficiency-in-github-agentic-workflows/), no una promesa universal. Adoptar el patrón `token-usage.jsonl`/medición, no el porcentaje. |
| LLMLingua puede comprimir prompts fuertemente | B | [Microsoft Research](https://www.microsoft.com/en-us/research/project/llmlingua/llmlingua/) lo publica como investigación y reconoce el compromiso compresión/fidelidad. Solo canario posterior, nunca sobre políticas autoritativas. |

### Multiagente y protocolos

| Afirmación | Grado | Verificación y decisión |
|---|---:|---|
| S²-MAD reduce comunicación redundante con debate selectivo | B | Existe: [S²-MAD, arXiv:2502.04790](https://arxiv.org/abs/2502.04790). Sus máximos de ahorro son de sus datasets; aquí solo se adopta revisión selectiva y bounded. |
| MCP pasó a una base stateless en la revisión 2026-07-28 | A/B | El [anuncio oficial MCP](https://blog.modelcontextprotocol.io/posts/2026-07-28/) documenta retiro de `initialize/initialized` y `Mcp-Session-Id`. Es reciente y no obliga a esta aplicación, que aún no usa MCP. |
| MCP pertenece al ecosistema AAIF/Linux Foundation | A | La [Linux Foundation anunció AAIF](https://www.linuxfoundation.org/press/linux-foundation-announces-the-formation-of-the-agentic-ai-foundation) en diciembre de 2025. Esto no vuelve obligatoria una migración. |
| CA-MCP propone un Shared Context Store | B | Existe: [CA-MCP, arXiv:2601.11595](https://arxiv.org/abs/2601.11595). Es experimental y queda fuera hasta existir una topología MCP real. |
| WIMSE trata identidad de workloads entre sistemas | B | Existe como [borrador IETF WIMSE](https://datatracker.ietf.org/doc/html/draft-ietf-wimse-arch-07), no como RFC final universal. Útil en una futura fase cross-organización. |
| C1–C7 es una taxonomía de seguridad multi-organizacional | B | Proviene de un position paper: [Seven Security Challenges, arXiv:2505.23847](https://arxiv.org/abs/2505.23847). No es un estándar ni prueba que esas métricas estén resueltas. |
| MINIM reduce contexto UI con privacidad local | B | Existe: [MINIM, arXiv:2606.13949](https://arxiv.org/abs/2606.13949). Fuera de alcance mientras no haya agente GUI/cross-domain. |

## Afirmaciones incorrectas o que deben corregirse

### TOON

El informe expande TOON como “Targeted Output Optimization Network”. Es incorrecto. La especificación pública define **Token-Oriented Object Notation** y todavía es un working draft: [toon-format/spec](https://github.com/toon-format/spec). Tampoco existe evidencia universal de que TOON supere a un texto delimitado para los tokenizers usados por Luna y DeepSeek. R31 lo puede medir, no adoptar por nombre.

### XML como compresión garantizada

XML aporta delimitación, pero repite etiquetas y puede consumir más tokens que JSON compacto o texto estructurado. La eficiencia depende del tokenizer y del contenido. La decisión correcta es benchmarkear representaciones preservando exactamente la misma semántica y seguridad.

### “Los outputs cuestan siempre 3–5×”

Es una generalización. La relación cambia por proveedor y modelo; algunos precios están fuera de ese intervalo. El ledger ya tiene precios versionados y debe resolver la tarifa exacta efectiva, no aplicar un multiplicador fijo.

### “Cinco servidores MCP consumen 55k–134k tokens siempre”

El costo depende de cliente, schemas y herramientas cargadas. MCP no obliga a inyectar un catálogo completo en cada turno. Además, el runtime actual no usa MCP ni envía un catálogo semejante; implementar Tool Search hoy sería optimizar una carga inexistente.

### “TOON principles” y códigos abreviados

Proyectar campos, eliminar nulls y usar respuestas acotadas son buenas prácticas de API, pero no constituyen el significado normativo de TOON. Los códigos crípticos como `OPN` pueden reducir tokens y a la vez aumentar ambigüedad; solo son aceptables bajo un schema versionado y medido.

### “RTK = Retrieval-Based Token Compression”

El informe usa RTK para más de una idea y lo presenta como paradigma consolidado sin una fuente primaria inequívoca. No se convierte en componente. Los patrones concretos —filtrado local de logs, artifacts externos y retrieval— se evalúan por separado.

## Evidencia comercial que no debe fijar requisitos

- NeuralTrust/TrustGate/TrustLens son productos reales, pero sus “cinco palancas”, porcentajes de ahorro y estadísticas macro son material del proveedor. La organización ya tiene un gate y ledger propios; no se añade dependencia.
- KanseiLink/ARI es un índice operado por el propio proveedor y sus cifras públicas han cambiado entre páginas/fechas. No fundamenta una migración.
- Las afirmaciones “80% de caída de precio”, “USD 8.4B”, “85% del presupuesto”, “60% de proyectos con 30–50% de sobrecosto” necesitan datasets/metodología primarios antes de aparecer como hechos canónicos.
- “Un workflow agéntico hace más de 15 llamadas” es un ejemplo plausible, no una constante arquitectónica.
- Los ahorros agregados de 70–95% no se usan como objetivo de aceptación.

## Técnicas reales pero fuera de alcance

| Técnica | Razón de exclusión actual |
|---|---|
| MixKV | [MixKV](https://arxiv.org/abs/2510.20707) modifica el KV cache interno de LVLMs en GPU. La organización consume LLMs por API; BGE-M3 es un embedder CPU. |
| VisPruner/VTW/SeTok/M3 visual | Requieren control del modelo multimodal o un pipeline visual que hoy no existe. |
| CA-MCP y WIMSE | No hay red MCP/cross-organización en producción. Diseñarlos ahora añade superficie sin consumidor. |
| Entrenamiento ACM/AdaCoM | Los modelos ruteados son APIs cerradas. Primero se implementa un gestor Go determinista y observable. |
| Mem0 como servicio | Duplicaría governance, lifecycle, Postgres y embeddings ya existentes. Solo se toman ideas comprobables. |
| NeuralTrust/KanseiLink | Sustituirían piezas internas sin demostrar una brecha que el sistema no pueda cubrir. |

## Qué sí entra a R31

1. Telemetría exacta de uso y caché por proveedor.
2. Snapshot canónico separado de la vista compacta de ejecución.
3. Presupuesto por tokens/model profile y bytes defensivos.
4. Prefijo estable medible.
5. RAG/Memory progresivos, con relevancia real, diversidad y fetch autorizado por referencia.
6. Checkpoints verificados y logs append-only para larga duración.
7. Mensajería tipada, bounded y por delta/artifact.
8. Revisión selectiva por riesgo/contradicción, no debate total.
9. Autoauditoría que propone pero no autopromueve.
10. Canario de ingesta BGE-M3 después de probar el sidecar real.

## Regla de evidencia futura

Toda nueva afirmación que pretenda cambiar arquitectura debe registrar:

```text
claim_id
fuente primaria y fecha
tipo: spec | paper | provider-doc | vendor-case
supuesto que requiere
brecha local demostrada
métrica baseline
canario/rollback
decisión: adopt | experiment | defer | reject
```

Sin esa ficha, el contenido puede entrar al RAG como material de investigación, pero no convertirse en regla canónica ni autorización de implementación.
