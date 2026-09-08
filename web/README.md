# Explorarte · Centro de operaciones

Frontend de la organización: oficina por departamentos, actividad por puesto,
misiones, aprendizaje, skills, recuerdos, costos y conversación con el CEO.

## Ejecutar

Requiere Node.js 22 o superior. No necesita instalar paquetes.

```sh
cd web
npm run dev
# http://127.0.0.1:4173
npm test
npm run check
npm run build
npm run preview
```

El servidor de desarrollo escucha sólo en loopback. `dist/` es un artefacto
estático; este cambio no lo publica ni despliega.

## Demostración y conexión

- `/?mode=demo`: datos simulados, identificados de forma permanente. El chat
  responde localmente; crear una campaña no invoca modelos ni genera gasto real.
- `/?mode=live`: consulta los endpoints del mismo origen. Un error de conexión
  muestra indisponibilidad; nunca se sustituye por métricas de demostración.

La demo visual de oficinas también puede leer el VPS mediante un túnel SSH
local: `npm run demo:live` y abrir
`http://127.0.0.1:4185/?source=vps`. Ese flujo consume el snapshot y los
reportes de misión en modo de solo lectura; la ruta `npm run demo` permanece
completamente simulada.

Los puestos de demostración usan IDs del catálogo canonical. Su actividad no
describe el estado productivo. Investigación permanece como unidad transversal
independiente. La UI no modifica routing, capacidades, approval ni activación.

`/mision <objetivo>` prepara una campaña con presupuesto y confirmación explícita.
Los importes del contrato se expresan en enteros de microusd (1 USD = 1 000 000).
Gasto real, estimado y presupuesto deben mantenerse separados.

## API pendiente de conectar

Al inspeccionar la base `54f7f8dd228765185147cb158ad1447334d1f7da`, el servidor
`internal/platform/httpserver` sólo exponía `/healthz`, `/readyz` y `/version`.
Los siguientes endpoints son el contrato de integración de este frontend, **no
endpoints implementados por este cambio**:

| Método | Endpoint | Resultado |
| --- | --- | --- |
| GET | `/api/organization/snapshot` | Estado y métricas, con esquema de `src/data.js` |
| POST | `/api/organization/ceo/messages` | `{ "message": "respuesta" }` |
| POST | `/api/organization/missions` | `{ "mission": {...}, "message": "..." }` |

Chat recibe `{ "message": "texto" }`. Misiones reciben `objective` y
`budgetMicrousd`, con `Idempotency-Key`. La implementación exacta del cliente está
en `src/api.js`. El servidor deberá autenticar al usuario mediante su sesión,
autorizar su organización, validar límites y aplicar protección CSRF. El cliente
no contiene tokens de proveedores ni fija un actor autorizado.

El flujo existente para campañas es `orgctl executive submit`, que utiliza
`executivebootstrap.Open` y `Orchestrator.Submit` con un `SubmitRequest`, presupuesto
durable e idempotencia. El futuro endpoint debe entrar por ese límite de aplicación,
con autorización host-owned; no ejecutar comandos shell construidos con el chat.
Una frase libre necesita convertirse en el contrato completo de objetivo del
Executive antes de aceptarse. No se presupone que texto libre sea ese contrato.

Las métricas conectadas deberán provenir de tasks/attempts y eventos del Harness,
Executive, MemoryOS, Skill Registry y ledger de costos. Los datos ausentes se
reportan como ausentes, nunca como cero. No enviar al navegador prompts privados,
razonamiento oculto, credenciales ni contenido clínico.

## Estado de entrega

Branch de trabajo: `feat/organization-command-center`. Frontend aislado del trabajo
simultáneo de Skill Forge. Sin cambios de schema, producción ni publicación.
