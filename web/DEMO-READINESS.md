# Revisión de preparación de la demo

Estado actual: PASS para demo local y para lectura en vivo del VPS. El piso, los assets, la actividad por puesto, los estados de oficina y el cierre enriquecido se renderizan con el mismo contrato; la fuente puede ser simulada o el adaptador de snapshot/reportes del VPS.

## Revisión inicial (histórica)

Resultado inicial: RETURN WITH FINDINGS para el objetivo de observar qué hace cada trabajador. Los puntos de ese bloque se corrigieron en la iteración posterior documentada al final.

## Evidencia

Revisión en Chromium local de las tres misiones, selección de sus 14 puestos en total y capturas de escritorio 1440×1050 y móvil 390×844. Sin respuestas HTTP fallidas ni errores de JavaScript. Capturas en `.impeccable/review/audit-mission-{0,1,2}.png` y `audit-mobile.png`.

## Hallazgos

1. **Identidad incorrecta en el inspector.** `panels.js` usa `portrait-other` para todos salvo CEO e Investigación, mientras `floor.js` usa seis sprites distintos. Operaciones, Frontend, Estratega, Analista, Diseñador de skills y Calidad pueden mostrar un retrato diferente al del plano. Unificar el catálogo visual por agente.
2. **Posición y profundidad incompletas.** Los personajes son figuras de pie superpuestas a un fondo plano. En Negocio y Recursos agénticos sus pies coinciden con superficies de escritorios, y no hay oclusión delante/detrás del mobiliario. Ajustar anclajes a suelo libre o crear poses de trabajo y capas de muebles.
3. **Nombres superpuestos en móvil.** Diseñador de skills y Evaluador se solapan a 390px. La vista general requiere etiquetas compactas y nombre completo en selección/directorio, o disposición que evite colisiones. Los 9–10px actuales también limitan legibilidad.
4. **Actividad individual ausente.** La pestaña Actividad y Eventos recientes consumen el mismo historial global de la misión. Faltan eventos con agentId, acción actual, resultado y siguiente paso por trabajador.
5. **Globo de actividad incompleto.** Solo Investigación muestra “Contrastando fuentes”, sin variar por paso ni corresponder siempre al estado Revisando. Los otros trabajadores no tienen globo; en móvil se oculta. Derivar resumen visible del mismo evento que alimenta el inspector.
6. **Progreso y evidencia genéricos.** El progreso de cada agente es phase/3 para toda la misión. La evidencia es texto de plantilla desde Validar. Para una demostración convincente faltan pasos y resultados ficticios específicos por rol.
7. **Animación limitada.** Durante reproducción todos los sprites oscilan 2px. No hay poses de escribir, investigar o revisar ni desplazamientos; todavía no representa visualmente el tipo de trabajo.

## Lo que sí funciona

Piso conectado y recursos completos, seis oficinas estables, selección de misión/persona/departamento, CEO e Investigación permanentes, ocupación filtrada por misión, zoom, controles de reproducción, consulta de tarea y avisos de simulación local.

## Orden de cierre

1. Catálogo de identidad único y anclajes/capas correctos.
2. Etiquetas móviles sin colisiones.
3. Guion simulado por agente con acción, resumen, herramienta, resultado y siguiente paso; inspector y globos derivados de esos mismos eventos.
4. Poses o animación correspondientes al guion; evidencias concretas de ejemplo.
5. Revisión visual de todas las misiones y prueba de correspondencia personaje/retrato/eventos.

La integración no expone pensamientos reales ni razonamiento interno de modelos. El adaptador consume estados y resúmenes explícitos del snapshot y de los reportes del kernel; no deduce actividad a partir de la animación. El modo VPS se mantiene de solo lectura y usa un túnel SSH local porque el API remoto escucha en loopback.

## Iteración posterior

Los hallazgos funcionales de esta revisión se implementaron en la fuente simulada y sus renderers:

- Cada agente tiene una línea de trabajo por etapa (`Pensando`, `Discutiendo`, `Investigando`, `Ejecutando`, `Revisando` o `Disponible`), con resumen, herramienta, salida, próximo paso y progreso.
- Cada puesto expone su globo derivado del mismo estado que aparece en el inspector. La pestaña Actividad ahora es individual y deja claro que es un registro simulado, no pensamiento interno.
- Cada oficina muestra estado y ocupación; el inspector de departamento lista el estado de cada participante.
- CEO, Investigación y todos los perfiles tienen un retrato coherente con su sprite del piso mediante el mismo catálogo visual.
- Los anclajes de las seis oficinas se ajustaron al frente libre de mesas y sillas; los nombres quedan fuera del mobiliario y los estados de sala se mantienen separados.
- Las etiquetas móviles usan nombres cortos y los globos se mantienen visibles en el plano reducido.
- Al llegar a Completada aparece una plantilla de resultado general con entregables, decisiones, evidencias y siguiente ciclo; el inspector añade la pestaña Resultado.

Se verificó nuevamente con `npm test`, `npm run check`, `npm run build` y `npm run test:demo`. `npm run test:demo:live` verifica el snapshot y reportes actuales del VPS, seis oficinas, los puestos permanentes, globos por trabajador, sprites coherentes y asientos únicos para misiones con muchos perfiles. Las imágenes consumidas cargan sin respuestas fallidas ni errores de JavaScript. Los textos en vivo son resúmenes de trabajo, no cadenas de razonamiento reales.
