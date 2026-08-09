# ADR-0006: IR tipada hacia Prolog/Datalog en shadow, Go autoritativo

Estado: decidido (resuelve D-007), rama 30.

## Decisión

Arquitectura híbrida: Go es autoritativo; una representación intermedia tipada se compila hacia Prolog/Datalog aislado en shadow. Ninguna divergencia cambia decisiones productivas hasta superar paridad, auditoría y promoción explícita.

Este texto es la decisión literal del owner y no se parafrasea; queda también registrada en `docs/canonical/decisions-required.yaml` bajo `resolved: D-007`.

## Contexto

ADR-0003 ya estableció el modo sombra (comparar sin bloquear) y `internal/shadowverifier` ya lo implementa para un conjunto fijo de hechos organizacionales (existencia de rol/departamento, liderazgo, mensajería, delegación, capability, dependencias) re-derivados en memoria contra los motores Go reales — ver `internal/shadowverifier/{types,service,derive,matrix}.go`. Eso resuelve la "Fase 1" que el propio ADR-0003 delimitó (tableaux queda para después) pero no es un motor de reglas lógicas general ni tiene una representación intermedia (IR) versionada: los hechos y su comparación están codificados directamente en Go, no en un formato compilable hacia Prolog/Datalog.

D-007 preguntaba qué implementación de lógica de producción usar tras la fase sombra. La decisión del owner no reemplaza a Go como motor autoritativo — lo mantiene así de forma permanente — y añade un solver lógico externo (Prolog/Datalog) como verificador en sombra sobre una IR tipada y versionada, no como reemplazo del camino crítico.

## Alcance de esta rama (R30)

R30 **no** implementa el solver completo (eso es una rama futura, R34+). R30 solo corrige/introduce lo mínimo necesario para que la decisión de arriba sea ejecutable más adelante sin retrabajo:

- Esquema de IR versionado (tipos Go, con número de versión explícito en cada programa serializado).
- Eventos necesarios para alimentar el shadow (qué se registra, cuándo).
- Contrato de comparación Go-vs-solver (forma del resultado, no el solver en sí).
- Almacenamiento de divergencias (qué se persiste cuando Go y el solver no coinciden).
- Límites duros de tiempo, profundidad y número de soluciones por evaluación.
- Prohibición explícita, a nivel de contrato: sin acceso a red, sin escritura de archivos, sin predicados peligrosos (efectos secundarios, invocación de comandos), y prohibición de enviar cualquier cadena de razonamiento (chain-of-thought) privada del modelo al solver o a su almacenamiento.

## Consecuencias

- `internal/shadowverifier` sigue siendo la implementación vigente de ADR-0003 para hechos organizacionales; no se reemplaza en R30.
- La IR y sus límites/prohibiciones se diseñan para que un futuro solver real (Prolog/Datalog embebido o externo) pueda conectarse sin cambiar el contrato ya fijado aquí.
- Ninguna decisión productiva puede depender del resultado del solver hasta que se documente paridad medida, auditoría del comportamiento en shadow, y una promoción explícita — igual que el patrón ya usado por `internal/improvement` para candidatos (`proposed → validated → evaluating → approved → canary → active`).
- D-005 (identidad real del proveedor detrás de `gpt-5.6-luna`) permanece abierto; esta ADR no lo toca.
