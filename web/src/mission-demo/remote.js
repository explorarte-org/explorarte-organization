import { activityLabels, presenceLabels, roomIds } from './projection.js';
import { departments, stages } from './model.js';

const departmentRoom = {
  empresa: 'direction', investigacion: 'research', ingenieria_ia: 'engineering',
  negocio: 'business', recursos_agenticos: 'skills', servicios: 'services',
};
const roomNames = Object.fromEntries(roomIds.map((id, index) => [id, departments[index]]));
const statusLabels = {
  active: 'Activa', blocked: 'Bloqueada', dead_letter: 'Bloqueada · dead_letter',
  completed: 'Completada', ready: 'Lista', review: 'En revisión', failed: 'Fallida', cancelled: 'Cancelada',
};
const visualByRole = {
  'empresa/ceo': 'ceo',
  'investigacion/research_worker_hourly': 'research',
  'investigacion/revisor_adversarial': 'ev',
  'investigacion/auditor_cerebro_empresa': 'research',
  'ingenieria_ia/arquitecto_software': 'ar',
  'ingenieria_ia/frontend': 'fe',
  'ingenieria_ia/qa': 'qa',
  'negocio/estratega_crecimiento': 'gr',
  'negocio/analista_performance': 'an',
  'recursos_agenticos/disenador_skills': 'sk',
  'recursos_agenticos/evaluador_agentes': 'ev',
  'servicios/operaciones_servicio': 'op',
  'servicios/analista_calidad': 'qa',
};
const visualBySuffix = {
  ciberseguridad: 'ev', 'code-runner': 'op', data_engineer: 'an', ingeniero_ia: 'ar',
  ml_data_scientist: 'an', semantic_engineer: 'sk', orquestador: 'op',
  administrador_financiero: 'an', ingeniero_industrial: 'op', director_negocio: 'gr',
  analista_calidad: 'qa', analista_audiencias: 'an', analista_costos: 'an', analista_kpis: 'an',
  community_manager: 'gr', copywriter: 'fe', disenador: 'sk', editor_contenido_marca: 'fe',
  editor_video: 'fe', estratega_expansion: 'gr', fotografo: 'fe', ilustrador: 'sk',
  investigador_consumidor: 'research', rrpp_alianzas: 'gr', desarrollo_organizacional: 'op',
  disenador_uxui: 'fe', product_manager_portafolio: 'gr', responsable_datos: 'an',
  service_designer: 'sk', soporte_usuario: 'op',
};
const roleNameLabels = {
  arquitecto_software: 'Arquitecto Software',
  ciberseguridad: 'Ciberseguridad',
  data_engineer: 'Data Engineer',
  ingeniero_ia: 'Ingeniero IA',
  ml_data_scientist: 'ML Data Scientist',
  semantic_engineer: 'Semantic Engineer',
  frontend: 'Frontend',
  qa: 'QA',
  administrador_financiero: 'Administrador Financiero',
  ingeniero_industrial: 'Ingeniero Industrial',
  director_negocio: 'Director de Negocio',
  analista_calidad: 'Analista de Calidad',
};

const escapeHTML = value => String(value ?? '').replace(/[&<>"']/g, character => ({
  '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
}[character]));
const short = (value, length = 115) => {
  const text = String(value ?? '').replace(/\s+/g, ' ').trim();
  return escapeHTML(text.length > length ? `${text.slice(0, length - 1)}…` : text);
};
const roleDepartment = roleId => String(roleId || '').split('/')[0] || 'empresa';
const visualForRole = roleId => {
  if (visualByRole[roleId]) return visualByRole[roleId];
  const suffix = String(roleId || '').split('/').at(-1) || '';
  if (visualBySuffix[suffix]) return visualBySuffix[suffix];
  const hash = [...String(roleId || '')].reduce((sum, character) => sum + character.charCodeAt(0), 0);
  return ['ar', 'fe', 'op', 'gr', 'an', 'sk', 'ev', 'qa'][hash % 8];
};
const cleanRoleName = (roleId, name) => {
  if (roleId === 'empresa/ceo') return 'CEO';
  if (roleId === 'investigacion/research_worker_hourly') return 'Investigador';
  if (roleId === 'investigacion/revisor_adversarial') return 'Revisor adversarial';
  const raw = String(name || roleId).replace(/\s*\([^)]*\)\s*$/, '').trim();
  const suffix = raw.includes('/') ? raw.split('/').at(-1) : raw;
  return escapeHTML(roleNameLabels[suffix] || suffix.replace(/_/g, ' ').replace(/\b\w/g, letter => letter.toUpperCase()));
};
const profileCount = count => `${count} ${count === 1 ? 'perfil' : 'perfiles'}`;

