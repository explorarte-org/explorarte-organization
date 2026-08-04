# ADR-0001: Kernel organizacional modular en un solo binario Go

Estado: propuesto para aprobación en Rama 0.

## Decisión

La primera versión se desplegará como un único binario Go modular. Las fronteras internas serán explícitas y comprobables; los departamentos y agentes no se convierten en microservicios. PostgreSQL será una dependencia externa y las células serán sistemas independientes.

## Motivo

La v1 sufrió por árboles y despliegues divergentes. Un solo artefacto reduce esa clase de error, mientras que la modularidad interna evita convertirlo en un monolito sin límites.

## Consecuencias

Un panic no contenido puede afectar todo el proceso, por lo que cada frontera concurrente debe recuperar, registrar y cancelar de forma controlada. La separación física se reserva para células y componentes que realmente necesiten un failure domain distinto.
