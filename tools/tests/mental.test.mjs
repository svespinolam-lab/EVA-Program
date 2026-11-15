import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const read = path => readFileSync(new URL('../../' + path, import.meta.url), 'utf8');
const source = read('web/app/js/engine/mental.js');
const context = vm.createContext({ window: { App: {} } });
vm.runInContext(source, context, { filename: 'mental.js' });
const M = context.window.App.mental;
const plain = value => JSON.parse(JSON.stringify(value));
const ids = ['mood', 'energy', 'stress', 'interest', 'attention', 'team', 'lonely', 'morale'];
const titles = ['Mood', 'Energy and fatigue', 'Stress and anxiety', 'Interest and drive',
  'Attention', 'Team and connection', 'Loneliness and homesickness', 'Motivation and morale'];
const codes = ['LOW_MOOD', 'FATIGUE', 'STRESS', 'APATHY', 'ATTENTION', 'ISOLATION', 'LONELINESS', 'LOW_MORALE'];
const good = () => ({ answers: Object.fromEntries(ids.map(id => [id, 0])),
  sleepMin: 420, sleepQuality: 0, safety: 0, at: Date.now() });
function noSafety(value) {
  if (!value || typeof value !== 'object') return;
  assert.ok(!Object.hasOwn(value, 'safety'), 'Private safety key must not escape');
  Object.values(value).forEach(noSafety);
}
let passed = 0;
let failed = 0;
function test(name, fn) {
  try { fn(); passed++; console.log('PASS ' + name); }
  catch (error) { failed++; console.error('FAIL ' + name + '\n' + error.stack); }
}

test('Eight ordered sections, three ordered options, sleep and exact safety prompt', () => {
  assert.deepEqual(plain(M.SECTIONS.map(s => s.id)), ids);
  assert.deepEqual(plain(M.SECTIONS.map(s => s.title)), titles);
  for (const section of M.SECTIONS) {
    assert.ok(section.question.endsWith('?'));
    assert.deepEqual(plain(section.options.map(o => o.value)), [0, 1, 2]);
    assert.ok(section.options.every(o => typeof o.label === 'string' && o.label.length > 0));
  }
  assert.equal(M.SLEEP.hours.field, 'sleepMin');
  assert.equal(M.SLEEP.hours.stepMin, 15);
  assert.equal(M.SLEEP.hours.recommendedMin, 420);
  assert.deepEqual(plain(M.SLEEP.quality.options), [
    { label: 'Restful', value: 0 }, { label: 'Fair', value: 1 }, { label: 'Poor', value: 2 }
  ]);
  assert.equal(M.SAFETY.question, 'Right now, do you feel safe and able to carry on with your duties?');
  assert.deepEqual(plain(M.SAFETY.options.map(o => o.label)), ['Yes', 'Not sure', 'No']);
});

test('All good and all middle answers produce no concern flags', () => {
  const c = good();
  assert.equal(M.evaluate(c).level, 0);
  assert.deepEqual(plain(M.evaluate(c).flags), []);
  ids.forEach(id => { c.answers[id] = 1; });
  c.sleepQuality = 1;
  assert.equal(M.evaluate(c).level, 0);
  assert.equal(M.evaluate(c).flags.length, 0);
});

test('Each poor section gives caution and its specific flag', () => {
  ids.forEach((id, index) => {
    const c = good(); c.answers[id] = 2;
    const result = M.evaluate(c);
    assert.equal(result.level, 1);
    assert.deepEqual(plain(result.flags), [codes[index]]);
    assert.deepEqual(plain(result.sections[index]), { id, value: 2, level: 1 });
  });
});

test('Three poor sections raise overall level to 2', () => {
  const c = good();
  c.answers.mood = c.answers.energy = 2;
  assert.equal(M.evaluate(c).level, 1);
  c.answers.stress = 2;
  assert.equal(M.evaluate(c).level, 2);
  assert.deepEqual(plain(M.evaluate(c).flags), codes.slice(0, 3));
});

