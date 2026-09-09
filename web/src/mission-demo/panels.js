import { activityLabels, evidenceFor } from './projection.js';
import { stages } from './model.js';
import { spriteIndexFor } from './floor.js';

const escapeHTML = value => String(value ?? '').replace(/[&<>"']/g, character => ({
  '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
}[character]));
const list = values => `<ul class="result-list">${values.map(value => `<li>${value}</li>`).join('')}</ul>`;

export function ceoChatMarkup({ messages = [], live = false, busy = false, draft = '', composer = null, error = '' } = {}) {
  return `<section class="ceo-chat" aria-label="Chat con el CEO"><div class="ceo-chat-head"><div><span class="result-kicker">Dirección ejecutiva</span><h2>Habla con el CEO</h2><p><span class="ceo-chat-dot"></span>${live ? 'API del VPS · conectado' : 'CEO simulado · demo local'}</p></div><span class="ceo-chat-mark">CE</span></div><div class="ceo-chat-messages" role="log" aria-live="polite">${messages.map(message => `<article class="ceo-chat-message ${message.role === 'user' ? 'user' : 'ceo'}">${message.role === 'ceo' ? '<small>CEO</small>' : '<small>Tú</small>'}<p>${escapeHTML(message.text)}</p></article>`).join('')}${busy ? '<article class="ceo-chat-message ceo"><small>CEO</small><p>Preparando respuesta…</p></article>' : ''}</div><div class="ceo-chat-actions"><button type="button" data-chat-prompt="Dame un resumen del estado de la organización">Resumen</button><button type="button" data-chat-prompt="/mision ">/mision</button></div><form id="ceo-chat-form"><label class="sr-only" for="ceo-chat-input">Mensaje para el CEO</label><textarea id="ceo-chat-input" rows="2" maxlength="8000" placeholder="Escribe al CEO o usa /mision objetivo…" ${busy ? 'disabled' : ''}>${escapeHTML(draft)}</textarea><div><span>Enter para enviar · Shift+Enter para nueva línea</span><button class="ceo-chat-send" type="submit" aria-label="Enviar mensaje" ${busy ? 'disabled' : ''}>↑</button></div></form>${composer ? `<section class="mission-composer" aria-label="Confirmar misión"><div><span class="result-kicker">Crear misión</span><h3>Confirma la directriz del CEO</h3></div><form id="mission-composer-form"><label>Objetivo<textarea id="mission-objective" rows="3" maxlength="2000">${escapeHTML(composer.objective)}</textarea></label><label>Presupuesto máximo · USD<input id="mission-budget" inputmode="decimal" value="${escapeHTML(composer.budget)}"></label>${error ? `<p class="ceo-chat-error" role="alert">${escapeHTML(error)}</p>` : ''}<div class="mission-composer-actions"><button type="button" data-chat-cancel>Cancelar</button><button class="primary" type="submit" ${busy ? 'disabled' : ''}>Confirmar e iniciar</button></div></form></section>` : error ? `<p class="ceo-chat-error" role="alert">${escapeHTML(error)}</p>` : ''}<p class="ceo-chat-note">${live ? 'Las respuestas y misiones pasan por la API del CEO. La oficina se actualiza después de crear una misión.' : 'Simulación local: no ejecuta agentes ni genera gasto real.'}</p></section>`;
}

export function missionResultMarkup(snapshot) {
  if (!snapshot.result) return '';
  const result = snapshot.result;
  const live = result.live === true;
  return `<section class="mission-result" aria-label="Resultado general de la misión"><div class="result-kicker">${live ? 'Resultado observado · VPS' : 'Resultado simulado · misión completada'}</div><h2>${result.headline}</h2><p>${result.summary}</p><div class="result-grid"><section><h3>Entregables</h3>${list(result.deliverables)}</section><section><h3>Decisiones</h3>${list(result.decisions)}</section><section><h3>Evidencia</h3>${list(result.evidence)}</section><section><h3>Siguiente ciclo</h3>${list(result.nextSteps)}</section></div><p class="demo-note">${live ? 'Cierre leído del reporte del VPS. No contiene cadenas de razonamiento interno.' : 'Este cierre es una plantilla de ejemplo. No representa una ejecución real ni una aprobación del VPS.'}</p></section>`;
}

function activityTimeline(agent) {
  return `<ol class="activity-timeline">${agent.activityLog.slice().reverse().map(entry => `<li class="activity-entry"><div class="activity-entry-head"><span class="activity-dot activity-${entry.activity}"></span><strong>${activityLabels[entry.activity]}</strong><small>${entry.stageLabel}</small></div><p>${entry.action}</p><span class="activity-summary">${entry.text}</span><dl><div><dt>Herramienta</dt><dd>${entry.tool}</dd></div><div><dt>Salida</dt><dd>${entry.output}</dd></div><div><dt>Siguiente</dt><dd>${entry.nextStep}</dd></div></dl><small class="simulated-tag">${entry.simulated ? 'Demo · simulado' : 'VPS · observado'}</small></li>`).join('')}</ol>`;
}

