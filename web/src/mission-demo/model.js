export const departments = ['Dirección', 'Investigación', 'Ingeniería IA', 'Negocio', 'Recursos agénticos', 'Servicios'];
export const stages = ['Investigar', 'Construir', 'Validar', 'Completada'];

const job = (activity, bubble, summary, tool, output, nextStep, progress) => ({ activity, bubble, summary, tool, output, nextStep, progress });
const work = (research, build, validate, complete = 'Misión completada') => ({
  0: research, 1: build, 2: validate,
  3: job('available', complete, complete, 'Resultado de misión', 'Resultado general disponible', 'Revisar el informe final', 1),
});
const person = (id, name, department, task, jobs) => ({ id, name, department, task, work: jobs });

const ceo = person('ceo', 'CEO', 0, 'Coordinar prioridades y aprobar entregables', work(
  job('thinking', 'Pensando el plan', 'Ordena prioridades y define qué debe ocurrir primero.', 'Tablero de misión', 'Plan de delegación', 'Delegar responsables', .18),
  job('discussing', 'Discutiendo prioridades', 'Alinea entregables y responsables con cada departamento.', 'Sala de dirección', 'Asignaciones confirmadas', 'Revisar avances', .54),
  job('reviewing', 'Revisando el cierre', 'Contrasta el resultado de cada equipo antes de aprobarlo.', 'Informe de misión', 'Decisión de aprobación', 'Cerrar la misión', .86),
  'Misión aprobada por el CEO',
));
const researcher = person('research', 'Investigador', 1, 'Contrastar fuentes y registrar hallazgos', work(
  job('researching', 'Buscando fuentes', 'Compara literatura y separa fuentes candidatas para la misión.', 'SearchRouter (simulado)', 'Fuentes candidatas', 'Contrastar hallazgos', .22),
  job('discussing', 'Discutiendo hallazgos', 'Comparte coincidencias y dudas con el equipo antes de escribir.', 'Mesa de investigación', 'Matriz de fuentes', 'Verificar citas', .58),
  job('reviewing', 'Verificando citas', 'Revisa que cada afirmación tenga una fuente trazable.', 'Registro de evidencia', 'Citas verificadas', 'Entregar evidencia', .9),
  'Evidencia de investigación revisada',
));

