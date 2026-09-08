import { createDemoSource } from './projection.js';
import { createRemoteSource } from './remote.js';
import { floorMarkup, loadFloorArt, paintSprites } from './floor.js';
import { createClient, parseMissionCommand, validateBudgetMicrousd } from '../api.js';
import { missionResultMarkup, missionsMarkup, sidebarMarkup } from './panels.js';

const root = document.querySelector('#demo');
const liveMode = new URLSearchParams(location.search).get('source') === 'vps';
let source;
const defaultAgent = () => 'ceo';
let selected = defaultAgent(), selectedRoom = 'direction', tab = 'status', running = false;
let timer = null, zoom = 1, view = 'office', evidenceOpen = false;
let chatDraft = '', chatBusy = false, chatError = '', missionComposer = null;
const ceoMessages = [{ role: 'ceo', text: liveMode ? 'Estoy conectado al kernel del VPS. Puedes pedirme un resumen o escribir /mision objetivo para preparar una nueva misión.' : 'Soy el CEO simulado de esta demo. Puedes pedirme un resumen o escribir /mision objetivo para probar el flujo local.' }];
const chatClient = createClient({ mode: liveMode ? 'live' : 'demo' });
const mobile = () => matchMedia('(max-width: 900px)').matches;

source = liveMode
  ? createRemoteSource({ onChange: () => { if (!root.querySelector('.loading')) render(); } })
  : createDemoSource();