function eventList(snapshot, count = 20) {
  return `<ol class="events">${snapshot.events.slice(-count).reverse().map(event => `<li><p><strong>${event.actor}</strong> ${event.text}</p><small>${stages[event.phase] || 'VPS'} <span>${event.simulated ? 'Demo · simulado' : 'VPS · observado'}</span></small></li>`).join('')}</ol>`;
}

function statusTextFor(agent) {
  return agent.presenceLabel === agent.activityLabel ? agent.presenceLabel : `${agent.presenceLabel} · ${agent.activityLabel}`;
}

function assignmentMarkup(snapshot, agent) {
  return `<dl class="assignment"><div><dt>Tarea actual</dt><dd class="task">${agent.task}</dd></div><div><dt>Misión</dt><dd>${snapshot.mission.name}</dd></div>${agent.taskId ? `<div><dt>Task ID</dt><dd>${agent.taskId}</dd></div>` : ''}</dl>`;
}

function workCardMarkup(agent) {
  return `<section class="agent-work-card"><div class="work-card-head"><span>${agent.simulated ? 'Actividad simulada' : 'Actividad observada · VPS'}</span><strong>${agent.activityLabel}</strong></div><p class="work-action">${agent.bubble}</p><p>${agent.summary}</p><dl><div><dt>Herramienta</dt><dd>${agent.tool}</dd></div><div><dt>Salida</dt><dd>${agent.output}</dd></div><div><dt>Próximo paso</dt><dd>${agent.nextStep}</dd></div></dl><div class="agent-progress"><span style="--progress:${agent.progress}"></span></div></section>`;
}

function personContextMarkup(agent, room) {
  return `<div class="person-title"><canvas class="portrait" width="80" height="88" data-portrait="${spriteIndexFor(agent.visualId || agent.agentId)}" aria-hidden="true"></canvas><div><h2 id="person-details" tabindex="-1">${agent.name}</h2><p>${room.name}</p></div></div><p class="agent-status"><span aria-hidden="true"></span>${statusTextFor(agent)}</p><button class="return-office" data-action="return">Volver al puesto</button>`;
}

function roomContextMarkup(snapshot, roomId) {
  const room = snapshot.rooms.find(item => item.id === roomId) || snapshot.rooms[0];
  const people = snapshot.agents.filter(item => item.room === room.id);
  return `<h2 id="person-details" tabindex="-1">${room.name}</h2><p class="muted">Oficina del departamento</p><p class="department-status"><span class="status-dot status-${room.status}"></span>${room.statusLabel}</p><p class="room-summary">${room.summary}</p><button class="return-office" data-action="return">Volver a la oficina</button><h3>En esta misión</h3>${people.length ? `<ul class="department-people">${people.map(item => `<li><button data-person="${item.agentId}"><strong>${item.name}</strong><span>${item.activityLabel} · ${item.bubble}</span></button></li>`).join('')}</ul>` : '<div class="empty-state"><h3>Sin asignaciones</h3><p>Este departamento no participa en la misión seleccionada. Su oficina permanece disponible.</p></div>'}<p class="demo-note">CEO e Investigación siempre están presentes en el piso.</p>`;
}

function detailMarkup(snapshot, agent, tab, evidenceOpen) {
  const phase = snapshot.mission.phase;
  const evidence = evidenceFor(agent, phase);
  let body;
  if (tab === 'task') {
    body = `<h3>Objetivo</h3><p>${snapshot.mission.description}</p><h3>Recorrido de la misión</h3><ol class="steps">${stages.slice(0, 3).map((stage, i) => `<li class="${phase > i ? 'done' : phase === i ? 'active' : ''}"><span class="step-index">${i + 1}</span><span>${stage}</span><small>${phase > i ? 'Completado' : phase === i ? 'En curso' : 'Pendiente'}</small></li>`).join('')}</ol>`;
  } else if (tab === 'evidence') {
    body = evidence.length ? `<h3>Evidencia del puesto</h3><p class="muted">${agent.simulated ? 'Contenido ficticio vinculado a la actividad seleccionada.' : 'Contenido observado en el reporte de misión del VPS.'}</p><button class="evidence-button" data-action="evidence" aria-expanded="${evidenceOpen}" aria-controls="evidence-body">${evidenceOpen ? 'Cerrar borrador' : 'Abrir borrador'}</button><div id="evidence-body" ${evidenceOpen ? '' : 'hidden'}><h4>${evidence[0].title}</h4><p>${evidence[0].body}</p><dl class="evidence-meta"><div><dt>Herramienta</dt><dd>${evidence[0].tool}</dd></div><div><dt>Siguiente paso</dt><dd>${evidence[0].nextStep}</dd></div></dl></div>` : '<div class="empty-state"><h3>Aún no hay evidencia</h3><p>Avanza hasta Validar para consultar un borrador ficticio. No se han generado archivos reales.</p></div>';
  } else if (tab === 'activity') {
    body = `<h3>Actividad de ${agent.name}</h3><p class="muted">Registro resumido de este puesto; no es pensamiento interno del modelo.</p>${activityTimeline(agent)}`;
  } else {
    body = snapshot.result ? `<h3>${snapshot.result.headline}</h3><p>${snapshot.result.summary}</p><div class="result-inline"><h4>Entregables</h4>${list(snapshot.result.deliverables)}<h4>Siguiente ciclo</h4>${list(snapshot.result.nextSteps)}</div>` : '<div class="empty-state"><h3>Resultado pendiente</h3><p>El cierre enriquecido aparecerá cuando termine la misión.</p></div>';
  }
  const events = tab === 'activity' || tab === 'result' ? '' : `<section class="recent-events"><h3>Eventos de la misión</h3>${eventList(snapshot, 3)}</section>`;
  return `<section id="detail-panel" role="tabpanel" aria-labelledby="tab-${tab}" tabindex="0">${body}</section>${events}<p class="demo-note">${snapshot.source === 'vps' ? 'Estados y actividades sincronizados desde el VPS; los textos son resúmenes de tareas, no pensamiento interno.' : 'Todos los estados, actividades y eventos son simulados.'}</p>`;
}

