import test from "node:test";
import assert from "node:assert/strict";
import {
  createClient,
  validateBudgetMicrousd,
  parseMissionCommand,
  validateSnapshot,
} from "../src/api.js";
import { demoSnapshot } from "../src/data.js";
const storage = () => {
  const values = new Map();
  return {
    getItem: (key) => values.get(key),
    setItem: (key, value) => values.set(key, value),
  };
};
test("integer microUSD rejects decimals, negative, unsafe and string money", () => {
  for (const value of [
    0,
    -1,
    1.5,
    NaN,
    Infinity,
    Number.MAX_SAFE_INTEGER + 1,
    "5000000",
  ])
    assert.throws(() => validateBudgetMicrousd(value));
  assert.equal(validateBudgetMicrousd(5000000), 5000000);
});
test("slash command extracts full objective, rejects empty and ignores ordinary text", () => {
  assert.equal(
    parseMissionCommand("/mision Lanzar campaña nueva"),
    "Lanzar campaña nueva",
  );
  assert.throws(() => parseMissionCommand("/mision "));
  assert.equal(parseMissionCommand("Hola CEO"), null);
  assert.equal(parseMissionCommand("/misiones"), null);
});
test("demo idempotency persists and conflicts on differing payload", async () => {
  const disk = storage();
  const client = createClient({ storage: disk });
  const input = {
    objective: "Crear producto",
    budgetMicrousd: 5000000,
    idempotencyKey: "test-key",
  };
  const first = await client.createMission(input);
  assert.deepEqual(await client.createMission(input), first);
  const resumed = createClient({ storage: disk });
  assert.deepEqual(await resumed.createMission(input), first);
  assert.equal(
    (await resumed.snapshot()).missions.length,
    demoSnapshot.missions.length + 1,
  );
  await assert.rejects(
    client.createMission({ ...input, budgetMicrousd: 6000000 }),
    (e) => e.status === 409,
  );
  assert.match(first.message, /simulado/);
});
test("live unavailable never returns demo", async () => {
  const client = createClient({
    mode: "live",
    fetchImpl: async () => {
      throw new Error("offline");
    },
  });
  await assert.rejects(client.snapshot(), /API/);
  await assert.rejects(client.chat("hola"), /API/);
});
test("live contract uses same origin cookies, exact payload and idempotency header", async () => {
  const calls = [];
  const client = createClient({
    mode: "live",
    fetchImpl: async (url, options) => {
      calls.push({ url, options });
      return {
        ok: true,
        json: async () =>
          url.endsWith("snapshot")
            ? demoSnapshot
            : url.endsWith("messages")
              ? { message: "Hola" }
              : { mission: { id: "m1" }, message: "Creada" },
      };
    },
  });
  await client.snapshot();
  await client.chat("Hola");
  await client.createMission({
    objective: "Campaña",
    budgetMicrousd: 1,
    idempotencyKey: "k1",
  });
  assert.deepEqual(
    calls.map((c) => c.url),
    [
      "/api/organization/snapshot",
      "/api/organization/ceo/messages",
      "/api/organization/missions",
    ],
  );
  assert.ok(calls.every((c) => c.options.credentials === "same-origin"));
  assert.deepEqual(JSON.parse(calls[1].options.body), { message: "Hola" });
  assert.deepEqual(JSON.parse(calls[2].options.body), {
    objective: "Campaña",
    budgetMicrousd: 1,
  });
  assert.equal(calls[2].options.headers["Idempotency-Key"], "k1");
});
test("snapshot validation rejects corrupt amounts and role state", () => {
  assert.equal(validateSnapshot(demoSnapshot), demoSnapshot);
  const bad = structuredClone(demoSnapshot);
  bad.metrics.cost.actualMicrousd = 0.5;
  assert.throws(() => validateSnapshot(bad));
  bad.metrics.cost.actualMicrousd = 1;
  bad.departments[0].roles[0].status = "unknown";
  assert.throws(() => validateSnapshot(bad));
});
test("live handles authentication and timeout clearly", async () => {
  await assert.rejects(
    createClient({
      mode: "live",
      fetchImpl: async () => ({ ok: false, status: 401 }),
    }).snapshot(),
    (e) => e.status === 401,
  );
  const client = createClient({
    mode: "live",
    timeoutMs: 5,
    fetchImpl: (_, { signal }) =>
      new Promise((resolve, reject) =>
        signal.addEventListener("abort", () => reject(new Error("abort"))),
      ),
  });
  await assert.rejects(client.snapshot(), /tiempo de espera/);
});
test("live rejects malformed JSON and incomplete snapshot responses", async () => {
  const malformed = createClient({
    mode: "live",
    fetchImpl: async () => ({
      ok: true,
      json: async () => {
        throw new SyntaxError("bad JSON");
      },
    }),
  });
  await assert.rejects(malformed.snapshot(), /JSON válido/);
  const missing = createClient({
    mode: "live",
    fetchImpl: async () => ({
      ok: true,
      json: async () => ({ organization: { name: "Org" } }),
    }),
  });
  await assert.rejects(missing.snapshot(), /estructura/);
});
test("prototype-like idempotency key is safe and persisted conflicting replay rejects", async () => {
  const disk = storage();
  const input = {
    objective: "Validar campaña",
    budgetMicrousd: 1000000,
    idempotencyKey: "__proto__",
  };
  const client = createClient({ storage: disk });
  const first = await client.createMission(input);
  const resumed = createClient({ storage: disk });
  assert.deepEqual(await resumed.createMission(input), first);
  await assert.rejects(
    resumed.createMission({ ...input, objective: "Otro objetivo" }),
    (e) => e.status === 409,
  );
  assert.equal(
    (await resumed.snapshot()).missions.length,
    demoSnapshot.missions.length + 1,
  );
});
