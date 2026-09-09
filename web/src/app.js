import { demoSnapshot } from "./data.js";
import { createClient } from "./api.js";
const urlParam = new URLSearchParams(location.search).get("mode");
let storedMode = null;
try {
  storedMode = localStorage.getItem("explorarte_mode");
} catch {}
const mode =
  urlParam === "live" || urlParam === "demo"
    ? urlParam
    : storedMode === "demo"
      ? "demo"
      : "live";
const client = createClient({ mode });
let state = null,
  view = "office",
  query = "",
  paused = false,
  chatBusy = false,
  missionBusy = false,
  chatDraft = "",
  error = "",
  draftKey = "";
const messages = [
  {
    role: "ceo",
    text:
      mode === "demo"
        ? "Hola, soy tu CEO simulado. Puedes explorar el trabajo del equipo. ¿Qué te gustaría construir hoy?"
        : "Hola Eduardo, soy el CEO de tu organización. Estoy conectado al kernel de producción en el VPS y listo para coordinar a los departamentos. ¿Cuál es tu próxima directriz?",
  },
];
const $ = (s) => document.querySelector(s),
  esc = (s) =>
    String(s ?? "").replace(
      /[&<>"']/g,
      (c) =>
        ({
          "&": "&amp;",
          "<": "&lt;",
          ">": "&gt;",
          '"': "&quot;",
          "'": "&#39;",
        })[c],
    );
const money = (n) =>
  new Intl.NumberFormat("es-CL", {
    style: "currency",
    currency: "USD",
    minimumFractionDigits: 2,
  }).format(n / 1e6);
const icons = { office: "▦", missions: "⚑", learning: "◇", finance: "↗" };
const status = {
  working: "Trabajando",
  reviewing: "Revisando",
  idle: "Disponible",
  blocked: "Bloqueado",
};
function toast(text) {
  $("#toast").textContent = text;
  $("#toast").classList.add("show");
  setTimeout(() => $("#toast").classList.remove("show"), 3500);
}
function metric(label, value, detail, icon) {
  return `<article class="metric"><div>${label}<span>${icon}</span></div><strong>${value}</strong><small>${detail}</small></article>`;
}
function metrics() {
  if (!state) return "";
  const m = state.metrics;
  return (
    metric(
      "Objetivos",
      `${m.objectives.completed}<em> / ${m.objectives.total}</em>`,
      "Completados este ciclo",
      "◎",
    ) +
    metric(
      "Misiones",
      m.missions.active,
      "En ejecución · " + m.missions.completed + " completadas",
      "⚑",
    ) +
    metric(
      "Aprendizaje",
      m.learning.episodes,
      m.learning.consolidated + " episodios consolidados",
      "◇",
    ) +
    metric(
      "Skills",
      m.skills.created + m.skills.learned,
      `${m.skills.created} creadas · ${m.skills.learned} aprendidas`,
      "⌘",
    ) +
    metric(
      "Recuerdos",
      m.memories.episodic + m.memories.semantic + m.memories.corrective,
      "Memoria de la organización",
      "◫",
    ) +
    metric(
      "Gasto total",
      money(m.cost.actualMicrousd),
      "de " + money(m.cost.budgetMicrousd) + " presupuestados",
      "↗",
    )
  );
}
function render() {
  const titles = {
    office: [
      "Tu organización, en movimiento.",
      "Una mirada en vivo al trabajo que conecta tus objetivos.",
    ],
    missions: [
      "Ideas que se convierten en misiones.",
      "Sigue cada campaña, sus objetivos y el progreso del equipo.",
    ],
    learning: [
      "Cada misión nos hace mejores.",
      "Skills, aprendizajes y recuerdos que permanecen.",
    ],
    finance: [
      "Cada recurso, con propósito.",
      "Visibilidad del gasto y los presupuestos de tu organización.",
    ],
  };
  $("#app").innerHTML =
    `<aside class="sidebar"><a class="brand" href="/" aria-label="Explorarte inicio"><b>n<span>✳</span></b> Explorarte<span class="brand-tag">OS</span></a><div class="org-switch"><span class="org-avatar">E</span><div>Psi.Explorarte<small>Workspace principal</small></div><span>⌄</span></div><p class="nav-label">WORKSPACE</p><nav>${Object.entries(
      {
        office: "Oficina",
        missions: "Misiones",
        learning: "Aprendizaje",
        finance: "Finanzas",
      },
    )
      .map(
        ([k, v]) =>
          `<button data-view="${k}" class="nav-item ${view === k ? "active" : ""}"><span>${icons[k]}</span>${v}${k === "missions" && state ? `<i>${state.metrics.missions.active}</i>` : ""}</button>`,
      )
      .join(
        "",
      )}</nav><div class="sidebar-bottom"><div class="system-health"><span class="dot"></span>${mode === "demo" ? "Entorno de simulación" : state ? "Organización conectada" : "Sin conexión"}</div><p>Inteligencia que trabaja contigo.</p><div class="profile"><span>ED</span><div>Mi organización<small>Administrador</small></div><b>⌘</b></div></div></aside><main><header class="topbar"><span>Workspace <i>/</i> ${Object.entries({ office: "Oficina", missions: "Misiones", learning: "Aprendizaje", finance: "Finanzas" }).find(([k]) => k === view)[1]}</span><div class="topbar-actions"><button id="toggle-mode" class="mode-toggle-btn ${mode === "live" ? "live" : ""}" title="Alternar entre datos en vivo del VPS y demostración local">${mode === "live" ? "Cambiar a Demo" : "Conectar a VPS (Live)"}</button><span class="mode-badge">${mode === "demo" ? "◌ Demostración · datos simulados" : state ? "● VPS en vivo (161.153.205.140)" : "○ VPS sin conexión"}</span></div></header><div class="workspace"><section class="page-heading"><div><div class="eyebrow">CENTRO DE OPERACIONES</div><h1>${titles[view][0]}</h1><p>${titles[view][1]}</p></div><button class="primary" id="new-mission" ${!state?.capabilities.createMission ? "disabled" : ""}><span>＋</span> Nueva misión</button></section>${error ? `<div class="error" role="alert">${esc(error)} <button id="retry">Reintentar conexión</button></div>` : ""}<section class="metrics">${metrics()}</section><div class="content-grid"><div class="main-content">${state ? renderView() : `<div class="empty-state"><span>◌</span><h2>${error ? "Organización desconectada" : "Conectando con tu organización…"}</h2><p>${error ? "Los datos y acciones estarán disponibles cuando responda la API." : "Cargando el estado de tus equipos."}</p></div>`}</div>${chatPanel()}</div></div><footer>EXPLORARTE · ORGANIZATION OS <span>${mode === "demo" ? "Simulación local · Sin gastos reales" : "Datos del backend organizacional"}</span><span>Diseñado para avanzar juntos ↗</span></footer></main>`;
  bind();
}
function renderView() {
  if (view === "missions")
    return `<section class="panel"><div class="panel-heading"><div><h2>Campañas de la organización</h2><p>Del objetivo a la ejecución.</p></div><span class="pill">${state.missions.length} campañas</span></div>${searchBox()}<div class="mission-list">${
      state.missions
        .filter((m) => m.title.toLowerCase().includes(query.toLowerCase()))
        .map(missionCard)
        .join("") || '<p class="empty">No hay misiones con esa búsqueda.</p>'
    }</div></section>`;
  if (view === "learning")
    return `<section class="panel"><div class="panel-heading"><div><h2>Inteligencia compartida</h2><p>Lo aprendido se convierte en capacidad.</p></div><span class="pill">◇ Biblioteca</span></div><div class="learning-summary"><div><strong>${state.metrics.skills.created}</strong><span>Skills creadas</span></div><div><strong>${state.metrics.skills.learned}</strong><span>Skills aprendidas</span></div><div><strong>${state.metrics.learning.consolidated}</strong><span>Aprendizajes consolidados</span></div></div>${state.learning.map((l) => `<article class="learning-item"><span class="learning-icon">${l.type === "skill" ? "⌘" : l.type === "memory" ? "◫" : "◇"}</span><div><small>${l.type === "skill" ? "SKILL" : l.type === "memory" ? "RECUERDO" : "APRENDIZAJE"}</small><h3>${esc(l.title)}</h3><p>${esc(state.departments.find((d) => d.id === l.department)?.name)} · ${esc(l.time)}</p></div><span class="pill">${esc(l.status)}</span></article>`).join("")}<div class="memory-grid">${["episodic", "semantic", "corrective"].map((k, i) => `<div><strong>${state.metrics.memories[k]}</strong><span>Recuerdos ${["episódicos", "semánticos", "correctivos"][i]}</span></div>`).join("")}</div></section>`;
  if (view === "finance")
    return `<section class="panel"><div class="panel-heading"><div><h2>Gasto de la organización</h2><p>${mode === "demo" ? "Consumo simulado" : "Consumo"} por día · USD</p></div><span class="pill">Últimos 7 días</span></div><div class="finance-total"><strong>${money(state.metrics.cost.actualMicrousd)}</strong><span>Gasto acumulado</span></div><div class="chart">${state.spend.map((p) => `<div class="chart-col"><span>${money(p.microusd)}</span><i style="height:${Math.max(8, (p.microusd / Math.max(...state.spend.map((x) => x.microusd))) * 150)}px"></i><small>${esc(p.label)}</small></div>`).join("")}</div><div class="budget-summary"><div><span>Presupuesto total</span><strong>${money(state.metrics.cost.budgetMicrousd)}</strong></div><div><span>Estimación al cierre</span><strong>${money(state.metrics.cost.estimatedMicrousd)}</strong></div></div><div class="mission-list">${state.missions.map(missionCard).join("")}</div></section>`;
  return `<section class="panel office-panel"><div class="panel-heading"><div><h2>La oficina <span class="live-dot"></span></h2><p>${state.departments.flatMap((d) => d.roles).filter((r) => r.status === "working").length} agentes trabajando · ${state.departments.length} departamentos</p></div><button id="pause" class="quiet">${paused ? "▶ Reanudar" : "Ⅱ Pausar vista"}</button></div><div class="office-toolbar">${searchBox()}<span><i class="dot"></i>${paused ? "Vista pausada" : "Actividad del equipo"}</span></div><div class="office-floor ${paused ? "paused" : ""}">${state.departments
    .map(
      (d, i) =>
        `<section class="department" style="--dept:${/^#[0-9a-f]{6}$/i.test(d.color) ? d.color : "#9bb880"}"><header><span class="department-number">0${i + 1}</span><div><h3>${esc(d.name)}</h3><p>${esc(d.subtitle)}</p></div><span class="department-light"></span></header><div class="desks">${
          d.roles
            .filter((r) =>
              (r.name + " " + r.activity + " " + d.name)
                .toLowerCase()
                .includes(query.toLowerCase()),
            )
            .map(
              (r) =>
                `<button class="desk ${r.status}" data-role="${esc(r.id)}" aria-label="Ver ${esc(r.name)}: ${status[r.status]}"><div class="desk-scene"><div class="monitor"><i></i><i></i><i></i></div><div class="coffee"></div><div class="keyboard"></div><div class="agent">${esc(r.initials)}</div></div><strong>${esc(r.name)}</strong><small><span class="dot"></span>${status[r.status]}</small><div class="desk-tooltip">${esc(r.activity)}</div></button>`,
            )
            .join("") || '<p class="empty">Sin coincidencias</p>'
        }</div></section>`,
    )
    .join(
      "",
    )}<div class="floor-label"><span>✳</span> EXPLORARTE HQ <i>Donde las ideas empiezan a moverse</i></div></div><div class="office-legend"><span><i class="dot"></i> Trabajando</span><span><i class="dot review"></i> Revisando</span><span><i class="dot idle"></i> Disponible</span><span><i class="dot blocked"></i> Bloqueado</span><span>Selecciona un puesto para explorar ↗</span></div></section><section class="panel activity-panel"><div class="panel-heading"><h2>Pulso de la organización</h2><span class="tiny-label">ACTIVIDAD RECIENTE</span></div>${state.activity
    .slice(0, 4)
    .map(
      (a, i) =>
        `<div class="activity-item"><span class="activity-symbol">${i === 1 ? "◇" : "↗"}</span><div><strong>${esc(a.role)}</strong> <span>${esc(a.text)}</span></div><small>${esc(a.time)}</small></div>`,
    )
    .join("")}</section>`;
}
function searchBox() {
  return `<label class="search"><span>⌕</span><input id="search" placeholder="${view === "missions" ? "Buscar misión…" : "Buscar agente o departamento…"}" value="${esc(query)}" aria-label="Buscar ${view === "missions" ? "misiones" : "agentes"}"></label>`;
}
function missionCard(m) {
  const isDone = m.status === "completed";
  return `<button class="mission-card" data-mission="${esc(m.id)}">
    <div>
      <span class="tiny-label">${esc(m.id.startsWith("MS-DEMO-") ? "CAMPAÑA DEMO" : m.id)}</span>
      <span class="pill ${isDone ? "pill-success" : m.status === "review" ? "pill-warning" : ""}">${m.status === "active" ? "En ejecución" : m.status === "review" ? "En revisión" : "Completada"}</span>
    </div>
    <h3>${esc(m.title)}</h3>
    <div class="progress"><i style="width:${m.progress}%"></i></div>
    <p>${esc(m.completedTasks)} / ${esc(m.totalTasks)} tareas <strong>${m.progress}%</strong></p>
    <small>${money(m.spentMicrousd)} de ${money(m.budgetMicrousd)}</small>
    <span class="card-action-link">📋 Ver informe completo de la misión →</span>
  </button>`;
}
function chatPanel() {
  return `<aside class="panel chat-panel"><div class="chat-heading"><span class="ceo-avatar">E<span>✳</span></span><div><h2>Habla con el CEO</h2><p><i class="dot"></i>${mode === "demo" ? "CEO simulado" : "Dirección ejecutiva"}</p></div><span class="chat-dots">···</span></div><div class="chat-context"><span>◎</span> Visión global. Una conversación.</div><div class="messages" role="log" aria-live="polite"><div class="chat-date">HOY</div>${messages.map((m) => `<div class="message ${m.role}">${m.role === "ceo" ? "<small>EXPLORARTE · CEO</small>" : ""}<p>${esc(m.text)}</p></div>`).join("")}${chatBusy ? '<div class="message ceo">Preparando respuesta…</div>' : ""}</div><div class="chat-actions"><button data-prompt="Dame un resumen del estado de la organización">↗ Resumen del día</button><button data-prompt="/mision ">⚑ Crear una misión</button></div><form id="chat-form"><label class="sr-only" for="chat-input">Mensaje para el CEO</label><textarea id="chat-input" rows="2" maxlength="8000" placeholder="Comparte una idea o escribe /mision…" ${!state?.capabilities.chat ? "disabled" : ""}>${esc(chatDraft)}</textarea><div><span><kbd>/</kbd> comandos</span><button class="send" aria-label="Enviar mensaje" ${!state?.capabilities.chat || chatBusy ? "disabled" : ""}>↑</button></div></form><p class="chat-footnote">${mode === "demo" ? "Simulación local. No ejecuta agentes reales." : state ? "Conectado a tu organización." : "Sin conexión con la organización."}</p></aside>`;
}
function bind() {
  document.querySelectorAll("[data-view]").forEach(
    (b) =>
      (b.onclick = () => {
        view = b.dataset.view;
        query = "";
        render();
      }),
  );
  $("#new-mission").onclick = () => openMission();
  $("#toggle-mode")?.addEventListener("click", () => {
    const nextMode = mode === "live" ? "demo" : "live";
    try {
      localStorage.setItem("explorarte_mode", nextMode);
    } catch {}
    const u = new URL(window.location.href);
    u.searchParams.set("mode", nextMode);
    window.location.href = u.toString();
  });
  $("#retry")?.addEventListener("click", load);
  $("#pause")?.addEventListener("click", () => {
    paused = !paused;
    render();
  });
  $("#search")?.addEventListener("input", (e) => {
    const pos = e.target.selectionStart;
    query = e.target.value;
    render();
    $("#search").focus();
    $("#search").setSelectionRange(pos, pos);
  });
  document
    .querySelectorAll("[data-role]")
    .forEach((b) => (b.onclick = () => roleDetail(b.dataset.role)));
  document
    .querySelectorAll("[data-mission]")
    .forEach((b) => (b.onclick = () => missionDetail(b.dataset.mission)));
  document.querySelectorAll("[data-prompt]").forEach(
    (b) =>
      (b.onclick = () => {
        chatDraft = b.dataset.prompt;
        $("#chat-input").value = chatDraft;
        $("#chat-input").focus();
      }),
  );
  $("#chat-form").onsubmit = send;
  $("#chat-input").oninput = (e) => {
    chatDraft = e.target.value;
  };
  $("#chat-input").onkeydown = (e) => {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      $("#chat-form").requestSubmit();
    }
  };
  const log = $(".messages");
  log.scrollTop = log.scrollHeight;
}
function showDialog(html) {
  const modal = $("#modal");
  modal.innerHTML = html;
  modal.showModal();
  modal
    .querySelector("[data-close]")
    ?.addEventListener("click", () => modal.close());
  modal.onclick = (e) => {
    if (e.target === modal) {
      const r = modal.getBoundingClientRect();
      if (
        e.clientX < r.left ||
        e.clientX > r.right ||
        e.clientY < r.top ||
        e.clientY > r.bottom
      )
        modal.close();
    }
  };
}
function roleDetail(id) {
  const d = state.departments.find((d) => d.roles.some((r) => r.id === id)),
    r = d.roles.find((r) => r.id === id);
  showDialog(
    `<div class="dialog-top"><span class="eyebrow">PUESTO DE TRABAJO · ${esc(d.name)}</span><button data-close aria-label="Cerrar">×</button></div><h2>${esc(r.name)}</h2><span class="pill">${status[r.status]}</span><p class="dialog-copy">${esc(r.activity)}</p><div class="progress"><i style="width:${r.progress}%"></i></div><p>Avance de la tarea: <strong>${r.progress}%</strong></p><div class="detail-box"><small>MISIÓN ASIGNADA</small><p>${esc(state.missions.find((m) => m.id === r.missionId)?.title || "Sin misión asignada")}</p></div><p class="muted">${mode === "demo" ? "Actividad ilustrativa del puesto en esta simulación." : "Estado reportado por la organización."}</p>`,
  );
}
async function missionDetail(id) {
  const m = state?.missions?.find((x) => x.id === id) || {
    id,
    title: "Campaña " + id,
    status: "active",
    completedTasks: 0,
    totalTasks: 0,
    progress: 0,
    spentMicrousd: 0,
    budgetMicrousd: 5000000,
    department: "empresa",
  };

  showDialog(`
    <div class="report-modal">
      <div class="dialog-top">
        <span class="eyebrow">INFORME DE MISIÓN · ${esc(m.id)}</span>
        <button data-close aria-label="Cerrar">×</button>
      </div>
      <h2>${esc(m.title)}</h2>
      <div class="report-loading">
        <div class="spinner"></div>
        <p>Consultando informe ejecutivo, resoluciones departamentales y telemetría de la misión…</p>
      </div>
    </div>
  `);

  let report = null;
  try {
    report = await client.getMissionReport(id);
  } catch (err) {
    console.warn("Error fetching mission report:", err);
  }

  const modal = $("#modal");
  if (!modal || !modal.open) return;

  if (!report) {
    modal.innerHTML = `
      <div class="report-modal">
        <div class="dialog-top">
          <span class="eyebrow">CAMPAÑA ORGANIZACIONAL · ${esc(m.id)}</span>
          <button data-close aria-label="Cerrar">×</button>
        </div>
        <h2>${esc(m.title)}</h2>
        <div class="report-stats-grid">
          <div class="stat-card"><span>Estado</span><strong>${m.status === "completed" ? "Completada" : m.status === "review" ? "En revisión" : "En ejecución"}</strong></div>
          <div class="stat-card"><span>Avance</span><strong>${m.progress}%</strong></div>
          <div class="stat-card"><span>Gasto</span><strong>${money(m.spentMicrousd)}</strong></div>
          <div class="stat-card"><span>Presupuesto</span><strong>${money(m.budgetMicrousd)}</strong></div>
        </div>
        <div class="report-empty-state">
          <p class="dialog-copy">Esta misión aún no cuenta con un informe detallado registrado en el kernel.</p>
        </div>
      </div>
    `;
    modal.querySelector("[data-close]")?.addEventListener("click", () => modal.close());
    return;
  }

  const isCompleted = report.status === "completed";
  const statusLabel = isCompleted ? "Completada" : report.status === "review" ? "En revisión" : "En ejecución";
  const statusPillClass = isCompleted ? "pill-success" : report.status === "review" ? "pill-warning" : "pill-info";

  const deptCardsHtml = (report.departmentReviews || []).map((dr) => {
    const deptInfo = state?.departments?.find((d) => d.id === dr.department) || {
      name: dr.department.replace(/_/g, " ").toUpperCase(),
      color: "#8b9eb8",
    };
    const findingsList = (dr.findings || []).map((f) => `<li><span class="bullet-dot" style="background:${deptInfo.color}"></span><span>${esc(f)}</span></li>`).join("");
    return `
      <div class="report-dept-card" style="--dept-accent: ${deptInfo.color}">
        <div class="dept-card-header">
          <div>
            <span class="tiny-label" style="color: ${deptInfo.color}">DEPARTAMENTO</span>
            <h4>${esc(deptInfo.name)}</h4>
          </div>
          <span class="verdict-pill ${dr.verdict === "accept" ? "verdict-accept" : "verdict-pending"}">
            ${dr.verdict === "accept" ? "✓ ACEPTADO" : esc(dr.verdict.toUpperCase())}
          </span>
        </div>
        <ul class="dept-findings-list">
          ${findingsList || "<li><span>Revisión registrada sin discrepancias.</span></li>"}
        </ul>
      </div>
    `;
  }).join("");

  const specialistAuditsHtml = (report.specialistAudits || []).map((sa) => {
    const deptInfo = state?.departments?.find((d) => d.id === sa.department) || { color: "#8b9eb8", name: sa.department };
    return `
      <div class="specialist-item">
        <div class="specialist-header">
          <span class="specialist-role" style="border-color:${deptInfo.color}40; background:${deptInfo.color}15; color:${deptInfo.color}">${esc(sa.roleName || sa.roleId)}</span>
          <span class="specialist-title">${esc(sa.title)}</span>
          <span class="specialist-status ${sa.status === "completed" ? "status-done" : "status-prog"}">${sa.status === "completed" ? "✓ Verificada" : esc(sa.status)}</span>
        </div>
        ${sa.summary ? `<p class="specialist-summary">${esc(sa.summary)}</p>` : ""}
      </div>
    `;
  }).join("");

  let ceoClosureHtml = "";
  if (report.ceoClosure) {
    ceoClosureHtml = `
      <section class="report-section ceo-closure-section">
        <div class="section-title-row">
          <span class="section-badge">DIRECCIÓN EJECUTIVA</span>
          <h3>Dictamen de Cierre del CEO</h3>
          <span class="closure-badge">✓ Cierre Aceptado</span>
        </div>
        <div class="ceo-answer-box">
          <div class="ceo-avatar-badge">CEO</div>
          <div class="ceo-answer-content">
            <span class="quote-eyebrow">RESPUESTA FORMAL AL PROPIETARIO (OWNER)</span>
            <p class="ceo-quote">${esc(report.ceoClosure.answerToOwner || "Cierre ejecutivo completado con éxito.")}</p>
          </div>
        </div>
        ${report.ceoClosure.completedItems && report.ceoClosure.completedItems.length > 0 ? `
          <div class="ceo-items-box">
            <h4>Puntos de auditoría verificados y resueltos:</h4>
            <ul class="resolutions-list">
              ${report.ceoClosure.completedItems.map((item) => `
                <li class="resolution-item">
                  <span class="check-icon">✓</span>
                  <p>${esc(item)}</p>
                </li>
              `).join("")}
            </ul>
          </div>
        ` : ""}
        ${report.ceoClosure.blockedItems && report.ceoClosure.blockedItems.length > 0 ? `
          <div class="ceo-alert-box alert-blocked">
            <h4>Bloqueos registrados:</h4>
            <ul>${report.ceoClosure.blockedItems.map((b) => `<li>${esc(b)}</li>`).join("")}</ul>
          </div>
        ` : `<div class="clean-governance-note"><span>🛡</span> Gobernanza verificada: Sin conflictos sin resolver, sin violaciones de privacidad ni decisiones pendientes del owner.</div>`}
      </section>
    `;
  }

  modal.innerHTML = `
    <div class="report-modal">
      <div class="dialog-top">
        <div class="report-header-meta">
          <span class="eyebrow">INFORME DE MISIÓN · ${esc(report.missionId)}</span>
          <span class="pill ${statusPillClass}">${statusLabel}</span>
        </div>
        <button data-close aria-label="Cerrar">×</button>
      </div>

      <h2 class="report-main-title">${esc(report.title)}</h2>

      <div class="report-stats-grid">
        <div class="stat-card">
          <span>Tareas / Subagentes</span>
          <strong>${report.completedTasks} / ${report.totalTasks}</strong>
          <small>100% completadas</small>
        </div>
        <div class="stat-card">
          <span>Tokens consumidos</span>
          <strong>${report.totalTokens > 0 ? (report.totalTokens >= 1e6 ? (report.totalTokens / 1e6).toFixed(2) + "M" : report.totalTokens.toLocaleString()) : "—"}</strong>
          <small>${report.inputTokens > 0 ? "Prompt: " + (report.inputTokens / 1e6).toFixed(2) + "M" : "Tokens totales"}</small>
        </div>
        <div class="stat-card">
          <span>Gasto real / Techo</span>
          <strong>${money(report.spentMicrousd)}</strong>
          <small>Techo: ${money(report.budgetMicrousd)}</small>
        </div>
        <div class="stat-card">
          <span>Tiempo de ejecución</span>
          <strong>${report.duration || "En curso"}</strong>
          <small>${report.completedAt ? "Finalizado" : "Activo"}</small>
        </div>
      </div>

      ${ceoClosureHtml}

      <section class="report-section">
        <div class="section-title-row">
          <span class="section-badge">HALLAZGOS Y RESOLUCIONES</span>
          <h3>Dictamen de las 4 Unidades Operativas</h3>
        </div>
        <p class="section-desc">Auditoría coordinada e independiente de cada departamento con verificación de límites, dependencias y procesos.</p>
        <div class="dept-reports-grid">
          ${deptCardsHtml || "<p class='empty'>No se registraron revisiones departamentales.</p>"}
        </div>
      </section>

      ${specialistAuditsHtml ? `
        <section class="report-section">
          <details class="specialists-accordion" open>
            <summary class="specialists-summary-trigger">
              <div>
                <span class="section-badge">ESPECIALISTAS</span>
                <h3>Desglose de Auditorías Individuales (${report.specialistAudits.length} tareas)</h3>
              </div>
              <span class="accordion-arrow">▾</span>
            </summary>
            <div class="specialist-list">
              ${specialistAuditsHtml}
            </div>
          </details>
        </section>
      ` : ""}

      ${report.executivePlan ? `
        <section class="report-section executive-plan-section">
          <details class="plan-accordion">
            <summary class="plan-summary-trigger">
              <div>
                <span class="section-badge">PLAN INICIAL</span>
                <h3>Criterios de Éxito y Restricciones Ejecutivas</h3>
              </div>
              <span class="accordion-arrow">▾</span>
            </summary>
            <div class="plan-content">
              <h4>Criterios de éxito fijados por el CEO:</h4>
              <ul>
                ${(report.executivePlan.successCriteria || []).map((sc) => `<li><span>✓</span> ${esc(sc)}</li>`).join("")}
              </ul>
              <h4>Restricciones globales de seguridad:</h4>
              <ul>
                ${(report.executivePlan.globalConstraints || []).map((gc) => `<li><span>•</span> ${esc(gc)}</li>`).join("")}
              </ul>
            </div>
          </details>
        </section>
      ` : ""}

      <div class="report-footer">
        <button id="copy-report-btn" class="quiet">📋 Copiar resumen del informe</button>
        <button class="primary" data-close>Cerrar informe</button>
      </div>
    </div>
  `;

  modal.querySelectorAll("[data-close]").forEach((btn) => {
    btn.addEventListener("click", () => modal.close());
  });

  const copyBtn = modal.querySelector("#copy-report-btn");
  if (copyBtn) {
    copyBtn.onclick = async () => {
      const summaryText = `INFORME DE CAMPAÑA ${report.missionId}: ${report.title}\n` +
        `Estado: ${statusLabel} | Tareas: ${report.completedTasks}/${report.totalTasks} | Tokens: ${report.totalTokens.toLocaleString()} | Gasto: ${money(report.spentMicrousd)}\n\n` +
        (report.ceoClosure?.answerToOwner ? `DICTAMEN DEL CEO:\n${report.ceoClosure.answerToOwner}\n\n` : "") +
        `RESOLUCIONES:\n` + (report.keyResolutions || []).map((r, i) => `${i + 1}. ${r}`).join("\n");
      try {
        await navigator.clipboard.writeText(summaryText);
        toast("Informe copiado al portapapeles");
      } catch {
        toast("No se pudo copiar automáticamente");
      }
    };
  }
}
function openMission(objective = "") {
  if (!state?.capabilities.createMission) return;
  draftKey = crypto.randomUUID();
  showDialog(
    `<form id="mission-form"><div class="dialog-top"><span class="eyebrow">NUEVA CAMPAÑA ${mode === "demo" ? "SIMULADA" : ""}</span><button type="button" data-close aria-label="Cerrar">×</button></div><h2>¿Qué vamos a lograr?</h2><p class="dialog-copy">Define el objetivo. El CEO organizará el siguiente paso.</p><label class="field">Objetivo de la misión<textarea id="objective" rows="3" required maxlength="2000" placeholder="Ej. Diseñar una campaña de lanzamiento">${esc(objective)}</textarea></label><label class="field">Presupuesto máximo · USD<input id="budget" inputmode="decimal" value="5" required autocomplete="off"></label><div class="detail-box">${mode === "demo" ? "Esta campaña se guardará en tu navegador. No ejecuta agentes ni genera gastos reales." : "Al confirmar se enviará la campaña al backend organizacional."}</div><p id="mission-error" class="form-error" role="alert"></p><button class="primary wide" type="submit">⚑ Confirmar e iniciar campaña</button></form>`,
  );
  $("#mission-form").onsubmit = async (e) => {
    e.preventDefault();
    if (missionBusy) return;
    const val = $("#budget").value.trim().replace(",", ".");
    if (!/^\d+(\.\d{1,6})?$/.test(val)) {
      $("#mission-error").textContent =
        "Usa un monto positivo con hasta 6 decimales.";
      return;
    }
    const [whole, fraction = ""] = val.split(".");
    const micros = Number(
      BigInt(whole) * 1000000n + BigInt(fraction.padEnd(6, "0")),
    );
    if (!Number.isSafeInteger(micros) || micros <= 0) {
      $("#mission-error").textContent =
        "El presupuesto debe ser positivo y válido.";
      return;
    }
    missionBusy = true;
    const submit = $("#mission-form button[type=submit]");
    submit.disabled = true;
    try {
      const result = await client.createMission({
        objective: $("#objective").value.trim(),
        budgetMicrousd: micros,
        idempotencyKey: draftKey,
      });
      messages.push({ role: "ceo", text: result.message });
      $("#modal").close();
      chatDraft = "";
      view = "missions";
      query = "";
      try {
        state = await client.snapshot();
      } catch (refreshError) {
        error =
          "Campaña aceptada. No se pudo actualizar la vista: " +
          refreshError.message;
      }
      render();
      toast("Campaña creada correctamente");
    } catch (e) {
      $("#mission-error").textContent = e.message;
    } finally {
      missionBusy = false;
      if (submit.isConnected) submit.disabled = false;
    }
  };
}
async function send(e) {
  e.preventDefault();
  const text = $("#chat-input").value.trim();
  if (!text || chatBusy || !state?.capabilities.chat) return;
  if (/^\/mision(?:\s|$)/.test(text)) {
    openMission(text.replace(/^\/mision\s*/, ""));
    return;
  }
  messages.push({ role: "user", text });
  chatDraft = "";
  chatBusy = true;
  render();
  try {
    const result = await client.chat(text);
    messages.push({ role: "ceo", text: result.message });
  } catch (e) {
    messages.push({ role: "ceo", text: e.message });
  } finally {
    chatBusy = false;
    render();
    $("#chat-input").focus();
  }
}
async function load() {
  error = "";
  render();
  try {
    state = await client.snapshot();
  } catch (e) {
    state = null;
    error = e.message;
  }
  render();
}
void demoSnapshot;
load();