function phaseFor(mission, report) {
  const status = report?.status || mission?.status;
  if (status === 'completed') return 3;
  if (status === 'review') return 2;
  const progress = Number(mission?.progress || 0);
  return progress >= 67 ? 2 : progress >= 34 ? 1 : 0;
}

function statusForTask(status, fallback = 'idle') {
  const value = String(status || fallback).toLowerCase();
  if (['blocked', 'dead_letter', 'rejected', 'cancelled', 'failed'].includes(value)) return 'blocked';
  if (['completed', 'succeeded', 'idle', 'available'].includes(value)) return 'available';
  return 'active';
}

function activityFor(status, activity, summary = '') {
  const value = `${status || ''} ${activity || ''} ${summary || ''}`.toLowerCase();
  if (/blocked|dead.?letter|rejected|cancelled|failed|bloquead/.test(value)) return 'blocked';
  if (/completed|succeeded|disponible|en espera|idle/.test(value)) return 'available';
  if (/review|audit|verif|revis|auditor/.test(value)) return 'reviewing';
  if (/research|investig|fuente|buscar/.test(value)) return 'researching';
  if (/discuss|coord|plan|aline/.test(value)) return 'discussing';
  if (/ready|queued|pens|planif/.test(value)) return 'thinking';
  return 'executing';
}

function phaseFromActivity(activity, phase) {
  if (activity === 'thinking' || activity === 'discussing') return Math.min(phase, 1);
  if (activity === 'researching') return 0;
  if (activity === 'reviewing' || activity === 'blocked') return Math.max(phase, 2);
  return phase;
}

function reportResult(report, mission) {
  if (!report || report.status !== 'completed') return null;
  const closure = report.ceoClosure;
  const specialistTitles = (report.specialistAudits || []).map(item => short(item.title || item.roleName, 100)).filter(Boolean);
  const findings = (report.departmentReviews || []).flatMap(item => item.findings || []).map(item => short(item, 135)).filter(Boolean);
  const evidence = (report.specialistAudits || []).map(item => short(item.summary, 135)).filter(Boolean);
  const decisions = (report.keyResolutions || []).map(item => short(item, 135)).filter(Boolean);
  const nextSteps = (closure?.blockedItems || []).concat(closure?.unresolvedDecisions || []).map(item => short(item, 135)).filter(Boolean);
  if (!nextSteps.length) nextSteps.push('Revisar el cierre en el kernel y definir el siguiente ciclo.');
  return {
    headline: short(closure?.status === 'completed' ? `${mission.title} · cierre del VPS` : mission.title, 120),
    summary: short(closure?.answerToOwner || report.objective || mission.title, 320),
    deliverables: specialistTitles.length ? specialistTitles : ['Reporte de misión del kernel'],
    decisions: decisions.length ? decisions : ['El estado de la misión proviene del reporte del VPS.'],
    evidence: evidence.length ? evidence : findings.length ? findings : ['Evidencia registrada en el reporte del VPS.'],
    nextSteps,
    live: true,
  };
}

function activityEntry(agent, phase) {
  return {
    id: `${agent.agentId}:${agent.taskId || 'role'}`,
    agentId: agent.agentId,
    phase: phaseFromActivity(agent.activity, phase),
    stageLabel: `VPS · ${stages[phaseFromActivity(agent.activity, phase)]}`,
    text: agent.summary,
    action: agent.bubble,
    tool: agent.tool,
    output: agent.output,
    nextStep: agent.nextStep,
    activity: agent.activity,
    simulated: false,
  };
}

function eventsFor(mission, report, agents) {
  const events = [];
  if (report?.executivePlan) events.push({ id: `${mission.id}:plan`, agentId: 'ceo', actor: 'CEO', phase: 0, text: 'Plan ejecutivo observado en el VPS', simulated: false });
  for (const agent of agents.filter(item => item.agentId !== 'ceo')) {
    events.push({ id: `${mission.id}:${agent.agentId}`, agentId: agent.agentId, actor: agent.name, phase: phaseFromActivity(agent.activity, mission.phase), text: `${agent.activityLabel}: ${agent.task}`, simulated: false });
  }
  if (report?.ceoClosure) events.push({ id: `${mission.id}:closure`, agentId: 'ceo', actor: 'CEO', phase: 3, text: 'Cierre ejecutivo observado en el VPS', simulated: false });
  return events;
}