function stopPlayback() { clearInterval(timer); timer = null; running = false; }
function stop() { stopPlayback(); source.stop?.(); }
function focusKey() {
  const el = document.activeElement;
  if (!el || !root.contains(el)) return null;
  if (el.id) return `#${el.id}`;
  for (const name of ['action', 'person', 'room', 'tab', 'mission', 'view']) {
    if (el.dataset[name] !== undefined) return `[data-${name}="${el.dataset[name]}"]`;
  }
  return null;
}
function focusState() {
  const el = document.activeElement;
  const selector = focusKey();
  if (!el || !selector) return null;
  return {
    selector,
    start: typeof el.selectionStart === 'number' ? el.selectionStart : null,
    end: typeof el.selectionEnd === 'number' ? el.selectionEnd : null,
    scrollTop: typeof el.scrollTop === 'number' ? el.scrollTop : null,
  };
}
function updatedLabel(snapshot) {
  if (!snapshot.updatedAt) return 'sin actualización';
  const date = new Date(snapshot.updatedAt);
  return Number.isNaN(date.valueOf()) ? 'sin actualización' : date.toLocaleTimeString('es-CL', { hour: '2-digit', minute: '2-digit', second: '2-digit' });
}
function render({ focus = null } = {}) {
  const previousFocus = focusState();
  const snapshot = source.snapshot();
  const phase = snapshot.mission?.phase ?? 0;
  if (phase !== 3 && tab === 'result') tab = 'status';
  const scroll = root.querySelector('.floor-viewport');
  const scrollPosition = scroll ? [scroll.scrollLeft, scroll.scrollTop] : [0, 0];
  const live = source.isLive === true;
  const statusLabel = live
    ? (snapshot.error ? `VPS · ${snapshot.error}` : `VPS · actualizado ${updatedLabel(snapshot)}`)
    : phase === 3 ? 'Simulación completada. Puedes reiniciar el recorrido.' : running ? 'Reproduciendo · la fase avanza cada 5 segundos.' : 'Simulación pausada · avanza a tu ritmo.';
  const playback = live
    ? '<button class="primary" data-action="refresh">Actualizar VPS</button>'
    : `<button data-action="play" ${phase === 3 ? 'disabled' : ''}>${running ? 'Pausar' : 'Reproducir'}</button><button class="primary" data-action="next" ${phase === 3 ? 'disabled' : ''}>Avanzar</button><button class="quiet" data-action="reset">Reiniciar</button>`;
  const sourceLabel = live ? 'VPS · datos en vivo' : 'Demo · datos simulados';
  const footerLabel = live ? 'Conectado al VPS mediante el adaptador de snapshot y reportes.' : 'Simulación local, sin conexión al VPS. Arte generado con IA para esta demo.';
  root.innerHTML = `<header class="topbar"><a class="brand" id="brand" href="/mission-demo.html${live ? '?source=vps' : ''}">Explorarte</a><nav aria-label="Vista principal"><button data-view="office" aria-pressed="${view === 'office'}">Organización</button><button data-view="missions" aria-pressed="${view === 'missions'}">Misiones</button></nav><span class="demo-label">${sourceLabel}</span></header>
    <main><div class="mission-bar"><label class="mission-picker" for="mission-select"><span class="sr-only">Misión seleccionada</span><select id="mission-select">${snapshot.missions.map((m, i) => `<option value="${i}" ${m.id === snapshot.mission.id ? 'selected' : ''}>${m.id} / ${m.name}</option>`).join('')}</select></label><span class="phase-label">${live ? 'Estado' : 'Fase'}: <strong>${live ? (snapshot.mission.statusLabel || snapshot.mission.status) : snapshot.mission.stageLabel}</strong></span><div class="playback">${playback}</div></div>
    ${missionResultMarkup(snapshot)}${view === 'missions' ? missionsMarkup(snapshot) : `<div class="workspace"><section class="floor-panel" aria-label="Oficina de la misión"><h1 class="sr-only">Organización viva</h1><div class="floor-viewport" tabindex="0" id="floor-viewport" aria-label="Plano de oficinas. Usa las flechas para desplazarte cuando amplíes el piso."><div class="floor-sizer" style="--zoom:${zoom}">${floorMarkup(snapshot, selected, selectedRoom, running)}</div></div><div class="map-toolbar"><button data-action="fit">Piso completo</button><div class="zoom-controls"><button data-action="out" aria-label="Reducir zoom" ${zoom <= 1 ? 'disabled' : ''}>−</button><output aria-label="Nivel de zoom">${Math.round(zoom * 100)}%</output><button data-action="in" aria-label="Ampliar zoom" ${zoom >= 2 ? 'disabled' : ''}>+</button></div><button data-action="center">Centrar</button></div><details class="room-directory"><summary>Departamentos y puestos · ${snapshot.agents.length} perfiles</summary><div>${snapshot.rooms.map(room => `<section><button data-room="${room.id}">${room.name}<small>${room.statusLabel}</small></button>${snapshot.agents.filter(a => a.room === room.id).map(a => `<button class="roster-person" data-person="${a.agentId}">${a.name}<small>${a.activityLabel}</small></button>`).join('') || '<span>Sin asignaciones</span>'}</section>`).join('')}</div></details><p class="floor-hint">Selecciona una oficina o un personaje para consultar su trabajo.</p></section><aside class="inspector" aria-label="Detalle de la selección">${sidebarMarkup(snapshot, selected, selectedRoom, tab, evidenceOpen, { messages: ceoMessages, live, busy: chatBusy, draft: chatDraft, composer: missionComposer, error: chatError })}</aside></div>`}
    <p class="simulation-status ${live && snapshot.error ? 'live-error' : ''}" role="status">${statusLabel}</p></main><footer class="site-footer">${footerLabel}</footer>`;
  if (view === 'office') { paintSprites(root); root.querySelector('.floor-viewport')?.scrollTo(...scrollPosition); }
  const focusSelector = focus || previousFocus?.selector;
  if (focusSelector) {
    const target = root.querySelector(focusSelector);
    if (target && !target.disabled) target.focus({ preventScroll: true });
    else root.querySelector(live ? '[data-action="refresh"]' : '[data-action="reset"]')?.focus({ preventScroll: true });
    if (!focus && target && !target.disabled && previousFocus?.start !== null && typeof target.setSelectionRange === 'function') {
      const max = target.value.length;
      target.setSelectionRange(Math.min(previousFocus.start, max), Math.min(previousFocus.end ?? previousFocus.start, max));
      if (previousFocus.scrollTop !== null) target.scrollTop = previousFocus.scrollTop;
    }
  }
}
function selectMission(index) {
  stopPlayback();
  source.select(index);
  selected = defaultAgent(); selectedRoom = 'direction'; tab = 'status'; evidenceOpen = false;
}
function budgetToMicrousd(value) {
  const normalized = String(value || '').trim().replace(',', '.');
  if (!/^\d+(\.\d{1,6})?$/.test(normalized)) throw new Error('Usa un presupuesto positivo con hasta 6 decimales.');
  const [whole, fraction = ''] = normalized.split('.');
  const micros = Number(BigInt(whole) * 1000000n + BigInt(fraction.padEnd(6, '0')));
  validateBudgetMicrousd(micros);
  return micros;
}
async function sendCEO() {
  const text = chatDraft.trim();
  if (!text || chatBusy) return;
  try {
    const objective = parseMissionCommand(text);
    if (objective !== null) {
      ceoMessages.push({ role: 'user', text });
      chatDraft = '';
      chatError = '';
      missionComposer = { objective, budget: '5' };
      render({ focus: '#mission-objective' });
      return;
    }
  } catch (error) {
    chatError = error.message;
    render({ focus: '#ceo-chat-input' });
    return;
  }
  ceoMessages.push({ role: 'user', text });
  chatDraft = '';
  chatError = '';
  chatBusy = true;
  render();
  try {
    const result = await chatClient.chat(text);
    ceoMessages.push({ role: 'ceo', text: result.message });
  } catch (error) {
    ceoMessages.push({ role: 'ceo', text: error.message });
  } finally {
    chatBusy = false;
    render({ focus: '#ceo-chat-input' });
  }
}
async function createMissionFromChat() {
  if (!missionComposer || chatBusy) return;
  const objective = root.querySelector('#mission-objective')?.value.trim() || '';
  const budget = root.querySelector('#mission-budget')?.value || missionComposer.budget;
  if (!objective) { chatError = 'Escribe un objetivo para la misión.'; render({ focus: '#mission-objective' }); return; }
  let budgetMicrousd;
  try { budgetMicrousd = budgetToMicrousd(budget); } catch (error) { chatError = error.message; render({ focus: '#mission-budget' }); return; }
  chatBusy = true;
  chatError = '';
  render();
  try {
    const idempotencyKey = crypto.randomUUID();
    const result = await chatClient.createMission({ objective, budgetMicrousd, idempotencyKey });
    ceoMessages.push({ role: 'ceo', text: result.message });
    missionComposer = null;
    if (liveMode && source.refresh) {
      await source.refresh();
      const createdId = result.mission?.id;
      const createdIndex = createdId ? source.snapshot().missions.findIndex(mission => mission.id === createdId) : -1;
      if (createdIndex >= 0) {
        selected = defaultAgent(); selectedRoom = 'direction'; tab = 'status'; evidenceOpen = false;
        await source.select(createdIndex);
      }
    }
  } catch (error) {
    chatError = error.message;
  } finally {
    chatBusy = false;
    render({ focus: missionComposer ? '#mission-objective' : '#ceo-chat-input' });
  }
}
root.addEventListener('change', event => {
  if (event.target.id === 'mission-select') { selectMission(Number(event.target.value)); render({ focus: '#mission-select' }); }
});
root.addEventListener('input', event => {
  if (event.target.id === 'ceo-chat-input') chatDraft = event.target.value;
  if (event.target.id === 'mission-objective' && missionComposer) missionComposer.objective = event.target.value;
  if (event.target.id === 'mission-budget' && missionComposer) missionComposer.budget = event.target.value;
});
root.addEventListener('submit', event => {
  if (event.target.id === 'ceo-chat-form') { event.preventDefault(); void sendCEO(); }
  if (event.target.id === 'mission-composer-form') { event.preventDefault(); void createMissionFromChat(); }
});
root.addEventListener('click', event => {
  const b = event.target.closest('button'); if (!b || b.disabled) return;
  let focus = focusKey();
  if (b.dataset.chatPrompt !== undefined) { tab = 'chat'; chatDraft = b.dataset.chatPrompt; chatError = ''; render({ focus: '#ceo-chat-input' }); return; }
  if (b.dataset.chatCancel !== undefined) { tab = 'chat'; missionComposer = null; chatError = ''; render({ focus: '#ceo-chat-input' }); return; }
  if (b.dataset.view) view = b.dataset.view;
  if (b.dataset.mission !== undefined) { selectMission(Number(b.dataset.mission)); view = 'office'; focus = '#mission-select'; }
  if (b.dataset.person) {
    selected = b.dataset.person;
    const agent = source.snapshot().agents.find(item => item.agentId === selected);
    if (agent) selectedRoom = agent.room;
    tab = 'status'; evidenceOpen = false;
  }
  if (b.dataset.room) { selectedRoom = b.dataset.room; selected = null; tab = 'status'; evidenceOpen = false; }
  if (b.dataset.tab) {
    tab = b.dataset.tab;
    if (tab === 'status') {
      const ceo = source.snapshot().agents.find(item => item.agentId === 'ceo');
      if (ceo) { selected = ceo.agentId; selectedRoom = ceo.room; }
    }
    evidenceOpen = false;
  }
  const action = b.dataset.action;
  if (action === 'return') {
    const target = root.querySelector(selected ? `.agent[data-person="${selected}"]` : `.room-label[data-room="${selectedRoom}"]`);
    target?.focus(); target?.scrollIntoView({ block: 'center', inline: 'center' }); return;
  }
  if (action === 'refresh') void source.refresh?.();
  if (action === 'play') {
    if (running) stopPlayback();
    else { running = true; timer = setInterval(() => { const previousFocus = focusKey(); source.next(); if (source.snapshot().mission.phase === 3) stopPlayback(); render({ focus: previousFocus }); }, 5000); }
  }
  if (action === 'next') { source.next(); if (source.snapshot().mission.phase === 3) stopPlayback(); }
  if (action === 'reset') { stopPlayback(); source.reset(); evidenceOpen = false; }
  if (action === 'in') zoom = Math.min(2, zoom + .25);
  if (action === 'out') zoom = Math.max(1, zoom - .25);
  if (action === 'fit') zoom = 1;
  if (action === 'evidence') evidenceOpen = !evidenceOpen;
  render({ focus });
  if (action === 'center' || action === 'fit') { const viewport = root.querySelector('.floor-viewport'); viewport?.scrollTo({ left: (viewport.scrollWidth - viewport.clientWidth) / 2, top: 0 }); }
  if ((b.dataset.person || b.dataset.room) && mobile()) { const heading = root.querySelector('#person-details'); heading?.focus(); heading?.scrollIntoView({ block: 'start' }); }
});
root.addEventListener('keydown', event => {
  if (event.target.id === 'ceo-chat-input' && event.key === 'Enter' && !event.shiftKey) { event.preventDefault(); root.querySelector('#ceo-chat-form')?.requestSubmit(); return; }
  if (!event.target.dataset.tab || !['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return;
  event.preventDefault();
  const tabs = ['chat', 'status', 'task', 'evidence', 'activity', ...(source.snapshot().mission.phase === 3 ? ['result'] : [])], index = tabs.indexOf(tab);
  tab = event.key === 'Home' ? tabs[0] : event.key === 'End' ? tabs.at(-1) : tabs[(index + (event.key === 'ArrowRight' ? 1 : tabs.length - 1)) % tabs.length];
  evidenceOpen = false; render({ focus: `[data-tab="${tab}"]` });
});
window.addEventListener('pagehide', stop);
root.innerHTML = `<main class="loading"><h1>Preparando la oficina…</h1><p role="status">${liveMode ? 'Conectando con el VPS y cargando el piso…' : 'Cargando el piso y los personajes locales.'}</p></main>`;
try {
  await Promise.all([loadFloorArt(), source.isLive ? source.load() : Promise.resolve()]);
  source.start?.();
  render();
} catch {
  root.innerHTML = '<main class="loading"><h1>No se pudo cargar la oficina</h1><p>Revisa el servidor y vuelve a cargar los recursos.</p><a href="/mission-demo.html">Reintentar</a></main>';
}
