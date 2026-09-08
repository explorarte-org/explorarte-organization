# Demo de misiones

Para la maqueta aislada ejecutar `npm run demo` desde este directorio y abrir http://127.0.0.1:4184. Para abrir la misma oficina con datos del VPS ejecutar `npm run demo:live` y abrir http://127.0.0.1:4185/?source=vps. El segundo comando crea un túnel SSH de solo lectura hacia el API interno del VPS y levanta un proxy local para la interfaz; no expone la dirección interna del servicio al navegador.

En el VPS, `npm run serve:vps` sirve esta misma carpeta en el origen público y proxy las rutas `/api/` hacia `127.0.0.1:8080`. La unidad `deployments/explorarte-web.service` deja ese proceso administrado por systemd, con el frontend y el backend bajo el mismo origen.
La demo implementa la opción A aprobada: un piso pixel art conectado de seis oficinas y una barra lateral contextual. La maqueta de referencia está en `.impeccable/approved-office.png`. El arte del piso y los personajes se generó para esta demo; los personajes tienen movimiento sutil durante la reproducción.

El modo local sirve archivos mediante una lista explícita y no hace llamadas externas. El modo VPS conserva esa lista y añade únicamente las rutas `GET /api/organization/snapshot` y `GET /api/organization/missions/:id/report`, reenviadas por `scripts/serve-demo.js` al túnel local. La entrada es `mission-demo.html`; el frontend previo mantiene su entrada original.

Tres escenarios ficticios permiten seleccionar misión, oficina o puesto, consultar tarea/evidencia/actividad, reproducir/pausar, avanzar etapas y reiniciar. La barra lateral cambia entre **Chat**, **Estado CEO**, **Tarea**, **Evidencia** y **Actividad** (y muestra **Resultado** al cerrar una misión), sin mezclar el chat con el inspector del puesto. Cada puesto tiene una secuencia simulada por fase: pensamiento de planificación, discusión, investigación, ejecución, revisión o disponibilidad. El globo del personaje, el estado y la pestaña Actividad se derivan de ese mismo estado e incluyen herramienta, salida y próximo paso. El progreso vive en memoria y se restablece al recargar. CEO e Investigación siempre están presentes. Los otros perfiles dependen de la misión; los departamentos sin asignación conservan su sala vacía y cada sala muestra su estado. Zoom de 100% a 200%, centrado y directorio de puestos complementan el plano. En móvil se ve el piso completo y seleccionar un puesto lleva el foco al inspector; Volver al puesto restaura el foco. Mientras llega un refresco del VPS, el chat conserva el foco, el texto y el cursor para que se pueda seguir escribiendo sin volver a pulsar el cuadro.

La evidencia de ejemplo aparece desde Validar y se consulta en la página. Al completar una misión se muestra una plantilla enriquecida con resumen, entregables, decisiones, evidencias y siguiente ciclo; la pestaña Resultado repite el cierre para el puesto seleccionado. En modo VPS esos campos se construyen a partir del snapshot, del plan ejecutivo, de las revisiones departamentales y de las auditorías de especialistas del reporte real. El piso se actualiza cada cinco segundos y el botón `Actualizar VPS` fuerza una lectura; la interfaz es de solo lectura. Los globos y la pestaña Actividad muestran resúmenes de tareas observados, nunca cadenas de razonamiento interno. Agent Office continúa siendo el destino visual posterior; esta entrega conecta el adaptador de proyección directamente con el VPS sin sustituir el runtime del kernel.

### Chat del CEO y `/mision`

El panel **Habla con el CEO** usa el cliente existente de `src/api.js`. En `?source=vps`, un mensaje normal hace `POST /api/organization/ceo/messages` con `{ "message": "..." }` y muestra la respuesta devuelta por la API. El botón **Resumen** sólo completa el texto; Enter envía y Shift+Enter conserva una línea nueva.

Escribe `/mision <objetivo>` para abrir la confirmación de la directriz. Antes de enviar se puede editar el objetivo y el presupuesto en USD (por defecto, 5). **Confirmar e iniciar** convierte el importe a microUSD, genera una clave `Idempotency-Key` y hace `POST /api/organization/missions`. La oficina vuelve a consultar el snapshot después de la respuesta. Cancelar no llama al backend. El modo local reproduce el mismo flujo en memoria y no genera gasto.

## Módulos

- `src/mission-demo/model.js`: escenarios, equipos y transición de etapa.
- `src/mission-demo/projection.js`: contrato OfficeProjection y fuente simulada con versión/cursor, asignaciones, presencia, actividad y eventos.
- `src/mission-demo/remote.js`: adaptador VPS que normaliza snapshot/reportes al mismo contrato, garantiza CEO e Investigación, asigna oficinas y deriva globos, actividad, evidencia y cierre.
- `src/mission-demo/floor.js`: geometría del piso y composición de personajes, independiente del contrato.
- `src/mission-demo/panels.js`: inspector, chat del CEO, evidencia de ejemplo, actividad y listado de misiones.
- `src/mission-demo/app.js`: selección, chat `/mision` y simulación local o lectura VPS según `?source=vps`.
- `src/mission-demo/styles.css`: controles y estructura general.
- `src/mission-demo/offices.css`: distribución y capas de las oficinas departamentales.
- `src/mission-demo/assets/`: nuevo arte local y procedencia en OFFICE-A-PROVENANCE.md. Los recursos anteriores se conservan pero no se cargan.
- `scripts/serve-demo.js`: servidor estático independiente.
- `scripts/serve-live.js`: túnel SSH y proxy local para el modo VPS.

## Verificación

`npm run build`, `npm test`, `npm run check`.
Con Playwright disponible: `npm run test:demo` (requiere servidor local activo). Opcionalmente configurar `PLAYWRIGHT_MODULE` y `CHROMIUM_PATH`.
La prueba cubre ocupación según misión, puestos permanentes, selección de tarea/oficina, evidencia, pestañas por teclado, etapas, reproducción y foco, reinicio, zoom, navegación, ausencia de desborde móvil y ausencia de solicitudes externas, llamadas API o errores JS. Captura cuatro anchos: 1536, 1440, 1024 y 390. `test/projection.test.js` comprueba independencia de misiones, estabilidad de oficinas e identificadores y progresión acotada.

Con el túnel activo, `npm run test:demo:live` verifica snapshot y reportes del VPS, seis oficinas, CEO e Investigación permanentes, un globo por puesto, sprites coherentes, asientos sin duplicar, resultado observado y el contrato de chat/creación de misión. Los dos `POST` se interceptan en el navegador de prueba, así que la validación no crea datos ni gasto en el VPS. La prueba usa `LIVE_URL` si se necesita otro puerto. El backend se consulta mediante el alias SSH `explorarte-nuevo` de `~/.ssh/config`; no se guardan credenciales en este proyecto.

Cambios divididos por responsabilidad; este directorio no es un repositorio Git, por lo que no se generaron commits. La integración no escribe ni modifica datos en el VPS.
