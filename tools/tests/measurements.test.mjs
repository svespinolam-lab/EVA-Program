// node tools/tests/measurements.test.mjs  (no dependencies)
import { readFileSync } from "node:fs";
import vm from "node:vm";

const src = readFileSync(new URL("../../web/app/js/engine/measurements.js", import.meta.url), "utf8");
function boot(physicalManual, hardware) {
  const mem = new Map();
  const App = {
    storage: { get: (k, d) => (mem.has(k) ? JSON.parse(mem.get(k)) : d), set: (k, v) => mem.set(k, JSON.stringify(v)) },
    store: { get: (k) => (k === "physicalManual" ? physicalManual || {} : undefined) },
    levels: { classify: (k, v) => (k === "hr" ? (v <= 115 ? 0 : v <= 135 ? 1 : 3) : 0) },
    crew: { byId: () => ({ baseline: { hr: 62, weight: 60 } }) },
    telemetry: { isHardwareConnected: (c, k) => !!(hardware && hardware[k]) },
  };
  const ctx = { window: { App }, Date, Math, Number, Object, JSON, String };
  vm.runInNewContext(src, ctx);
  return { M: App.measurements, App, mem };
}
let pass = 0, fail = 0;
function ok(cond, name) { if (cond) { pass++; console.log("PASS " + name); } else { fail++; console.log("FAIL " + name); } }

{ const { M } = boot();
  ok(M.REQUIRED12.length === 12 && new Set(M.REQUIRED12).size === 12, "exactly the 12 required parameters");
  ok(!M.add("AST-001", "hr", NaN).ok && !M.add("AST-001", "hr", 400).ok && !M.add("AST-001", "nope", 1).ok, "rejects NaN, implausible values and unknown parameters");
  ok(M.add("AST-001", "hr", 70).ok, "accepts a valid reading");
  ok(!M.add("AST-001", "bp", { sys: 80, dia: 90 }).ok && M.add("AST-001", "bp", { sys: 118, dia: 76 }).ok, "blood pressure needs diastolic below systolic");
  const bp = M.latest("AST-001", "bp");
  ok(bp.v === 118 && bp.x.dia === 76 && bp.src === "manual" && Number.isFinite(bp.at), "record keeps value, diastolic, source and time");
  const d = M.daily("AST-001", "glucose", 7);
  ok(d.length === 7 && d.every((x) => x.v === null), "7 days with gaps when there is no data");
  M.add("AST-001", "sleep", 6.5);
  ok(M.daily("AST-001", "sleep", 7)[6].v === 6.5, "today appears as the last day");
  ok(M.light("glucose", 90) === "green" && M.light("glucose", 110) === "yellow" && M.light("glucose", 140) === "red", "glucose traffic light");
  ok(M.light("weight", 61, "AST-001") === "green" && M.light("weight", 62.5, "AST-001") === "yellow" && M.light("weight", 66, "AST-001") === "red", "weight light versus baseline");
  ok(M.light("hr", 70) === "green" && M.light("hr", 125) === "yellow" && M.light("hr", 160) === "red", "configured parameters use the app classifier");
  for (let i = 0; i < 600; i++) M.add("AST-001", "temp", 36.6, { at: Date.now() - i * 1000 });
  ok(M.history("AST-001", "temp", 7).length <= 500, "history is capped at 500 records");
}
{ const { M } = boot({}, { hr: true });
  const t = Date.now();
  const n1 = M.sampleLive("AST-002", { t, hr: 80, spo2: 97 });
  const n2 = M.sampleLive("AST-002", { t: t + 60000, hr: 81, spo2: 97 });
  ok(n1 === 2 && n2 === 0, "live samples throttled to one per 5 minutes");
  ok(M.latest("AST-002", "hr").src === "device" && M.latest("AST-002", "spo2").src === "demo", "paired channel is device, simulated channel is demo");
}
{ const pm = { "AST-001": { glucose: 95, bpSys: 120, bpDia: 80, bpAt: Date.now() - 1000 } };
  const { M } = boot(pm);
  ok(M.latest("AST-001", "glucose").v === 95 && M.latest("AST-001", "bp").x.dia === 80, "earlier manual values imported");
  ok(M.migrate() === 0 && M.history("AST-001", "glucose", 7).length === 1, "import runs only once");
}
console.log("\n" + pass + " passed; " + fail + " failed.");
process.exit(fail ? 1 : 0);
