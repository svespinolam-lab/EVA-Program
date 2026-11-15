// node tools/tests/cycle.test.mjs  (no dependencies)
import { readFileSync } from 'node:fs';
import vm from 'node:vm';
const src = readFileSync(new URL('../../web/app/js/engine/cycle.js', import.meta.url), 'utf8');
const mem = new Map();
const App = { storage: { get: (k, d) => (mem.has(k) ? JSON.parse(mem.get(k)) : d), set: (k, v) => mem.set(k, JSON.stringify(v)) } };
vm.runInNewContext(src, { window: { App }, Date, Math, JSON, Array, Object });
const C = App.cycle, DAY = 864e5;
let pass = 0, fail = 0;
function ok(c, n) { if (c) { pass++; console.log('PASS ' + n); } else { fail++; console.log('FAIL ' + n); } }
ok(!C.isEnabled() && C.phase() === null && C.adjustments().temp === 0, 'off by default: no phase, no adjustment');
C.setEnabled(true);
const now = Date.now();
C.logStart(now - 56 * DAY); C.logStart(now - 26 * DAY);
ok(C.cycleLength() === 30, 'mean cycle length from logged starts');
ok(C.phase(now - 23 * DAY) === 'menstrual', 'day 4 is menstrual');
ok(C.phase(now - 10 * DAY) === 'ovulatory', 'ovulation window = length - 14');
ok(C.phase(now) === 'luteal' && C.adjustments(now).temp === 0.3 && C.adjustments(now).hr === 3, 'luteal phase gives +0.3 C and +3 bpm');
ok(C.phase(now + 20 * DAY) === 'unknown' && C.adjustments(now + 20 * DAY).temp === 0, 'no adjustment when the next start is overdue');
C.logDay(now, { flow: 'Medium', symptoms: ['Cramps', 'Not a symptom'] });
ok(C.today().flow === 'Medium' && C.today().symptoms.length === 1, 'logs flow and only known symptoms');
ok(!/luteal|Cramps|Medium|starts/.test(JSON.stringify(C.adjustments(now))), 'what can leave the phone carries no cycle details');
C.erase();
ok(!C.isEnabled() && C.phase(now) === null, 'erase removes everything');
console.log('\n' + pass + ' passed; ' + fail + ' failed.');
process.exit(fail ? 1 : 0);