function statusMarkup(snapshot, agent) {
  if (!agent) return '<section class="sidebar-status-empty"><h3>Estado del CEO</h3><p>Selecciona Estado CEO para consultar la dirección ejecutiva.</p></section>';
  return `<section class="sidebar-status-panel" aria-label="Estado del puesto"><div class="sidebar-section-heading"><span class="result-kicker">Estado actual</span><h3>${statusTextFor(agent)}</h3></div>${assignmentMarkup(snapshot, agent)}${workCardMarkup(agent)}<section class="recent-events"><h3>Eventos recientes</h3>${eventList(snapshot, 3)}</section></section><p class="demo-note">${snapshot.source === 'vps' ? 'Estado observado en el VPS; el resumen no expone pensamiento interno.' : 'Estado simulado para recorrer la demo.'}</p>`;
}

export function inspectorMarkup(snapshot, selected, roomId, tab = 'status', evidenceOpen = false) {
  const agent = snapshot.agents.find(item => item.agentId === selected);
  const room = snapshot.rooms.find(item => item.id === (agent?.room || roomId)) || snapshot.rooms[0];
  return agent ? `${personContextMarkup(agent, room)}${tab === 'status' ? statusMarkup(snapshot, agent) : detailMarkup(snapshot, agent, tab, evidenceOpen)}` : roomContextMarkup(snapshot, room.id);
}

export function sidebarMarkup(snapshot, selected, roomId, tab = 'status', evidenceOpen = false, chat = {}) {
  const phase = snapshot.mission.phase;
  const tabs = [['chat', 'Chat'], ['status', 'Estado CEO'], ['task', 'Tarea'], ['evidence', 'Evidencia'], ['activity', 'Actividad'], ...(phase === 3 ? [['result', 'Resultado']] : [])];
  const activeTab = tabs.some(([id]) => id === tab) ? tab : 'status';
  const agent = snapshot.agents.find(item => item.agentId === selected);
  const room = snapshot.rooms.find(item => item.id === (agent?.room || roomId)) || snapshot.rooms[0];
  const context = activeTab === 'chat' ? '' : agent ? personContextMarkup(agent, room) : roomContextMarkup(snapshot, room.id);
  const content = activeTab === 'chat' ? ceoChatMarkup(chat) : agent ? (activeTab === 'status' ? statusMarkup(snapshot, agent) : detailMarkup(snapshot, agent, activeTab, evidenceOpen)) : '<section class="sidebar-status-empty"><h3>Selecciona un puesto</h3><p>Elige un perfil en la oficina para consultar su tarea, evidencia o actividad.</p></section>';
  return `<nav class="sidebar-tabs" role="tablist" aria-label="Información de la misión">${tabs.map(([id, label]) => `<button id="tab-${id}" role="tab" data-tab="${id}" aria-selected="${activeTab === id}" aria-controls="sidebar-panel" tabindex="${activeTab === id ? 0 : -1}">${label}</button>`).join('')}</nav><div id="sidebar-panel" class="sidebar-panel" role="tabpanel" aria-labelledby="tab-${activeTab}">${context}${content}</div>`;
}

export function missionsMarkup(snapshot) {
  return `<section class="missions-view"><h1>Misiones</h1><p>Elige un recorrido para observar cómo se ocupa el piso y consultar el trabajo de cada puesto.</p><div class="mission-list">${snapshot.missions.map((mission, index) => `<button class="mission-card" data-mission="${index}"><span>${mission.id} · ${stages[mission.phase]}</span><strong>${mission.name}</strong><span>${mission.count} perfiles asignados</span><span class="mission-link">Abrir oficina</span></button>`).join('')}</div></section>`;
}
