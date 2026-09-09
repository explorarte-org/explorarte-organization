# Explorarte · demo local de organización

Esta demo permite observar una misión, sus departamentos y los perfiles asignados en una oficina pixel art. El usuario aprobó la opción A, Organización viva, y su maqueta: piso conectado de seis oficinas, panel contextual a la derecha y controles de simulación arriba.

CEO e Investigación permanecen visibles. Los demás perfiles se muestran según la misión. Las oficinas mantienen su posición. En la fuente local los datos, eventos y evidencias son ficticios; en la fuente VPS se leen del snapshot y del reporte de la misión, con etiquetas explícitas de observación. La demo no ejecuta agentes ni escribe en el VPS.

El contrato OfficeProjection debe ser independiente de la presentación. `remote.js` traduce el snapshot y los reportes del Organization Kernel al mismo contrato que usa la simulación. Agent Office es el destino visual previsto, no un segundo orquestador; esta demo todavía no incorpora su runtime.

La UI real en index.html y src/app.js queda fuera de este cambio.
