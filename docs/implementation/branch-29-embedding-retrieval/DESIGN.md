# Rama 29 — Fundación de retrieval semántico por embeddings (RAG + Memory)

## Estado y base

- Rama: commits directos sobre `main` (mismo patrón que R23-R27 en esta sesión, sin checkout de feature branch separado).
- Base: `c247ee9` (`main` post R27/token-plan retirement).
- Migraciones reservadas: a partir de `000027` (confirmar el tip real de `migrations/` antes de cada commit, no asumir de antemano cuántos números va a consumir esta rama).

## Por qué esta rama existe

`internal/rag` recupera contenido con `ts_rank`/`plainto_tsquery('simple', ...)` — full-text lexical puro de Postgres. Verificado en vivo contra Postgres 17 real: `plainto_tsquery('simple','20')` no matchea un documento con "2000" (correcto), pero `"error-20"` tampoco matchea `"error 20"` porque el guión queda pegado al lexema numérico (`'-20'`) — un falso negativo real, no solo teórico.

`internal/memory` no tiene ningún mecanismo de búsqueda: `ListApproved` (`internal/memory/contextprovider/provider.go`) devuelve las últimas N entradas aprobadas por rol, ordenadas por recencia, sin ningún parámetro de texto. Es el caso concreto que motivó este trabajo: un agente que busca una lección sobre un error que cometió no tiene ninguna forma de buscar por significado — solo una ventana de recencia que descarta lecciones viejas sin importar su relevancia.

## Relación explícita con R18 y R20 (supersession declarada)

`docs/implementation/branch-18-organizational-memory/INTEGRATION.md` declara fuera de alcance: *"No implementa scratchpad, chain-of-thought, memoria clínica, RAG ni búsqueda vectorial [...] embeddings/vector index/RAG."*

`docs/implementation/branch-20-approved-knowledge-rag/DESIGN.md` (Alcance D) declara: *"R20 no debe bloquear el Context Engine esperando un proveedor externo de embeddings que todavía no existe [...] no agregar pgvector ni cambiar la imagen postgres:17-bookworm en esta rama [...] Fuera de alcance: [...] provider específico de embeddings; pgvector/vector DB externo."*

Ambas cláusulas fueron deliberadas, no descuidos — R18/R20 dejaron el schema de `internal/rag` "preparado" (columnas `embedding_model_id`/`embedding_model_version`/`embedding_dimension` en `rag_knowledge_chunks`, todas nullable, con un CHECK "todas o ninguna") a propósito, para una rama futura. **Esta rama (29) declara explícitamente que supersede esas dos cláusulas de fuera-de-alcance** y asume la responsabilidad de introducir pgvector y un proveedor de embeddings real, con la misma disciplina de gobernanza (evidencia, autorización, fail-closed en dinero, inmutabilidad de contenido aprobado) que el resto del repo.

`docs/canonical/role-catalog.yaml` ya nombra un rol futuro "Dueño de la parte modelo de la organización: embeddings y su calidad (troceado, re-embebido, evaluación...)" — este trabajo es la base técnica que ese rol eventualmente opera, aunque el rol en sí no se activa en esta rama.

## Alcance de esta rama

Fundación de retrieval semántico: gobernanza (esta rama + fitness gates), infraestructura pgvector, runtime de embeddings (Gemini, online y Batch API real como superficies distintas), tablas derivadas de embeddings (nunca mutando `rag_knowledge_chunks`/`organizational_memory_versions`), canal exacto de identificadores/números junto a FTS+vector, autorización y egress reales en cada punto de lectura/escritura nuevo, integración con `costledger`/`agentbudget`, y conexión obligatoria de `internal/memory/contextprovider` a la nueva búsqueda — sin esto último el caso motivador queda sin resolver aunque el resto se construya.

### Explícitamente fuera de alcance (candidatas a ramas futuras R30+, no resueltas ni simuladas acá)

- Chunking estructural consciente de tablas/código/DAG jerárquico de documentos.
- Inteligencia de memoria: detección automática de duplicados/contradicciones, consolidación gobernada, señal de utilidad/feedback sobre lecciones recuperadas.
- RAG agéntico completo: reformulación de query, rondas iterativas de recuperación, reranking cruzado, evaluación de cobertura, retrieval trace evidencial completo.
- Programa formal de canary/benchmark de recall/nDCG en producción (una verificación puntual de los tres canales sí es parte de esta rama, como test de integración; el programa continuo de benchmarking no).

## Diseño técnico

Ver `/home/ubuntu/.claude/plans/structured-gliding-lark.md` para el desglose completo por fases (0 a 8), decisiones de arquitectura (tablas derivadas vs. mutar filas inmutables, `billing_mode` como dimensión explícita de `modelpricing`, distinción online/Batch API real, canal exacto de identificadores, wiring de `costledger`/`agentbudget`, autorización real en `memory.Search`) y su justificación técnica.

## Corpus canónico afectado

Ningún archivo de `docs/canonical/*.yaml` cambia en esta rama — el alcance es puramente de infraestructura de datos y runtime, no de definición organizacional. Los dos scripts de fitness (`scripts/check-rag-fitness.sh`, `scripts/check-memory-fitness.sh`) sí se modifican (ver Fase 0 del plan), acotando exclusivamente las líneas que bloquean `pgvector`/`embedding`, sin tocar el resto de sus invariantes.
