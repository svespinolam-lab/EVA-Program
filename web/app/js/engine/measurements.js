/* EVA · Physical measurements store: the 12 physical parameters with value,
   unit, date/time, source and a 7-day history, kept on this phone.

   Records: App.storage 'measurements' = { crewId: { key: [ {v, at, src, dev?, x?} ] } }
     v   number (blood pressure: systolic; x.dia = diastolic)
     at  Unix ms of the measurement
     src 'manual' | 'device' (paired hardware) | 'demo' (simulated: never real)
   Kept: last 30 days and at most 500 records per parameter.

   Traffic light: parameters that have thresholds in config.js (hr, hrv, bp,
   spo2, temp, rr) use the app's single classifier App.levels.classify
   (level 0 green, 1 yellow, 2-3 red), so the card never disagrees with the
   risk engine. The others use the documented ranges below (green / yellow,
   anything else red); weight uses the change versus the personal baseline. */
(function () {
  'use strict';
  var App = window.App = window.App || {};
  var STORE = 'measurements', DAY = 864e5, KEEP = 30 * DAY, MAX = 500, LIVE_EVERY = 5 * 60000;

  var LIVE = ['hr', 'hrv', 'spo2', 'temp', 'rr'];
  var REQUIRED12 = ['hr', 'hrv', 'bp', 'orthostatic', 'spo2', 'temp', 'rr', 'glucose', 'sleep', 'weight', 'hydration', 'radiationMonth'];
  // plausible: values outside are rejected as input errors.
  // green / yellow: [min, max] ranges for parameters without config thresholds.
  var PARAMS = {
    hr:             { unit: 'bpm',      plausible: [25, 230] },
    hrv:            { unit: 'ms',       plausible: [5, 300] },
    bp:             { unit: 'mmHg',     plausible: [60, 260], dia: [30, 160] },
    orthostatic:    { unit: 'mmHg',     plausible: [-40, 80],  green: [-40, 9],  yellow: [-40, 19] },  // systolic drop on standing; >= 20 = red
    spo2:           { unit: '%',        plausible: [50, 100] },
    temp:           { unit: '°C',       plausible: [30, 43] },
    rr:             { unit: 'rpm',      plausible: [3, 70] },
    glucose:        { unit: 'mg/dL',    plausible: [20, 600],  green: [70, 99],  yellow: [60, 125] },  // fasting
    sleep:          { unit: 'h',        plausible: [0, 16],    green: [7, 9.5],  yellow: [6, 11] },
    weight:         { unit: 'kg',       plausible: [30, 250] },                                       // vs baseline: <2 % green, <5 % yellow
    hydration:      { unit: '1–8',      plausible: [1, 8],     green: [1, 3],    yellow: [4, 5] },     // urine colour scale
    radiationMonth: { unit: 'mSv',      plausible: [0, 1000],  green: [0, 30],   yellow: [0, 50] },    // cumulative this month
  };
  var CONFIG_LIGHT = { hr: 'hr', hrv: 'hrv', bp: 'bp', spo2: 'spo2', temp: 'temp', rr: 'rr' };

  function now() { return Date.now(); }
  function load() { var d = App.storage && App.storage.get(STORE, {}); return d && typeof d === 'object' ? d : {}; }
  function save(d) { if (App.storage) App.storage.set(STORE, d); }
  function list(d, crewId, key) { d[crewId] = d[crewId] || {}; d[crewId][key] = d[crewId][key] || []; return d[crewId][key]; }
  function inRange(v, r) { return r && v >= r[0] && v <= r[1]; }
  function dayKey(t) { var d = new Date(t); return d.getFullYear() + '-' + ('0' + (d.getMonth() + 1)).slice(-2) + '-' + ('0' + d.getDate()).slice(-2); }

  /* add: value is a number, or {sys, dia} for blood pressure. */
  function add(crewId, key, value, meta) {
    var p = PARAMS[key];
    meta = meta || {};
    if (!p || !crewId) return { ok: false, error: 'Unknown parameter' };
    var v = value, x = null;
    if (key === 'bp') {
      v = value && Number(value.sys);
      var dia = value && Number(value.dia);
      if (!Number.isFinite(dia) || !inRange(dia, p.dia)) return { ok: false, error: 'Diastolic must be ' + p.dia[0] + '–' + p.dia[1] + ' mmHg' };
      if (dia >= v) return { ok: false, error: 'Diastolic must be lower than systolic' };
      x = { dia: dia };
    }
    v = Number(v);
    if (!Number.isFinite(v) || !inRange(v, p.plausible)) return { ok: false, error: 'Value must be ' + p.plausible[0] + '–' + p.plausible[1] + ' ' + p.unit };
    var src = meta.source === 'device' || meta.source === 'demo' ? meta.source : 'manual';
    var rec = { v: v, at: Number(meta.at) || now(), src: src };
    if (meta.device) rec.dev = String(meta.device).slice(0, 40);
    if (x || meta.extra) rec.x = Object.assign({}, meta.extra || {}, x || {});
    var d = load(), arr = list(d, crewId, key);
    arr.push(rec);
    arr.sort(function (a, b) { return a.at - b.at; });
    var cut = now() - KEEP;
    while (arr.length && (arr[0].at < cut || arr.length > MAX)) arr.shift();
    save(d);
    return { ok: true, record: rec };
  }

  function history(crewId, key, days) {
    var arr = ((load()[crewId] || {})[key]) || [];
    var from = now() - (days || 7) * DAY;
    return arr.filter(function (r) { return r.at >= from; });
  }
  function latest(crewId, key) { var h = ((load()[crewId] || {})[key]) || []; return h.length ? h[h.length - 1] : null; }

  /* 7 calendar days (oldest first): mean for continuous parameters, last value otherwise; null = no data. */
  function daily(crewId, key, days) {
    days = days || 7;
    var out = [], recs = history(crewId, key, days + 1);
    for (var i = days - 1; i >= 0; i--) {
      var day = dayKey(now() - i * DAY);
      var ofDay = recs.filter(function (r) { return dayKey(r.at) === day; });
      var v = null;
      if (ofDay.length) {
        v = LIVE.indexOf(key) >= 0 ? ofDay.reduce(function (s, r) { return s + r.v; }, 0) / ofDay.length : ofDay[ofDay.length - 1].v;
      }
      out.push({ day: day, v: v });
    }
    return out;
  }

  function baseline(crewId, key) {
    var m = App.crew && App.crew.byId ? App.crew.byId(crewId) : null;
    var b = (m && m.baseline) || {};
    var map = { hr: b.hr, hrv: b.hrv, bp: b.bpSys, spo2: b.spo2, temp: b.temp, rr: b.rr, weight: b.weight };
    return Number.isFinite(map[key]) ? map[key] : null;
  }

  /* 'green' | 'yellow' | 'red' | null */
  function light(key, value, crewId) {
    if (!Number.isFinite(value)) return null;
    if (CONFIG_LIGHT[key] && App.levels && App.levels.classify) {
      var lv = App.levels.classify(CONFIG_LIGHT[key], value);
      return lv === 0 ? 'green' : lv === 1 ? 'yellow' : lv >= 2 ? 'red' : null;
    }
    if (key === 'weight') {
      var b = crewId ? baseline(crewId, 'weight') : null;
      if (!b) return null;
      var pct = Math.abs(value - b) / b * 100;
      return pct < 2 ? 'green' : pct < 5 ? 'yellow' : 'red';
    }
    var p = PARAMS[key];
    if (!p || !p.green) return null;
    return inRange(value, p.green) ? 'green' : inRange(value, p.yellow) ? 'yellow' : 'red';
  }

  /* Continuous parameters get a point every 5 minutes, tagged by provenance. */
  var lastLive = {};
  function sampleLive(crewId, data) {
    if (!crewId || !data) return 0;
    var t = Number(data.t) || now();
    if (lastLive[crewId] && t - lastLive[crewId] < LIVE_EVERY) return 0;
    lastLive[crewId] = t;
    var n = 0;
    LIVE.forEach(function (k) {
      if (!Number.isFinite(data[k])) return;
      var hw = !!(App.telemetry && App.telemetry.isHardwareConnected && App.telemetry.isHardwareConnected(crewId, k));
      if (add(crewId, k, data[k], { source: hw ? 'device' : 'demo', at: t }).ok) n++;
    });
    return n;
  }

  /* One-time import of the earlier manual values (App.store 'physicalManual'). */
  function migrate() {
    if (!App.storage || App.storage.get('measurementsMigrated', false)) return 0;
    var old = (App.store && App.store.get('physicalManual')) || {}, n = 0;
    Object.keys(old).forEach(function (crewId) {
      var row = old[crewId] || {};
      Object.keys(row).forEach(function (k) {
        if (k === 'bpSys' || k === 'bpDia' || k === 'bpAt' || row[k] == null) return;
        if (PARAMS[k] && add(crewId, k, row[k], { source: 'manual' }).ok) n++;
      });
      if (row.bpSys != null && row.bpDia != null && add(crewId, 'bp', { sys: row.bpSys, dia: row.bpDia }, { source: 'manual', at: row.bpAt }).ok) n++;
    });
    App.storage.set('measurementsMigrated', true);
    return n;
  }

  App.measurements = {
    PARAMS: PARAMS, REQUIRED12: REQUIRED12,
    add: add, latest: latest, history: history, daily: daily, light: light, baseline: baseline,
    sampleLive: sampleLive, migrate: migrate,
  };
  try { migrate(); } catch (e) { /* storage unavailable: nothing to import */ }
})();
