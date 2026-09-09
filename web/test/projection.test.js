import test from 'node:test';
import assert from 'node:assert/strict';
import { createDemoSource, evidenceFor } from '../src/mission-demo/projection.js';
test('projection preserves rooms and permanent roles across missions, without renderer coordinates', () => {
  const source = createDemoSource(), rooms = source.snapshot().rooms.map(({id,name}) => ({id,name}));
  for (let i = 0; i < 3; i++) {
    source.select(i); const state = source.snapshot();
    assert.deepEqual(state.rooms.map(({id,name}) => ({id,name})), rooms);
    for (const id of ['ceo','research']) assert.ok(state.agents.some(a => a.agentId === id));
    assert.ok(state.agents.every(a => a.missionId === state.mission.id && !('x' in a)));
    assert.equal(new Set(state.agents.map(a => a.assignmentId)).size, state.agents.length);
    assert.ok(state.agents.every(a => a.activityLabel && a.bubble && a.tool && a.output && a.nextStep));
    assert.equal(state.rooms.length, 6);
  }
});
test('mission progression is isolated and bounded; evidence is synthetic and phase gated', () => {
  const source = createDemoSource(); source.next();
  assert.equal(source.snapshot().mission.phase, 2);
  source.select(1); assert.equal(source.snapshot().mission.phase, 0);
  source.select(0); assert.equal(source.snapshot().mission.phase, 2);
  source.next(); const version = source.snapshot().version; source.next();
  assert.equal(source.snapshot().version, version);
  assert.ok(source.snapshot().agents.every(a => a.presence === 'available'));
  const agent = source.snapshot().agents[0];
  assert.deepEqual(evidenceFor(agent, 0), []);
  assert.match(evidenceFor(agent, 2)[0].body, /ficticio/);
  source.reset(); assert.equal(source.snapshot().events.length, 1);
});