function roleRows(raw, mission, report) {
  const roles = new Map();
  for (const department of raw.departments || []) {
    for (const role of department.roles || []) roles.set(role.id, { ...role, department: department.id });
  }
  const audits = new Map((report?.specialistAudits || []).map(item => [item.roleId, item]));
  const selected = new Set(audits.keys());
  for (const [id, role] of roles) {
    if (role.missionId === mission.id || (role.status !== 'idle' && !role.missionId)) selected.add(id);
  }
  const ceoId = roles.has('empresa/ceo') ? 'empresa/ceo' : [...roles.keys()].find(id => id.endsWith('/ceo'));
  const researchId = [...roles.keys()].find(id => id === 'investigacion/research_worker_hourly')
    || [...roles.keys()].find(id => id.startsWith('investigacion/'));
  if (ceoId) selected.add(ceoId);
  if (researchId) selected.add(researchId);

  const ordered = [...selected].sort((a, b) => {
    const rank = id => id === ceoId ? 0 : id === researchId ? 1 : 2;
    return rank(a) - rank(b) || a.localeCompare(b);
  });
  return ordered.map(roleId => {
    const rawRole = roles.get(roleId) || { id: roleId, name: roleId, department: roleDepartment(roleId), status: 'idle', progress: 0, activity: '' };
    const audit = audits.get(roleId);
    const isCEO = roleId === ceoId;
    const isResearch = roleId === researchId;
    const status = audit?.status || (isCEO && report?.status ? report.status : rawRole.status);
    const reportSummary = isCEO && report
      ? report.status === 'completed' ? report.ceoClosure?.answerToOwner
        : ['blocked', 'dead_letter'].includes(report.status) ? 'La planificación ejecutiva quedó detenida; revisar el bloqueo en el reporte del VPS.'
          : report.executivePlan?.objective
      : '';
    const summary = audit?.summary || reportSummary || rawRole.summary || rawRole.activity || `Estado ${status || 'idle'} en el VPS.`;
    const activity = isCEO && report && !audit
      ? report.status === 'completed' ? 'available' : ['blocked', 'dead_letter'].includes(report.status) ? 'blocked' : report.status === 'review' ? 'reviewing' : 'thinking'
      : activityFor(status, audit ? `${audit.title || ''} ${audit.taskClass || ''}` : `${rawRole.activity || ''} ${rawRole.focus || ''}`, summary);
    const room = departmentRoom[rawRole.department] || 'services';
    const progress = audit?.status === 'completed' ? 1 : Number.isFinite(rawRole.progress) && rawRole.progress > 1 ? Math.min(1, rawRole.progress / 100) : Number(mission.progress || 0) / 100;
    const name = isCEO ? 'CEO' : isResearch ? 'Investigador' : cleanRoleName(roleId, audit?.roleName || rawRole.name);
    const task = audit?.title || rawRole.task || (isCEO ? report?.executivePlan?.objective : mission.title);
    const rawBubble = audit?.title || rawRole.bubble || rawRole.focus || rawRole.activity || summary;
    const bubble = isCEO && report?.status === 'completed' ? `Cerrado · ${short(report.ceoClosure?.answerToOwner || 'Resultado general disponible', 82)}` : isCEO && ['blocked', 'dead_letter'].includes(report?.status) ? `Bloqueado · ${short(summary, 82)}` : isCEO && report ? `${activityLabels[activity] || 'Pensando'} · ${short(report.executivePlan?.objective || summary, 82)}` : activity === 'blocked' ? `Bloqueado · ${short(summary, 82)}` : activity === 'available' ? audit ? `Completado · ${short(audit.title || summary, 82)}` : 'Disponible · esperando asignación' : short(rawBubble, 82);
    const presence = statusForTask(status, rawRole.status);
    const reportStatusText = statusLabels[report?.status] || report?.status || '';
    const reportOutput = isCEO && report
      ? `Reporte ${escapeHTML(reportStatusText)} · ${report.departmentReviews?.length || 0} revisiones · ${report.specialistAudits?.length || 0} auditorías`
      : '';
    const planTool = isCEO && report ? 'Organization API · VPS · ExecutivePlan' : '';
    const agent = {
      agentId: isCEO ? 'ceo' : isResearch ? 'research' : roleId,
      visualId: isCEO ? 'ceo' : isResearch ? 'research' : visualForRole(roleId),
      name,
      roleId,
      missionId: mission.id,
      department: room,
      room,
      task: short(task || mission.title, 240),
      presence,
      presenceLabel: presenceLabels[presence] || (presence === 'active' ? 'Activo' : 'Disponible'),
      activity,
      activityLabel: activityLabels[activity] || activity,
      focus: short(rawRole.focus || summary, 180),
      bubble,
      summary: short(summary, 320),
      tool: audit?.taskClass ? `Kernel · ${escapeHTML(audit.taskClass)}` : planTool || (rawRole.tool ? short(rawRole.tool, 120) : 'Organization API · VPS'),
      output: audit?.status ? `Estado de tarea: ${escapeHTML(audit.status)}` : reportOutput || (rawRole.output ? short(rawRole.output, 180) : 'Estado de puesto observado'),
      nextStep: rawRole.nextStep ? short(rawRole.nextStep, 180) : presence === 'blocked' ? 'Revisar el bloqueo en el kernel' : presence === 'available' ? 'Consultar el reporte de misión' : 'Esperar la siguiente actualización del VPS',
      taskId: audit?.taskId ? String(audit.taskId) : isCEO && report?.rootTaskId ? String(report.rootTaskId) : rawRole.taskId ? String(rawRole.taskId) : '',
      assignmentId: audit?.assignmentId ? String(audit.assignmentId) : isCEO && report?.missionId ? String(report.missionId) : rawRole.assignmentId ? String(rawRole.assignmentId) : roleId,
      progress,
      updatedAt: raw.updatedAt,
      simulated: false,
    };
    agent.activityLog = [activityEntry(agent, phaseFor(mission, report))];
    if (audit) agent.evidence = [{
      title: `Reporte VPS · ${short(audit.title || agent.name, 120)}`,
      body: `${short(audit.summary || summary, 460)} Fuente: reporte de misión del VPS.`,
      tool: agent.tool,
      nextStep: agent.nextStep,
      simulated: false,
    }];
    if (isCEO && report?.executivePlan) {
      const planActivity = agent.activity === 'blocked' ? 'blocked' : agent.activity === 'reviewing' ? 'reviewing' : 'thinking';
      agent.evidence = [{
        title: `ExecutivePlan VPS · ${mission.id}`,
        body: `Estado del reporte: ${escapeHTML(reportStatusText)}. Objetivo: ${short(report.executivePlan.objective, 420)} Criterios: ${(report.executivePlan.successCriteria || []).slice(0, 3).map(item => short(item, 180)).join(' ') || 'No se entregaron criterios.'} Fuente: reporte de misión del VPS.`,
        tool: agent.tool,
        nextStep: agent.nextStep,
        simulated: false,
      }];
      agent.activityLog.unshift({ ...activityEntry(agent, phaseFor(mission, report)), id: `${mission.id}:ceo-plan`, phase: 0, stageLabel: 'VPS · Investigar', action: 'Plan ejecutivo observado', text: short(report.executivePlan.objective, 320), output: reportOutput, activity: planActivity });
    }
    return agent;
  });
}