test('Sleep duration boundaries and quality', () => {
  for (const [sleepMin, expected] of [[0, 2], [240, 2], [299, 2], [300, 1], [419, 1], [420, 0], [720, 0]]) {
    const result = M.evaluate({ ...good(), sleepMin });
    assert.equal(result.level, expected, 'sleepMin=' + sleepMin);
    assert.equal(result.flags.includes('SHORT_SLEEP'), sleepMin < 420);
  }
  const c = { ...good(), sleepQuality: 2 };
  assert.equal(M.evaluate(c).level, 1);
  assert.deepEqual(plain(M.evaluate(c).flags), ['POOR_SLEEP']);
  c.sleepMin = 240;
  assert.equal(M.evaluate(c).level, 2);
  assert.deepEqual(plain(M.evaluate(c).flags), ['SHORT_SLEEP', 'POOR_SLEEP']);
});

test('All 6,561 section combinations follow the rules and never exceed 2', () => {
  for (let n = 0; n < 3 ** 8; n++) {
    let digits = n, poor = 0;
    const c = good();
    ids.forEach(id => { c.answers[id] = digits % 3; if (digits % 3 === 2) poor++; digits = Math.floor(digits / 3); });
    const result = M.evaluate(c);
    assert.equal(result.level, poor >= 3 ? 2 : poor ? 1 : 0);
    assert.ok(result.sections.every(s => s.level >= 0 && s.level <= 2));
    assert.equal(M.evaluate({ ...c, sleepMin: 240, sleepQuality: 2, safety: 2 }).level, 2);
  }
});

test('Legacy records evaluate and all mood/energy meanings round-trip', () => {
  for (let mood = 0; mood <= 2; mood++) {
    for (let energy = 0; energy <= 2; energy++) {
      const old = { mood, energy, sleepMin: 435, at: 123, example: true };
      const converted = M.fromLegacy(old);
      assert.equal(converted.answers.mood, 2 - mood);
      assert.equal(converted.answers.energy, 2 - energy);
      assert.equal(converted.answers.stress, null);
      assert.deepEqual(plain(M.toLegacy(converted)), old);
      const result = M.evaluate(old);
      assert.equal(result.flags.includes('LOW_MOOD'), mood === 0);
      assert.equal(result.flags.includes('FATIGUE'), energy === 0);
      assert.match(result.summary, /incomplete/i);
    }
  }
});

test('Nested answers take precedence and input records are never mutated', () => {
  const c = { ...good(), mood: 0, energy: 0 };
  const before = JSON.stringify(c);
  assert.equal(M.evaluate(c).level, 0);
  assert.equal(M.toLegacy(c).mood, 2);
  assert.equal(M.toLegacy(c).energy, 2);
  const normalized = M.fromLegacy(c);
  normalized.answers.mood = 2;
  assert.equal(JSON.stringify(c), before);
});

test('Malformed answers remain unanswered and evaluation stays bounded', () => {
  for (const invalid of [null, undefined, NaN, Infinity, -1, 3, '2', {}, false]) {
    const c = { answers: { mood: invalid }, sleepMin: invalid, sleepQuality: invalid };
    const result = M.evaluate(c);
    assert.equal(result.sections[0].value, null);
    assert.ok(result.level >= 0 && result.level <= 2);
    assert.match(result.summary, /incomplete/i);
  }
  assert.equal(M.evaluate(null).level, 0);
  assert.equal(M.evaluate().level, 0);
  assert.equal(M.evaluate({ ...good(), sleepMin: -1 }).flags.includes('SHORT_SLEEP'), false);
});

test('Safety has no key or influence in evaluation or legacy export', () => {
  const c = good();
  const evaluated = plain(M.evaluate(c));
  const legacy = plain(M.toLegacy(c));
  for (const safety of [0, 1, 2, null, 'No']) {
    const record = { ...c, safety, unexpected: { safety } };
    noSafety(M.evaluate(record)); noSafety(M.toLegacy(record));
    assert.deepEqual(plain(M.evaluate(record)), evaluated);
    assert.deepEqual(plain(M.toLegacy(record)), legacy);
  }
  assert.deepEqual(Object.keys(legacy).sort(), ['at', 'energy', 'example', 'mood', 'sleepMin']);
});

