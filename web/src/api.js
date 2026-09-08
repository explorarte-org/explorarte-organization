import { demoSnapshot, getDemoMissionReport } from "./data.js";
const KEY = "explorarte-organization-demo-v1";
const clone = (value) => JSON.parse(JSON.stringify(value));
export class ApiError extends Error {
  constructor(message, status = 0) {
    super(message);
    this.name = "ApiError";
    this.status = status;
  }
}
export function validateBudgetMicrousd(value) {
  if (!Number.isSafeInteger(value) || value <= 0)
    throw new ApiError(
      "El presupuesto debe ser un entero positivo de microUSD.",
    );
  return value;
}
export function parseMissionCommand(text) {
  const match = String(text)
    .trim()
    .match(/^\/mision(?:\s+(.*))?$/s);
  if (!match) return null;
  const objective = match[1]?.trim();
  if (!objective) throw new ApiError("Escribe un objetivo después de /mision.");
  return objective;
}
const validMoney = (n) => Number.isSafeInteger(n) && n >= 0;
export function validateSnapshot(s) {
  const fail = () => {
    throw new ApiError(
      "La API devolvió una estructura de organización inválida.",
    );
  };
  if (
    !s ||
    !s.organization ||
    typeof s.organization.name !== "string" ||
    !s.metrics ||
    !Number.isFinite(Date.parse(s.updatedAt))
  )
    fail();
  for (const key of [
    "departments",
    "missions",
    "learning",
    "activity",
    "spend",
  ])
    if (!Array.isArray(s[key])) fail();
  const fields = {
    objectives: ["completed", "total"],
    missions: ["active", "completed"],
    learning: ["episodes", "consolidated"],
    skills: ["created", "learned"],
    memories: ["episodic", "semantic", "corrective"],
    cost: ["actualMicrousd", "budgetMicrousd", "estimatedMicrousd"],
  };
  for (const [key, names] of Object.entries(fields))
    for (const name of names) if (!validMoney(s.metrics[key]?.[name])) fail();
  for (const d of s.departments) {
    if (
      typeof d.id !== "string" ||
      typeof d.name !== "string" ||
      !Array.isArray(d.roles)
    )
      fail();
    for (const r of d.roles)
      if (
        typeof r.id !== "string" ||
        typeof r.name !== "string" ||
        typeof r.activity !== "string" ||
        !["working", "reviewing", "idle", "blocked"].includes(r.status) ||
        !Number.isFinite(r.progress) ||
        r.progress < 0 ||
        r.progress > 100
      )
        fail();
  }
  for (const m of s.missions)
    if (
      typeof m.id !== "string" ||
      typeof m.title !== "string" ||
      !["active", "review", "completed"].includes(m.status) ||
      !validMoney(m.budgetMicrousd) ||
      !validMoney(m.spentMicrousd) ||
      !Number.isFinite(m.progress) ||
      m.progress < 0 ||
      m.progress > 100
    )
      fail();
  for (const l of s.learning)
    if (
      typeof l.id !== "string" ||
      typeof l.title !== "string" ||
      typeof l.department !== "string" ||
      typeof l.status !== "string" ||
      typeof l.time !== "string" ||
      !["skill", "memory", "episode"].includes(l.type)
    )
      fail();
  for (const a of s.activity)
    if (
      typeof a.id !== "string" ||
      typeof a.role !== "string" ||
      typeof a.text !== "string" ||
      typeof a.time !== "string"
    )
      fail();
  for (const p of s.spend)
    if (typeof p.label !== "string" || !validMoney(p.microusd)) fail();
  if (
    typeof s.capabilities?.chat !== "boolean" ||
    typeof s.capabilities?.createMission !== "boolean"
  )
    fail();
  return s;
}
export function createClient({
  mode = "demo",
  fetchImpl = globalThis.fetch,
  storage,
  timeoutMs = 12000,
} = {}) {
  if (!["demo", "live"].includes(mode))
    throw new ApiError("Modo de conexión desconocido.");
  if (storage === undefined && typeof window !== "undefined") {
    try {
      storage = window.localStorage;
    } catch {
      storage = null;
    }
  }
  let state = clone(demoSnapshot);
  let requests = {};
  if (mode === "demo") {
    try {
      const saved = JSON.parse(storage?.getItem(KEY) || "null");
      if (saved) {
        state = validateSnapshot(saved.snapshot);
        requests = saved.requests || {};
      }
    } catch {
      state = clone(demoSnapshot);
      requests = {};
    }
  }
  const persist = () => {
    try {
      storage?.setItem(
        KEY,
        JSON.stringify({
          snapshot: state,
          requests,
        }),
      );
    } catch {
      /* Demo also works without browser storage. */
    }
  };
  async function request(path, options = {}) {
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), timeoutMs);
    try {
      const response = await fetchImpl(path, {
        ...options,
        credentials: "same-origin",
        signal: controller.signal,
        headers: {
          Accept: "application/json",
          ...(options.body
            ? {
                "Content-Type": "application/json",
              }
            : {}),
          ...options.headers,
        },
      });
      if (!response.ok) {
        const messages = {
          401: "Inicia sesión para conectar con tu organización.",
          403: "Tu sesión no tiene permiso para esta acción.",
          404: "El backend organizacional no está configurado.",
          409: "Esta solicitud ya existe con otros datos. Actualiza antes de reintentar.",
          429: "Demasiadas solicitudes. Espera unos segundos e inténtalo de nuevo.",
        };
        throw new ApiError(
          messages[response.status] ||
            `El servidor devolvió HTTP ${response.status}. Reintenta o revisa el backend.`,
          response.status,
        );
      }
      try {
        return await response.json();
      } catch {
        throw new ApiError(
          "El servidor no devolvió JSON válido. Revisa el backend.",
        );
      }
    } catch (error) {
      if (error instanceof ApiError) throw error;
      throw new ApiError(
        controller.signal.aborted
          ? "La conexión agotó el tiempo de espera. Reintenta."
          : "No se pudo conectar con la API. Revisa la conexión y el backend.",
      );
    } finally {
      clearTimeout(timer);
    }
  }
  return {
    mode,
    async snapshot() {
      return mode === "demo"
        ? clone(state)
        : validateSnapshot(await request("/api/organization/snapshot"));
    },
    async chat(text) {
      const message = String(text).trim();
      if (!message) throw new ApiError("Escribe un mensaje.");
      if (message.length > 8000)
        throw new ApiError("El mensaje no puede superar 8.000 caracteres.");
      if (mode === "live") {
        const result = await request("/api/organization/ceo/messages", {
          method: "POST",
          body: JSON.stringify({
            message,
          }),
        });
        if (typeof result?.message !== "string")
          throw new ApiError("La API devolvió una respuesta de chat inválida.");
        return result;
      }
      return {
        message:
          "[CEO simulado] " +
          (/estado|avance|resumen/i.test(message)
            ? "Tenemos " +
              state.metrics.missions.active +
              " misiones en curso y " +
              state.metrics.objectives.completed +
              " de " +
              state.metrics.objectives.total +
              " objetivos completados."
            : "He recibido tu mensaje en esta demostración. Para iniciar una campaña simulada, escribe /mision seguido de tu objetivo."),
      };
    },
    async createMission({ objective, budgetMicrousd, idempotencyKey } = {}) {
      objective = typeof objective === "string" ? objective.trim() : "";
      if (!objective || objective.length > 2000)
        throw new ApiError(
          "El objetivo debe tener entre 1 y 2.000 caracteres.",
        );
      validateBudgetMicrousd(budgetMicrousd);
      if (
        typeof idempotencyKey !== "string" ||
        !idempotencyKey.trim() ||
        idempotencyKey.length > 200 ||
        /[\r\n]/.test(idempotencyKey)
      )
        throw new ApiError(
          "La campaña necesita una clave de idempotencia válida.",
        );
      if (mode === "live") {
        const result = await request("/api/organization/missions", {
          method: "POST",
          headers: {
            "Idempotency-Key": idempotencyKey,
          },
          body: JSON.stringify({
            objective,
            budgetMicrousd,
          }),
        });
        if (
          !result?.mission ||
          typeof result.mission.id !== "string" ||
          typeof result.message !== "string"
        )
          throw new ApiError("La API devolvió una campaña inválida.");
        return result;
      }
      const fingerprint = JSON.stringify({
        objective,
        budgetMicrousd,
      });
      if (Object.hasOwn(requests, idempotencyKey)) {
        const prior = requests[idempotencyKey];
        if (prior.fingerprint !== fingerprint)
          throw new ApiError(
            "Esta clave ya fue usada para una campaña diferente.",
            409,
          );
        return clone(prior.result);
      }
      const mission = {
        id: `MS-DEMO-${Date.now()}-${state.missions.length}`,
        title: objective,
        status: "active",
        department: "empresa",
        progress: 0,
        budgetMicrousd,
        spentMicrousd: 0,
        completedTasks: 0,
        totalTasks: 4,
      };
      state.missions.unshift(mission);
      state.metrics.missions.active++;
      state.metrics.objectives.total++;
      state.updatedAt = new Date().toISOString();
      state.activity.unshift({
        id: `a-${mission.id}`,
        role: "CEO simulado",
        text: `Inició la campaña: ${objective}`,
        time: "Ahora",
      });
      state.departments[0].roles[0].activity = `Planificando: ${objective}`;
      state.departments[0].roles[0].missionId = mission.id;
      state.departments[0].roles[0].progress = 0;
      const result = {
        mission,
        message:
          "[CEO simulado] Campaña creada. Preparé 4 tareas iniciales y asigné la planificación a Dirección. No se ejecutan agentes ni se generan gastos reales.",
      };
      Object.defineProperty(requests, idempotencyKey, {
        value: {
          fingerprint,
          result: clone(result),
        },
        enumerable: true,
        writable: true,
        configurable: true,
      });
      persist();
      return clone(result);
    },
    resetDemo() {
      if (mode !== "demo")
        throw new ApiError("El reinicio solo está disponible en modo demo.");
      state = clone(demoSnapshot);
      requests = {};
      persist();
      return clone(state);
    },
    async getMissionReport(id) {
      if (!id || typeof id !== "string")
        throw new ApiError("ID de misión requerido.");
      if (mode === "live") {
        return await request(
          `/api/organization/missions/${encodeURIComponent(id)}/report`,
        );
      }
      return getDemoMissionReport(id, state);
    },
  };
}
