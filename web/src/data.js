const role = (
  id,
  name,
  initials,
  status,
  activity,
  progress,
  missionId = "MS-024",
) => ({ id, name, initials, status, activity, progress, missionId });
export const demoSnapshot = {
  organization: { id: "explorarte", name: "Psi.Explorarte" },
  updatedAt: "2026-09-06T18:00:00.000Z",
  metrics: {
    objectives: { completed: 8, total: 12 },
    missions: { active: 3, completed: 18 },
    learning: { episodes: 142, consolidated: 96 },
    skills: { created: 24, learned: 38 },
    memories: { episodic: 218, semantic: 84, corrective: 12 },
    cost: {
      actualMicrousd: 27420000,
      budgetMicrousd: 100000000,
      estimatedMicrousd: 42600000,
    },
  },
  departments: [
    {
      id: "empresa",
      name: "Empresa",
      subtitle: "Dirección y estrategia",
      color: "#8b7cf6",
      roles: [
        role(
          "empresa/ceo",
          "CEO",
          "CE",
          "working",
          "Coordinando prioridades de la campaña",
          72,
        ),
      ],
    },
    {
      id: "ingenieria_ia",
      name: "Ingeniería IA",
      subtitle: "Construcción e inteligencia",
      color: "#6b9df5",
      roles: [
        role(
          "ingenieria_ia/arquitecto_software",
          "Arquitecto de Software (Ingeniería de IA)",
          "AR",
          "working",
          "Diseñando el flujo de agentes",
          68,
        ),
        role(
          "ingenieria_ia/frontend",
          "Frontend (Ingeniería de IA)",
          "DE",
          "working",
          "Implementando memoria compartida",
          42,
        ),
      ],
    },
    {
      id: "negocio",
      name: "Negocio",
      subtitle: "Mercado y crecimiento",
      color: "#e6ae65",
      roles: [
        role(
          "negocio/estratega_crecimiento",
          "Estratega de Crecimiento (Marketing)",
          "GR",
          "working",
          "Analizando oportunidades del mercado",
          81,
          "MS-023",
        ),
        role(
          "negocio/analista_performance",
          "Analista de Performance (Marketing)",
          "AN",
          "reviewing",
          "Validando hipótesis de adquisición",
          93,
          "MS-023",
        ),
      ],
    },
    {
      id: "recursos_agenticos",
      name: "Recursos agénticos",
      subtitle: "Talento y capacidades",
      color: "#d08ccd",
      roles: [
        role(
          "recursos_agenticos/disenador_skills",
          "Diseñador de Skills",
          "SC",
          "working",
          "Consolidando una nueva skill de research",
          54,
          "MS-022",
        ),
        role(
          "recursos_agenticos/evaluador_agentes",
          "Evaluador de Agentes y Skills",
          "ME",
          "idle",
          "Disponible para el próximo aprendizaje",
          0,
          "MS-022",
        ),
      ],
    },
    {
      id: "servicios",
      name: "Servicios",
      subtitle: "Operaciones y soporte",
      color: "#69bfa6",
      roles: [
        role(
          "servicios/operaciones_servicio",
          "Operaciones de Servicio (Servicios)",
          "OP",
          "working",
          "Monitorizando calidad de ejecución",
          76,
        ),
        role(
          "servicios/soporte_usuario",
          "Soporte al Usuario (Servicios)",
          "SO",
          "blocked",
          "Esperando criterios de aceptación",
          25,
        ),
      ],
    },
    {
      id: "investigacion",
      name: "Investigación",
      subtitle: "Auditoría independiente",
      color: "#74bbc9",
      roles: [
        role(
          "investigacion/research_worker_hourly",
          "Worker horario de Investigación",
          "RE",
          "working",
          "Contrastando fuentes de conocimiento",
          64,
          "MS-022",
        ),
        role(
          "investigacion/revisor_adversarial",
          "Revisor adversarial independiente (Investigacion)",
          "EV",
          "reviewing",
          "Comparando resultados del experimento",
          88,
          "MS-022",
        ),
      ],
    },
  ],
  missions: [
    {
      id: "MS-024",
      title: "Lanzar el nuevo sistema de agentes",
      status: "active",
      department: "ingenieria_ia",
      progress: 68,
      budgetMicrousd: 15000000,
      spentMicrousd: 8420000,
      completedTasks: 8,
      totalTasks: 12,
    },
    {
      id: "MS-023",
      title: "Explorar oportunidades de crecimiento",
      status: "active",
      department: "negocio",
      progress: 81,
      budgetMicrousd: 10000000,
      spentMicrousd: 6200000,
      completedTasks: 7,
      totalTasks: 9,
    },
    {
      id: "MS-022",
      title: "Consolidar el conocimiento organizacional",
      status: "review",
      department: "investigacion",
      progress: 92,
      budgetMicrousd: 20000000,
      spentMicrousd: 12800000,
      completedTasks: 11,
      totalTasks: 12,
    },
  ],
  learning: [
    {
      id: "l1",
      type: "skill",
      title: "Research con validación de fuentes",
      department: "recursos_agenticos",
      status: "Creada",
      time: "Hace 2 min",
    },
    {
      id: "l2",
      type: "memory",
      title: "Priorizar entregables antes de expandir alcance",
      department: "empresa",
      status: "Consolidada",
      time: "Hace 8 min",
    },
    {
      id: "l3",
      type: "episode",
      title: "Evaluación del flujo multiagente",
      department: "investigacion",
      status: "Aprendido",
      time: "Hace 12 min",
    },
  ],
  activity: [
    {
      id: "a1",
      role: "Frontend (Ingeniería de IA)",
      text: "Comenzó a implementar la memoria compartida",
      time: "Ahora",
    },
    {
      id: "a2",
      role: "Diseñador de Skills",
      text: "Creó una skill de validación de fuentes",
      time: "Hace 2 min",
    },
    {
      id: "a3",
      role: "CEO",
      text: "Actualizó las prioridades de la campaña",
      time: "Hace 5 min",
    },
  ],
  spend: [
    { label: "Lun", microusd: 3200000 },
    { label: "Mar", microusd: 4100000 },
    { label: "Mié", microusd: 2850000 },
    { label: "Jue", microusd: 5400000 },
    { label: "Vie", microusd: 4670000 },
    { label: "Sáb", microusd: 3900000 },
    { label: "Dom", microusd: 3300000 },
  ],
  capabilities: { chat: true, createMission: true },
};

