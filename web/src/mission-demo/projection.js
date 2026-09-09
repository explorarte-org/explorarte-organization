import { departments, missions, stages, team, advance } from './model.js';

/** @typedef {'offline'|'available'|'active'|'paused'|'blocked'|'meeting'} Presence */
/** @typedef {Object} OfficeAgentState */

export const roomIds = ['direction', 'research', 'engineering', 'business', 'skills', 'services'];
export const activityLabels = {
  thinking: 'Pensando', discussing: 'Discutiendo', researching: 'Investigando',
  executing: 'Ejecutando', reviewing: 'Revisando', blocked: 'Bloqueado', available: 'Disponible',
};
export const presenceLabels = { active: 'Activo', available: 'Disponible', paused: 'Pausado', blocked: 'Bloqueado', meeting: 'En reunión', offline: 'Fuera de horario' };

function activityLogFor(agent, phase) {
  return Object.entries(agent.work).slice(0, phase + 1).map(([stage, step]) => ({
    id: `${agent.id}:${stage}`, agentId: agent.id, phase: Number(stage), stageLabel: stages[Number(stage)],
    text: step.summary, action: step.bubble, tool: step.tool, output: step.output,
    nextStep: step.nextStep, activity: step.activity, simulated: true,
  }));
}

function statusFor(agents) {
  if (!agents.length) return { status: 'empty', statusLabel: 'Sin asignaciones', summary: 'Oficina disponible para una misión futura.', activeCount: 0 };
  const activeCount = agents.filter(agent => agent.presence === 'active').length;
  const dominant = agents.find(agent => agent.activity !== 'available') || agents[0];
  const profileCount = count => `${count} ${count === 1 ? 'perfil' : 'perfiles'}`;
  return {
    status: activeCount ? 'active' : 'available',
    statusLabel: activeCount === 1 ? `${activityLabels[dominant.activity]} · 1 perfil` : activeCount > 1 ? `${activeCount} perfiles activos` : `${profileCount(agents.length)} disponibles`,
    summary: activeCount ? `${profileCount(activeCount)} de ${profileCount(agents.length)} están trabajando en esta misión.` : 'El equipo terminó su trabajo y está disponible para el siguiente ciclo.',
    activeCount,
    activity: dominant.activity,
  };
}

function makeEventHistory(mission, phase) {
  const events = [{ id: `${mission.id}:delegated`, agentId: 'ceo', actor: 'CEO', phase: 0, text: 'Delegó la misión', simulated: true }];
  if (phase >= 1) events.push({ id: `${mission.id}:build`, agentId: 'ceo', actor: 'CEO', phase: 1, text: 'Confirmó las asignaciones', simulated: true });
  if (phase >= 2) events.push({ id: `${mission.id}:review`, agentId: 'research', actor: 'Investigador', phase: 2, text: 'Entregó hallazgos para revisión', simulated: true });
  if (phase >= 3) events.push({ id: `${mission.id}:closed`, agentId: 'ceo', actor: 'CEO', phase: 3, text: 'Aprobó el resultado general', simulated: true });
  return events;
}

// This source imitates an OfficeProjection snapshot. It is the only state owner;
// floor and inspector renderers never invent worker state themselves.
export function createDemoSource() {
  let missionIndex = 0, version = 1;
  const phases = missions.map(mission => mission.stage);
  function snapshot() {
    const mission = missions[missionIndex], phase = phases[missionIndex];
    const agents = team(mission).map(person => {
      const step = person.work[phase] || person.work[0];
      const activityLog = activityLogFor(person, phase);
      return {
        agentId: person.id, name: person.name, roleId: person.id, missionId: mission.id,
        task: person.task, department: roomIds[person.department], room: roomIds[person.department],
        presence: phase === 3 ? 'available' : 'active', presenceLabel: phase === 3 ? 'Disponible' : 'Activo',
        activity: step.activity, activityLabel: activityLabels[step.activity], focus: person.task,
        bubble: step.bubble, summary: step.summary, tool: step.tool, output: step.output, nextStep: step.nextStep,
        taskId: `${mission.id}:${person.id}:task`, assignmentId: `${mission.id}:${person.id}`,
        progress: step.progress, updatedAt: new Date(2026, 8, 7, 9, 12 + phase * 6).toISOString(),
        activityLog, simulated: true,
      };
    });
    const rooms = roomIds.map((id, index) => {
      const roomAgents = agents.filter(agent => agent.room === id);
      return { id, name: departments[index], ...statusFor(roomAgents), agents: roomAgents.map(agent => agent.agentId) };
    });
    return {
      schemaVersion: 2, version, cursor: `demo:${version}`, source: 'simulation',
      mission: { ...mission, phase, stageLabel: stages[phase] },
      missions: missions.map((item, index) => ({ id: item.id, name: item.name, phase: phases[index], count: team(item).length, complete: phases[index] === 3 })),
      rooms, agents, events: makeEventHistory(mission, phase), result: phase === 3 ? mission.result : null,
    };
  }
  return {
    snapshot,
    select(index) { if (Number.isInteger(index) && missions[index]) { missionIndex = index; version++; } },
    next() { const before = phases[missionIndex]; phases[missionIndex] = advance(before); if (before !== phases[missionIndex]) version++; },
    reset() { phases[missionIndex] = 0; version++; },
  };
}

export function evidenceFor(agent, phase) {
  if (agent.evidence?.length) return agent.evidence;
  if (phase < 2) return [];
  const entry = agent.activityLog.find(item => item.phase === phase) || agent.activityLog.at(-1);
  return [{ title: `Borrador · ${agent.output}`, body: `${entry.output}. ${entry.text} Este material es ficticio y no procede de un agente ni del VPS.`, tool: entry.tool, nextStep: entry.nextStep }];
}