// Exercise the real store, risk and link modules with an in-memory socket.
// No network connection, browser, server or third-party package is used.
function integration() {
  const App = {}, messages = [], sockets = [], memory = new Map();
  class Socket {
    constructor() { this.readyState = 1; sockets.push(this); }
    send(text) { messages.push(JSON.parse(text)); }
    close() { this.readyState = 3; }
  }
  const elements = {};
  const document = {
    activeElement: null, body: { classList: { toggle() {} } },
    getElementById(id) { return elements[id] ||= { dataset: {}, innerHTML: '', textContent: '' }; }
  };
  const sandbox = vm.createContext({ App, console,
    window: { App, WebSocket: Socket, addEventListener() {}, localStorage: {
      getItem(k) { return memory.get(k) ?? null; }, setItem(k, v) { memory.set(k, v); }, removeItem(k) { memory.delete(k); }
    } }, document, WebSocket: Socket, navigator: { userAgent: 'test' },
    location: { protocol: 'http:', pathname: '/app/', host: 'localhost:8080' },
    setTimeout() {}, clearTimeout() {}, setInterval() {}, clearInterval() {}
  });
  for (const path of ['config.js', 'core.js', 'engine/risk.js', 'engine/link.js', 'engine/mental.js']) {
    vm.runInContext(read('web/app/js/' + path), sandbox, { filename: path });
  }
  App.ui = { toast() {} };
  App.store.set({ checkins: { 'AST-001': good() } });
  App.risk.init(); App.link.init();
  const socket = sockets[0];
  socket.onopen();
  socket.onmessage({ data: JSON.stringify({ type: 'welcome' }) });
  assert.equal(App.link.status().state, 'online');
  return { App, messages };
}

test('Risk integration preserves finding shape, caps levels and supports legacy fallback', () => {
  const { App } = integration();
  const c = good(); c.answers.mood = c.answers.energy = c.answers.stress = 2; c.sleepQuality = 2;
  App.store.set({ checkins: { 'AST-001': c } });
  const result = App.risk.evaluate('AST-001');
  assert.equal(result.groups.mental.level, 2);
  for (const f of result.groups.mental.findings) {
    assert.ok(f.level >= 1 && f.level <= 2);
    for (const key of ['key', 'group', 'label', 'short', 'level', 'value', 'valueText', 'direction',
      'trend', 'message', 'shortMessage', 'advice', 'since', 'depth']) assert.ok(Object.hasOwn(f, key));
    noSafety(f);
  }
  App.store.set({ checkins: { 'AST-001': { mood: 0, energy: 2, sleepMin: 435, at: Date.now() } } });
  assert.equal(App.risk.evaluate('AST-001').groups.mental.level, 1);
  App.mental = undefined;
  assert.equal(App.risk.evaluate('AST-001').groups.mental.level, 1);
});

test('Actual link dailyLog and outgoing readiness exclude safety and new private answers', () => {
  const { App, messages } = integration();
  const c = good(); c.answers.mood = c.answers.energy = 2;
  const legacy = App.mental.toLegacy(c);
  let baseline;
  for (const safety of [0, 1, 2]) {
    const record = { ...c, ...legacy, safety };
    App.store.set({ checkins: { 'AST-001': record }, dailyLog: {} });
    // First exercise the current-record fallback path in readiness().
    assert.equal(App.link.sendReadiness(), true);
    // Then exercise the exact sanitized event emitted by the Log save path.
    App.bus.emit('checkin', { crewId: 'AST-001', checkin: App.mental.toLegacy(record) });
    const readiness = messages.filter(m => m.type === 'readiness').at(-1);
    assert.ok(readiness.factors.includes('FATIGUE_HIGH'));
    assert.ok(readiness.factors.includes('BEHAVIORAL_LOAD'));
    noSafety(App.store.get('dailyLog')); noSafety(readiness);
    assert.ok(!Object.hasOwn(readiness, 'answers'));
    assert.ok(!Object.hasOwn(readiness, 'sleepQuality'));
    if (baseline) assert.deepEqual(readiness, baseline);
    baseline = readiness;
  }
  messages.forEach(noSafety);
  App.store.set({ checkins: { 'AST-001': { ...legacy, example: true } }, dailyLog: {} });
  App.bus.emit('checkin', { crewId: 'AST-001', checkin: { ...legacy, example: true } });
  assert.equal(App.link.sendReadiness(), false);
  assert.deepEqual(plain(App.store.get('dailyLog')), {});
});

console.log(`\n${passed} passed; ${failed} failed.`);
if (failed) process.exitCode = 1;
