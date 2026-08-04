# ADR-0002: PostgreSQL como fuente de verdad y motor durable inicial

Estado: propuesto para aprobación en Rama 0.

## Decisión

PostgreSQL almacenará organización, proyectos, tareas, ejecuciones, leases, mensajes, outbox, auditoría, memoria y metadatos de artefactos. La cola inicial utilizará `FOR UPDATE SKIP LOCKED`; las goroutines solo ejecutan trabajos ya persistidos.

## Consecuencias

La primera versión no requiere NATS. La interfaz de eventos deberá permitir incorporar un bus durable posteriormente sin cambiar el dominio.