export function getDemoMissionReport(id, state) {
  const m = state?.missions?.find((x) => x.id === id) || {
    id,
    title: "Auditoría exhaustiva de la organización",
    status: "completed",
    budgetMicrousd: 15000000,
    spentMicrousd: 648000,
    progress: 100,
  };
  return {
    missionId: m.id,
    rootTaskId: 95,
    title: m.title,
    objective: m.title,
    status: m.status,
    createdAt: new Date(Date.now() - 3600000).toISOString(),
    completedAt: m.status === "completed" ? new Date().toISOString() : null,
    duration: "35m 59s",
    budgetMicrousd: m.budgetMicrousd,
    spentMicrousd: m.spentMicrousd,
    totalTokens: 1873635,
    inputTokens: 1861375,
    outputTokens: 12260,
    totalTasks: 23,
    completedTasks: m.status === "completed" ? 23 : 4,
    ceoClosure: {
      status: "completed",
      answerToOwner: "Cierre ejecutivo completado y aceptado. Las cuatro revisiones departamentales están verificadas y cubren conexiones, flujos, dependencias, responsabilidades, riesgos y vacíos de evidencia. No se registran cambios requeridos ni decisiones del owner indispensables para este objetivo.",
      completedItems: [
        "Revisión de ingeniería_ia completada: verificación de límites arquitectónicos, políticas de seguridad, pipelines de datos, experimentos de ML, recuperación semántica, QA y accesibilidad sin conflictos.",
        "Revisión de negocio completada: consolidación de finanzas, flujos de procesos y sostenibilidad presupuestaria dentro de los límites de autoridad.",
        "Revisión de recursos_agénticos completada: coherencia de perfiles y aislamiento estricto de roles no importados conforme a la directiva canónica D-006.",
        "Revisión de servicios completada: métricas de portafolio y trazabilidad de operaciones validadas sin procesamiento de datos clínicos.",
        "La auditoría consolidada queda lista para continuar bajo la gobernanza, presupuesto y límites de datos vigentes."
      ],
      blockedItems: [],
      unresolvedDecisions: [],
    },
    executivePlan: {
      objective: m.title,
      successCriteria: [
        "Las cuatro unidades operativas entregan sus paquetes de auditoría.",
        "Comparación verificable del estado observado contra el canónico.",
        "Trazabilidad de cada flujo y condición de recuperación sin violar límites de seguridad."
      ],
      globalConstraints: [
        "Respetar fronteras inmutables de privacidad y seguridad.",
        "No ejecutar cambios en el código o repositorio no autorizados.",
        "Ajustar ejecución estrictamente a los techos presupuestarios."
      ],
    },
    departmentReviews: [
      {
        taskId: 195,
        department: "ingenieria_ia",
        roleId: "ingenieria_ia/orquestador",
        verdict: "accept",
        findings: [
          "Las 8 tareas departamentales de ingeniería (101 a 106, 113 y 114) completaron sus entregables verificados.",
          "Se detectó límite inicial de tokens en la tarea 104 y fue resuelto elevando el techo a 5.000.000 tokens.",
          "Sin vulnerabilidades críticas de seguridad ni conflictos de dependencias."
        ]
      },
      {
        taskId: 212,
        department: "recursos_agenticos",
        roleId: "recursos_agenticos/desarrollo_organizacional",
        verdict: "accept",
        findings: [
          "5 roles de workers permanecen inactivos hasta que el owner apruebe D-006 y sus perfiles existan en el repositorio.",
          "El líder canónico no delega tareas a roles inactivos, preservando la coherencia del registro.",
          "Aislamiento verificado sin asignaciones productivas ni consumo espurio."
        ]
      },
      {
        taskId: 507,
        department: "negocio",
        roleId: "negocio/director_negocio",
        verdict: "accept",
        findings: [
          "Consolidación exitosa de finanzas, comunicaciones y marketing/crecimiento.",
          "Sostenibilidad presupuestaria verificada sin redefinir roles globales.",
          "Trazabilidad completa de flujos operativos sin fricción transversal."
        ]
      },
      {
        taskId: 543,
        department: "servicios",
        roleId: "servicios/product_manager_portafolio",
        verdict: "accept",
        findings: [
          "Evaluadas métricas de portafolio y flujos operativos del servicio.",
          "Cero procesamiento de datos clínicos o sensibles.",
          "Criterios de calidad y continuidad operativa satisfechos."
        ]
      }
    ],
    specialistAudits: [
      { taskId: 101, department: "ingenieria_ia", roleId: "ingenieria_ia/arquitecto_software", roleName: "Arquitecto Software", title: "Audit Software Architecture Boundaries", taskClass: "engineering.review", status: "completed", summary: "Límites arquitectónicos y aislamiento estructural documentados y verificados." },
      { taskId: 102, department: "ingenieria_ia", roleId: "ingenieria_ia/ciberseguridad", roleName: "Ciberseguridad", title: "Audit Cybersecurity Posture and Secrets", taskClass: "security.audit", status: "completed", summary: "Políticas de seguridad y secretos verificados bajo el contexto de gobernanza organizacional." },
      { taskId: 103, department: "ingenieria_ia", roleId: "ingenieria_ia/data_engineer", roleName: "Data Engineer", title: "Audit Data Pipelines and RAG Ingestion", taskClass: "data.audit", status: "completed", summary: "Pipelines de ingesta y preprocesamiento de RAG auditados y verificados." },
      { taskId: 104, department: "ingenieria_ia", roleId: "ingenieria_ia/ingeniero_ia", roleName: "Ingeniero IA", title: "Audit AI Model Consumption and Routing", taskClass: "engineering.review", status: "completed", summary: "Consumo de modelos y enrutamiento verificados; desbloqueado y completado con el nuevo techo de 5M tokens." },
      { taskId: 105, department: "ingenieria_ia", roleId: "ingenieria_ia/ml_data_scientist", roleName: "ML Data Scientist", title: "Audit ML Embeddings and Data Quality", taskClass: "ml.audit", status: "completed", summary: "Embeddings vectoriales y experimentos de ML validados dentro de los límites operativos." },
      { taskId: 106, department: "ingenieria_ia", roleId: "ingenieria_ia/qa", roleName: "QA", title: "Audit QA and Testing Infrastructure", taskClass: "qa.audit", status: "completed", summary: "Toolchain de verificación automatizada validado." },
      { taskId: 113, department: "ingenieria_ia", roleId: "ingenieria_ia/semantic_engineer", roleName: "Ingeniero Semántico", title: "Audit Semantic Layer and Retrieval Tech", taskClass: "semantic.audit", status: "completed", summary: "Estrategia de recuperación híbrida y grafo de conocimiento alineados sin sobrecargar Go binary." },
      { taskId: 114, department: "ingenieria_ia", roleId: "ingenieria_ia/frontend", roleName: "Frontend", title: "Audit Frontend Interfaces and Accessibility", taskClass: "frontend.audit", status: "completed", summary: "Accesibilidad e interfaces del frontend auditadas bajo parámetros de calidad." },
      { taskId: 229, department: "negocio", roleId: "negocio/administrador_financiero", roleName: "Administrador Financiero", title: "Auditoría Financiera y Presupuestaria", taskClass: "negocio.audith_financial", status: "completed", summary: "Sostenibilidad financiera y asignación de presupuestos validada conforme a límites de rol." },
      { taskId: 230, department: "negocio", roleId: "negocio/ingeniero_industrial", roleName: "Ingeniero Industrial", title: "Mapeo Operativo y de Procesos", taskClass: "negocio.operational_mapping", status: "completed", summary: "Flujos de procesos internos consolidados facilitando trazabilidad sin privilegios cruzados." },
      { taskId: 231, department: "negocio", roleId: "negocio/director_negocio", roleName: "Director de Negocio", title: "Consolidación del Mapa Operativo y de Gobernanza", taskClass: "negocio.governance_mapping", status: "completed", summary: "Mapa operativo y de gobernanza sintetizado conectando objetivos y responsabilidades." },
      { taskId: 530, department: "servicios", roleId: "servicios/analista_calidad", roleName: "Analista de Calidad", title: "Audit Service Portfolio and Flows", taskClass: "servicios.audit", status: "completed", summary: "Evaluación integral de métricas de servicio, flujos operativos e historial de handoffs completada." }
    ],
    keyResolutions: [
      "Todas las 4 revisiones departamentales concluyeron con veredicto 'accept' sin bloqueos.",
      "Se verificó la política D-006: los perfiles sin archivo en el árbol permanecen inactivos y aislados sin generar consumo no autorizado.",
      "Se auditó el consumo de modelos IA y enrutamiento con ajuste de límite de tokens (5M) para garantizar cobertura total.",
      "Gobernanza y finanzas consolidadas con trazabilidad completa de flujos operativos.",
      "Portafolio de servicios: flujos verificados conforme a las restricciones operativas sin procesamiento de datos clínicos."
    ]
  };
}