export const missions = [
  {
    id: 'MS-024', name: 'Memoria compartida', description: 'Construir una memoria que conecte el trabajo de los agentes.', stage: 1,
    people: [
      person('ar', 'Arquitecto', 2, 'Definir el contrato de memoria', work(
        job('thinking', 'Pensando el contrato', 'Descompone los límites de la memoria y sus dependencias.', 'Pizarra de arquitectura', 'Borrador de contrato', 'Escribir interfaces', .2),
        job('executing', 'Escribiendo interfaces', 'Convierte las decisiones en un contrato que otros perfiles puedan usar.', 'Editor de código (simulado)', 'Interfaces de memoria', 'Revisar compatibilidad', .62),
        job('reviewing', 'Revisando compatibilidad', 'Comprueba que el contrato soporte las asignaciones de la misión.', 'Checklist técnico', 'Contrato revisado', 'Solicitar aprobación', .88),
      )),
      person('fe', 'Frontend', 2, 'Construir el explorador de recuerdos', work(
        job('thinking', 'Pensando la navegación', 'Define cómo una persona encontrará un recuerdo sin perder contexto.', 'Boceto de interfaz', 'Mapa de navegación', 'Construir el explorador', .16),
        job('executing', 'Construyendo la vista', 'Implementa el explorador y conecta sus estados de carga y vacío.', 'Editor de código (simulado)', 'Explorador funcional', 'Probar recorridos', .6),
        job('reviewing', 'Revisando recorridos', 'Prueba búsquedas y estados para detectar puntos sin salida.', 'Pruebas de interfaz', 'Recorridos verificados', 'Entregar vista', .9),
      )),
      person('op', 'Operaciones', 5, 'Verificar la trazabilidad', work(
        job('thinking', 'Pensando la trazabilidad', 'Define qué eventos deben quedar registrados en cada entrega.', 'Mapa de eventos', 'Lista de eventos', 'Verificar ejecuciones', .2),
        job('executing', 'Verificando eventos', 'Contrasta asignaciones con los eventos emitidos por cada puesto.', 'Registro de eventos (simulado)', 'Trazas de ejecución', 'Revisar evidencias', .58),
        job('reviewing', 'Revisando evidencias', 'Busca huecos entre una decisión y la evidencia que la respalda.', 'Auditoría de trazas', 'Informe de trazabilidad', 'Reportar hallazgos', .9),
      )),
    ],
    result: { headline: 'Memoria compartida lista para revisión', summary: 'El equipo dejó un contrato de memoria, un explorador y una traza de ejemplo listos para la siguiente revisión.', deliverables: ['Contrato de memoria', 'Explorador de recuerdos', 'Informe de trazabilidad'], decisions: ['La evidencia debe conservar su fuente y asignación.', 'La vista muestra el contexto antes del detalle.'], evidence: ['Matriz de fuentes simulada', 'Recorridos de interfaz simulados', 'Trazas de ejemplo'], nextSteps: ['Revisar el contrato con el CEO', 'Conectar la memoria al kernel cuando exista el adaptador'] },
  },
  {
    id: 'MS-023', name: 'Próxima campaña', description: 'Convertir hallazgos de audiencia en una campaña de adquisición.', stage: 0,
    people: [
      person('gr', 'Estratega', 3, 'Definir los segmentos de audiencia', work(
        job('thinking', 'Pensando segmentos', 'Ordena señales de audiencia y propone una primera segmentación.', 'Pizarra de audiencia', 'Hipótesis de segmentos', 'Contrastar hipótesis', .2),
        job('executing', 'Definiendo segmentos', 'Convierte las hipótesis en perfiles comparables para la campaña.', 'Matriz de segmentos (simulada)', 'Segmentos priorizados', 'Revisar supuestos', .58),
        job('reviewing', 'Revisando supuestos', 'Comprueba que cada segmento tenga una señal que lo justifique.', 'Checklist de audiencia', 'Supuestos revisados', 'Entregar estrategia', .88),
      )),
      person('an', 'Analista', 3, 'Contrastar las hipótesis de adquisición', work(
        job('researching', 'Contrastando hipótesis', 'Busca contraejemplos para evitar que la campaña dependa de una sola señal.', 'SearchRouter (simulado)', 'Comparativa de hipótesis', 'Compartir hallazgos', .24),
        job('executing', 'Modelando escenarios', 'Compara escenarios de adquisición con datos ficticios y explícitos.', 'Hoja de escenarios (simulada)', 'Escenarios comparados', 'Revisar riesgos', .62),
        job('reviewing', 'Revisando riesgos', 'Señala qué supuestos necesitan validación antes de publicar.', 'Matriz de riesgos', 'Riesgos priorizados', 'Recomendar siguiente prueba', .9),
      )),
    ],
    result: { headline: 'Estrategia de campaña preparada', summary: 'La misión produjo una segmentación inicial y una comparación de hipótesis para decidir la siguiente prueba.', deliverables: ['Segmentos priorizados', 'Comparativa de hipótesis', 'Matriz de riesgos'], decisions: ['Las hipótesis quedan etiquetadas como simuladas.', 'La siguiente prueba debe validar los supuestos de mayor riesgo.'], evidence: ['Matriz de audiencia simulada', 'Escenarios de adquisición simulados'], nextSteps: ['Validar segmentos con datos reales', 'No publicar la campaña desde esta demo'] },
  },
  {
    id: 'MS-022', name: 'Aprender a investigar', description: 'Convertir un proceso de investigación en una skill reutilizable.', stage: 2,
    people: [
      person('sk', 'Diseñador de skills', 4, 'Documentar el procedimiento', work(
        job('thinking', 'Pensando el procedimiento', 'Divide la investigación en pasos que otro perfil pueda repetir.', 'Lienzo de skill', 'Esqueleto del procedimiento', 'Documentar pasos', .24),
        job('executing', 'Documentando pasos', 'Escribe entradas, salidas y límites para la skill de investigación.', 'Editor de documentación (simulado)', 'Borrador de skill', 'Probar instrucciones', .62),
        job('reviewing', 'Revisando instrucciones', 'Comprueba que cada paso sea observable y evaluable.', 'Checklist de skill', 'Skill revisada', 'Entregar versión candidata', .91),
      )),
      person('ev', 'Evaluador', 4, 'Ejecutar los casos de evaluación', work(
        job('thinking', 'Pensando casos', 'Elige casos que puedan revelar ambigüedades del procedimiento.', 'Matriz de evaluación', 'Casos candidatos', 'Ejecutar casos', .2),
        job('executing', 'Ejecutando casos', 'Pasa casos ficticios por el procedimiento y registra sus salidas.', 'Harness de evaluación (simulado)', 'Salidas comparadas', 'Revisar fallos', .62),
        job('reviewing', 'Revisando fallos', 'Clasifica resultados inesperados y propone ajustes a la skill.', 'Informe de evaluación', 'Fallos clasificados', 'Solicitar cambios', .9),
      )),
      person('qa', 'Calidad', 5, 'Revisar las evidencias', work(
        job('thinking', 'Pensando controles', 'Define qué evidencia mínima hace confiable cada caso.', 'Checklist de calidad', 'Criterios de revisión', 'Auditar evidencias', .2),
        job('executing', 'Auditando evidencias', 'Contrasta casos, instrucciones y resultados del recorrido.', 'Auditoría (simulada)', 'Evidencias auditadas', 'Revisar excepciones', .62),
        job('reviewing', 'Revisando excepciones', 'Separa fallos de la skill de fallos del caso y deja una recomendación.', 'Registro de calidad', 'Recomendación de calidad', 'Cerrar revisión', .9),
      )),
    ],
    result: { headline: 'Skill de investigación evaluada', summary: 'El procedimiento tiene una versión candidata, casos ejecutados y una revisión de calidad con excepciones clasificadas.', deliverables: ['Procedimiento documentado', 'Casos de evaluación', 'Recomendación de calidad'], decisions: ['Los casos y resultados permanecen como ejemplos simulados.', 'Los fallos se clasifican antes de cambiar la skill.'], evidence: ['Borrador de skill', 'Salidas comparadas', 'Registro de excepciones'], nextSteps: ['Resolver excepciones abiertas', 'Publicar la skill solo después de una revisión real'] },
  },
];

export function team(mission) { return [ceo, researcher, ...mission.people]; }
export function advance(stage) { return Math.min(stage + 1, stages.length - 1); }