function roomStates(agents) {
  return roomIds.map(id => {
    const people = agents.filter(agent => agent.room === id);
    const blocked = people.filter(agent => agent.presence === 'blocked').length;
    const active = people.filter(agent => agent.presence === 'active').length;
    const dominant = people.find(agent => agent.activity !== 'available') || people[0];
    const status = blocked ? 'blocked' : active ? 'active' : people.length ? 'available' : 'empty';
    const statusLabel = blocked ? `${blocked} ${blocked === 1 ? 'bloqueado' : 'bloqueados'}` : active === 1 ? `${dominant.activityLabel} · 1 perfil` : active > 1 ? `${active} perfiles activos` : people.length ? `${profileCount(people.length)} disponibles` : 'Sin asignaciones';
    const summary = blocked ? `${blocked} ${blocked === 1 ? 'perfil requiere' : 'perfiles requieren'} atención.` : active ? `${profileCount(active)} de ${profileCount(people.length)} están activos según el VPS.` : people.length ? 'El equipo aparece disponible según el último snapshot.' : 'Oficina disponible para una misión futura.';
    return { id, name: roomNames[id], status, statusLabel, summary, activeCount: active, activity: dominant?.activity || 'available', agents: people.map(agent => agent.agentId) };
  });
}

export function normalizeRemoteSnapshot(raw, mission, report) {
  const phase = phaseFor(mission, report);
  const safeMission = {
    id: escapeHTML(mission.id), name: escapeHTML(mission.title), title: escapeHTML(mission.title),
    description: short(report?.executivePlan?.objective || report?.objective || mission.title, 500), status: escapeHTML(report?.status || mission.status),
    department: escapeHTML(mission.department), progress: Number(mission.progress || 0) / 100,
    budgetMicrousd: mission.budgetMicrousd, spentMicrousd: mission.spentMicrousd,
    completedTasks: mission.completedTasks, totalTasks: report?.totalTasks || mission.totalTasks,
    phase, stageLabel: stages[phase], statusLabel: statusLabels[report?.status || mission.status] || escapeHTML(report?.status || mission.status), live: true,
  };
  const agents = roleRows(raw, safeMission, report);
  const missions = (raw.missions || []).map(item => ({
    id: escapeHTML(item.id), name: escapeHTML(item.title), title: escapeHTML(item.title), status: escapeHTML(item.status), statusLabel: statusLabels[item.status] || escapeHTML(item.status),
    phase: phaseFor(item, item.id === mission.id ? report : null), stageLabel: stages[phaseFor(item, item.id === mission.id ? report : null)],
    count: item.id === mission.id ? agents.length : 0, complete: item.status === 'completed', progress: Number(item.progress || 0) / 100,
  }));
  return {
    schemaVersion: 2, version: Date.parse(raw.updatedAt) || Date.now(), cursor: `vps:${raw.updatedAt}`, source: 'vps', simulated: false,
    updatedAt: escapeHTML(raw.updatedAt), organization: escapeHTML(raw.organization?.name || 'VPS'),
    mission: safeMission, missions, rooms: roomStates(agents), agents,
    events: eventsFor(safeMission, report, agents), result: reportResult(report, mission), report: report || null,
    error: '',
  };
}

function emptySnapshot(error = '') {
  return {
    schemaVersion: 2, version: 0, cursor: 'vps:unavailable', source: 'vps', simulated: false,
    updatedAt: '', organization: 'VPS', mission: { id: '', name: 'Sin misión seleccionada', title: '', description: '', status: 'unavailable', progress: 0, phase: 0, stageLabel: stages[0] },
    missions: [], rooms: roomIds.map(id => ({ id, name: roomNames[id], status: 'empty', statusLabel: 'Sin datos', summary: 'No se pudo consultar el VPS.', activeCount: 0, activity: 'available', agents: [] })),
    agents: [], events: [], result: null, report: null, error: escapeHTML(error),
  };
}

export function createRemoteSource({ onChange = () => {}, pollMs = 5000 } = {}) {
  let selectedIndex = 0;
  let current = emptySnapshot('Conectando con el VPS…');
  let timer = null;
  let requestSequence = 0;

  async function request(path) {
    const response = await fetch(path, { cache: 'no-store', headers: { Accept: 'application/json' } });
    const payload = await response.json().catch(() => ({}));
    if (!response.ok) throw new Error(payload.error || `El VPS devolvió HTTP ${response.status}.`);
    return payload;
  }

  async function load(index = selectedIndex) {
    const sequence = ++requestSequence;
    try {
      const raw = await request('/api/organization/snapshot');
      const missions = Array.isArray(raw.missions) ? raw.missions : [];
      selectedIndex = Math.max(0, Math.min(Number(index) || 0, Math.max(0, missions.length - 1)));
      const mission = missions[selectedIndex];
      if (!mission) throw new Error('El snapshot del VPS no contiene misiones.');
      let report = null;
      try { report = await request(`/api/organization/missions/${encodeURIComponent(mission.id)}/report`); } catch (error) { report = { status: mission.status, error: error.message }; }
      if (sequence !== requestSequence) return current;
      current = normalizeRemoteSnapshot(raw, mission, report?.error ? null : report);
      if (report?.error) current.error = escapeHTML(`Snapshot conectado; reporte no disponible: ${report.error}`);
    } catch (error) {
      if (sequence === requestSequence) current = { ...current, error: escapeHTML(error.message || 'No se pudo consultar el VPS.') };
    }
    onChange(current);
    return current;
  }

  return {
    isLive: true,
    snapshot: () => current,
    load,
    refresh: () => load(selectedIndex),
    select: index => { selectedIndex = Number(index) || 0; return load(selectedIndex); },
    next: () => load(selectedIndex),
    reset: () => load(selectedIndex),
    start() { if (!timer) timer = setInterval(() => { void load(selectedIndex); }, pollMs); },
    stop() { clearInterval(timer); timer = null; },
  };
}
