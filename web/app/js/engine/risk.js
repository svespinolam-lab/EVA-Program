/* EVA · Risk classifier. Converts suit data and mental health check-ins into levels 0–3, scores 0–100, findings, and summaries. Thresholds: config.metrics. Continuous sensors rise after 3 consecutive readings and fall after 5. Blood pressure, signal, and mental health update immediately. Emits risk {crewId, assessment, prevLevel, changed} and alert {crewId, member, finding}. */
(function () {
  'use strict';
  var App = window.App = window.App || {};
  var C = App.config;

  var UP_TICKS = 3;                         // Consecutive readings before raising the level
  var DOWN_TICKS = 5;                       // Consecutive readings before lowering the level
  var BP_VALID_MS = 30 * 60000;             // Blood pressure readings are valid for 30 minutes
  var LIVE_KEYS = ['hr', 'spo2', 'temp', 'rr', 'hrv', 'suitP', 'o2', 'co2', 'batt', 'rad'];
  var GROUP_OF = { hr: 'bio', bp: 'bio', spo2: 'bio', temp: 'bio', suitP: 'suit', o2: 'suit', co2: 'suit', batt: 'suit',
                   rr: 'bio', hrv: 'bio', rad: 'suit', sensor: 'bio', link: 'link', mood: 'mental', energy: 'mental', sleep: 'mental', fatigue: 'mental' };
  var GROUP_ORDER = { link: 0, suit: 1, bio: 2, mental: 3 };   // Prioritize suit findings when severity is equal
  var SCORE_BANDS = { 1: [25, 49], 2: [50, 79], 3: [80, 100] };

  /* Brief advice: low/high indicate direction; critical applies at level 3. */
  var ADVICE = {
    rr:      { high: 'Pause activity and check breathing.', low: 'Confirm breathing and sensor fit.' },
    hrv:     { low: 'Reduce workload and allow recovery.' },
    rad:     { high: 'Seek radiation shelter and contact EVA Station.', critical: 'Return to radiation shelter immediately.' },
    hr:      { high: 'Pause activity and confirm status by radio.', low: 'Confirm status by radio and monitor heart rate.' },
    bp:      { high: 'Reduce exertion and measure again in 5 minutes.', low: 'Ask about dizziness or weakness and measure again.' },
    spo2:    { low: 'Check suit oxygen flow and prepare to return to the airlock.', critical: 'Return to the airlock immediately using backup oxygen.' },
    temp:    { high: 'Check suit cooling and reduce exertion.', low: 'Check suit heating.' },
    suitP:   { low: 'Check for leaks and prepare to return to the airlock.', high: 'Check the pressure regulator.', critical: 'Return to the airlock immediately.' },
    o2:      { low: 'Plan the return with an oxygen reserve.', critical: 'Return to the airlock immediately.' },
    co2:     { high: 'Switch to the backup scrubber and reduce exertion.', critical: 'Switch to the backup scrubber and return to the airlock.' },
    batt:    { low: 'Reduce power use and plan the return.', critical: 'Return to the airlock before the battery runs out.' },
    link:    { any: 'Try radio contact and check the suit antenna.' },
    mood:    { any: 'Check in with the crew member before the next task.' },
    energy:  { any: 'Schedule breaks and monitor performance.' },
    sleep:   { any: 'Adjust workload and prioritize rest.' },
    fatigue: { any: 'Postpone demanding tasks and monitor closely.' },
  };

  var assessments = {};    // Crew ID to latest assessment
  var tracks = {};         // Crew ID to per-key {level, up, upMin, down, downMax, since}
  var linkSince = {};      // Crew ID to signal loss time
  var everData = {};       // Crew IDs that have ever received suit data
  var lastAdvance = {};    // Last reading counted for hysteresis per crew member
  var checkinCache = {};   // Latest check-in received through an event

  /* Utilities */
  /* Lowercase the first letter of ordinary words, preserving abbreviations such as HR, SpO₂, and O?. */
  function lowerFirst(s) {
    if (!s) return s;
    var w = s.split(' ')[0];
    return /^[A-Z][a-z.]+$/.test(w) ? s.charAt(0).toLowerCase() + s.slice(1) : s;
  }
  function clamp01(v) { return Math.max(0, Math.min(1, v)); }

  function unitOf(key) { return C.metrics[key] ? C.metrics[key].unit : ''; }

  function valueText(key, data) {
    var txt = App.fmt.metric(key, data);
    return txt === '—' ? txt : txt + ' ' + unitOf(key);
  }

  function adviceFor(key, dir, level) {
    var a = ADVICE[key] || {};
    if (level >= 3 && a.critical) return a.critical;
    return a[dir] || a.any || a.high || a.low || 'Confirm status by radio.';
  }

  /* Trend over the last 10 readings: rising, falling, or null */
  function trendOf(crewId, key) {
    if (!App.telemetry || !App.telemetry.history) return null;
    var h = App.telemetry.history(crewId, key).slice(-10);
    if (h.length < 6) return null;
    var first = (h[0] + h[1] + h[2]) / 3;
    var last = (h[h.length - 1] + h[h.length - 2] + h[h.length - 3]) / 3;
    var m = C.metrics[key];
    var ref = m && m.nominal[0] !== null && m.nominal[1] !== null ? (m.nominal[1] - m.nominal[0]) : Math.abs(last) || 1;
    var delta = (last - first) / ref;
    if (delta > 0.04) return 'rising';
    if (delta < -0.04) return 'falling';
    return null;
  }

  /* Depth within the risk band (0 to 1) */
  function bandDepth(key, value, level, effort) {
    var m = C.metrics[key];
    var dir = App.levels.direction(key, value, effort);
    if (!m || !dir || !level) return 0.5;
    var side = dir === 'high' ? 1 : 0;
    var inner, outer;
    m = { nominal: App.levels.range(key, 'nominal', effort), caution: App.levels.range(key, 'caution', effort), warning: App.levels.range(key, 'warning', effort) };
    if (level === 1) { inner = m.nominal[side]; outer = m.caution[side]; }
    else if (level === 2) { inner = m.caution[side]; outer = m.warning[side]; }
    else {
      inner = m.warning[side];
      var prev = m.caution[side];
      outer = inner !== null && prev !== null ? inner + (inner - prev) : null;
    }
    if (inner === null || outer === null || inner === outer) return 0.5;
    return clamp01((value - inner) / (outer - inner));
  }

  /* Proximity to the nominal boundary: 0 at center, 1 at edge */
  function proximity(key, value, effort) {
    var m = C.metrics[key];
    if (!m || value === null || value === undefined) return 0;
    var range = App.levels.range(key, 'nominal', effort);
    var lo = range[0], hi = range[1];
    if (lo !== null && hi !== null) {
      var half = (hi - lo) / 2;
      return clamp01(Math.abs(value - (lo + hi) / 2) / half);
    }
    var edge = lo !== null ? lo : hi;
    var next = lo !== null ? m.caution[0] : m.caution[1];
    var margin = Math.abs(edge - next) * 3 || 1;
    return clamp01(1 - Math.abs(value - edge) / margin);
  }

  /* Continuous sensor hysteresis */
  function trackLevel(crewId, key, raw, advance, now) {
    var all = tracks[crewId] = tracks[crewId] || {};
    var t = all[key];
    if (!t) {   // First reading: display directly
      t = all[key] = { level: raw, up: 0, upMin: 9, down: 0, downMax: -1, since: raw > 0 ? now : null };
      return t;
    }
    if (!advance) return t;
    if (raw > t.level) {
      t.up++; t.upMin = Math.min(t.upMin, raw); t.down = 0; t.downMax = -1;
      if (t.up >= UP_TICKS) {
        if (t.level === 0) t.since = now;
        t.level = t.upMin; t.up = 0; t.upMin = 9;
      }
    } else if (raw < t.level) {
      t.down++; t.downMax = Math.max(t.downMax, raw); t.up = 0; t.upMin = 9;
      if (t.down >= DOWN_TICKS) {
        t.level = t.downMax; t.down = 0; t.downMax = -1;
        if (t.level === 0) t.since = null;
      }
    } else {
      t.up = 0; t.upMin = 9; t.down = 0; t.downMax = -1;
    }
    return t;
  }

  function metricFinding(crewId, key, level, value, data, since) {
    var m = C.metrics[key];
    var dir = App.levels.direction(key, value, data.effort);
    var adjective = dir === 'low' ? m.low : m.high;
    var trend = trendOf(crewId, key);
    return {
      key: key,
      group: GROUP_OF[key],
      label: m.label,
      short: m.short,
      level: level,
      value: value,
      valueText: valueText(key, data),
      direction: dir,
      trend: trend,
      message: m.label + ' ' + adjective,
      shortMessage: lowerFirst(m.short + ' ' + adjective),
      advice: adviceFor(key, dir, level),
      since: since || Date.now(),
      depth: bandDepth(key, value, level, data.effort),
    };
  }

  /* Mental health check-in */
  function checkinOf(crewId) {
    var stored = (App.store.get('checkins') || {})[crewId] || null;
    var cached = checkinCache[crewId] || null;
    if (stored && cached) return (cached.at || 0) > (stored.at || 0) ? cached : stored;
    return stored || cached;
  }

  function mentalFindings(ck) {
    var out = [];
    if (!ck) return out;
    var M = C.mental;
    var since = ck.at || Date.now();
    var shortSleep = typeof ck.sleepMin === 'number' && ck.sleepMin < M.sleep.recommendedMin;
    function add(key, level, label, message, vText) {
      out.push({ key: key, group: 'mental', label: label, short: label, level: level, value: null, valueText: vText,
        direction: null, trend: null, message: message, shortMessage: lowerFirst(message), advice: adviceFor(key, null, level),
        since: since, depth: 0.5 });
    }
    if (ck.mood === 0) add('mood', 1, M.mood.label, 'Low mood', M.mood.options[0]);
    if (ck.energy === 0) add('energy', 1, M.energy.label, 'Low energy', M.energy.options[0]);
    if (shortSleep) {
      var veryShort = ck.sleepMin < 300;
      add('sleep', veryShort ? 2 : 1, M.sleep.label, veryShort ? 'Very short sleep' : 'Insufficient sleep', App.fmt.minutes(ck.sleepMin));
    }
    if (ck.mood === 0 && ck.energy === 0 && shortSleep) add('fatigue', 2, 'Fatigue', 'Likely fatigue', '');
    return out;   // Mental findings never exceed level 2
  }

  /* Score from 0 to 100 */
  function scoreOf(level, findings, data) {
    if (level === null) return 0;
    if (level === 0) {
      var worst = 0;
      if (data) LIVE_KEYS.forEach(function (k) { worst = Math.max(worst, proximity(k, data[k], data.effort)); });
      return Math.round(4 + 14 * worst);
    }
    var band = SCORE_BANDS[level];
    var top = findings[0];
    var others = findings.length - 1;
    var mix = 0.7 * (top ? top.depth : 0.5) + 0.3 * Math.min(1, others / 3);
    return Math.round(band[0] + (band[1] - band[0]) * clamp01(mix));
  }

  function summaryOf(level, findings, hasTelemetry) {
    if (level === null) return 'No suit data yet.';
    if (!findings.length) return hasTelemetry ? 'All readings in range.' : 'No check-in findings.';
    function phrase(f, first) {
      var txt = first ? f.message : f.shortMessage;
      if (f.trend === 'falling' && f.direction === 'low') txt = (first ? f.label : f.short) + ' falling';
      if (f.trend === 'rising' && f.direction === 'high') txt = (first ? f.label : f.short) + ' rising';
      return first ? txt : lowerFirst(txt);
    }
    var parts = [phrase(findings[0], true)];
    if (findings[1]) parts.push(phrase(findings[1], false));
    var s = parts.join(findings.length > 2 ? ', ' : ' and ');
    if (findings.length > 2) s += ' and ' + (findings.length - 2) + ' more';
    return s + '.';
  }

  /* Assess a crew member; advance only for periodic readings that count toward hysteresis. */
  function assess(crewId, advance) {
    var now = Date.now();
    // Only EVA crew (EV*) have suit data
    var data = App.crew.isEva(crewId) && App.telemetry && App.telemetry.latest ? App.telemetry.latest(crewId) : null;
    if (advance) {
      if (lastAdvance[crewId] && now - lastAdvance[crewId] < C.sim.tickMs * 0.6) advance = false;
      else lastAdvance[crewId] = now;
    }
    var findings = [];
    var groups = {
      bio: { level: null, findings: [] },
      suit: { level: null, findings: [] },
      mental: { level: null, findings: [], checkin: null },
      link: { level: null, findings: [] },
    };
    var hasTelemetry = false;

    if (data) {
      // 1) Continuous sensors with hysteresis
      LIVE_KEYS.forEach(function (k) {
        var v = data[k];
        if (v === null || v === undefined) {
          if (tracks[crewId]) delete tracks[crewId][k];
          return;
        }
        hasTelemetry = true;
        var g = groups[GROUP_OF[k]];
        if (g.level === null) g.level = 0;
        var faulty = (k === 'hr' && (v < 25 || v > 240)) ||
          (k === 'spo2' && (v > 100.5 || v < 50)) || (k === 'temp' && (v < 30 || v > 43.5));
        if (faulty) {
          if (tracks[crewId]) delete tracks[crewId][k];
          if (!findings.some(function (f) { return f.key === 'sensor'; })) findings.push({
            key: 'sensor', metric: k, group: 'bio', level: 1, label: 'Possible sensor fault', short: 'Sensor',
            message: 'Possible sensor fault', shortMessage: 'possible sensor fault', value: v,
            valueText: valueText(k, data), advice: 'Check the sensor fit; do not act on this value alone.',
            since: now, depth: 0.5, direction: null, trend: null,
          });
          return;
        }
        var raw = App.levels.classify(k, v, data.effort);
        var t = trackLevel(crewId, k, raw, advance, now);
        if (t.level > 0) findings.push(metricFinding(crewId, k, t.level, v, data, t.since));
      });

      // 2) Blood pressure measured within 30 minutes
      if (data.bpSys && data.bpAt && now - data.bpAt < BP_VALID_MS) {
        hasTelemetry = true;
        if (groups.bio.level === null) groups.bio.level = 0;
        var lv = App.levels.classify('bp', data.bpSys);
        if (lv > 0) findings.push(metricFinding(crewId, 'bp', lv, data.bpSys, data, data.bpAt));
      }

      // 3) Suit signal
      var lost = App.telemetry.lost ? App.telemetry.lost(crewId) : [];
      groups.link.level = 0;
      if (lost.length) {
        linkSince[crewId] = linkSince[crewId] || now;
        findings.push({
          key: 'link', group: 'link', label: 'Suit signal', short: 'Signal', level: 2,
          value: lost.length, valueText: lost.length + ' of ' + LIVE_KEYS.length + ' sensors without data',
          direction: null, trend: null, message: 'No suit signal', shortMessage: 'no suit signal',
          advice: adviceFor('link', null, 2), since: linkSince[crewId], depth: 0.5,
        });
      } else {
        delete linkSince[crewId];
      }
    }
    if (hasTelemetry) everData[crewId] = true;

    // 4) Mental health
    var ck = checkinOf(crewId);
    if (ck) {
      groups.mental.checkin = ck;
      groups.mental.level = 0;
      mentalFindings(ck).forEach(function (f) { findings.push(f); });
    }

    // Sort by severity, then suit before body and mind
    findings.sort(function (a, b) {
      return (b.level - a.level) || (GROUP_ORDER[a.group] - GROUP_ORDER[b.group]) || (b.depth - a.depth) || (a.since - b.since);
    });
    findings.forEach(function (f) {
      var g = groups[f.group];
      g.findings.push(f);
      g.level = Math.max(g.level || 0, f.level);
    });

    var level = null;
    if (everData[crewId] || ck) level = findings.length ? findings[0].level : 0;
    return {
      crewId: crewId,
      level: level,
      score: scoreOf(level, findings, data),
      label: App.levels.meta(level).label,
      summary: summaryOf(level, findings, hasTelemetry),
      findings: findings,
      groups: groups,
      updatedAt: now,
    };
  }

  /* Evaluate, save, announce new or worsening findings, and emit risk */
  function run(crewId, advance) {
    var prev = assessments[crewId] || null;
    var a = assess(crewId, advance);
    assessments[crewId] = a;

    var before = {};
    if (prev) prev.findings.forEach(function (f) { before[f.key] = f.level; });
    var member = App.crew.byId(crewId);
    a.findings.forEach(function (f) {
      if (f.level > (before[f.key] || 0)) App.bus.emit('alert', { crewId: crewId, member: member, finding: f });
    });

    var prevLevel = prev ? prev.level : null;
    App.bus.emit('risk', { crewId: crewId, assessment: a, prevLevel: prevLevel, changed: prevLevel !== a.level });
    return a;
  }

  function seedExampleCheckins() {
    var current = App.store.get('checkins') || {};
    if (Object.keys(current).length) return;
    var seeds = { EV1: { mood: 2, energy: 1, sleepMin: 435 }, EV2: { mood: 1, energy: 1, sleepMin: 360 } };
    var out = {};
    App.crew.eva().forEach(function (m) {
      var s = seeds[m.role];
      if (s) out[m.id] = { mood: s.mood, energy: s.energy, sleepMin: s.sleepMin, at: Date.now() - 95 * 60000, example: true };
    });
    if (Object.keys(out).length) App.store.set({ checkins: out });
  }

  /* Public API */
  App.risk = {
    init: function () {
      App.bus.on('telemetry', function (p) { if (p && p.crewId) run(p.crewId, true); });
      App.bus.on('sensor', function (p) { if (p && p.crewId && !p.initial) run(p.crewId, false); });
      App.bus.on('checkin', function (p) {
        if (!p || !p.crewId) return;
        if (p.checkin) checkinCache[p.crewId] = p.checkin;
        run(p.crewId, false);
      });
      App.bus.on('state', function (p) {
        var k = p && p.patch;
        if (!k) return;
        if ('roles' in k) {
          // Crew no longer outside lose suit data
          Object.keys(assessments).forEach(function (id) {
            if (!App.crew.isEva(id)) { delete tracks[id]; delete everData[id]; delete linkSince[id]; }
          });
        }
        if ('checkins' in k || 'roles' in k) {
          var ids = {};
          App.crew.eva().forEach(function (m) { ids[m.id] = true; });
          Object.keys(k.checkins || {}).forEach(function (id) { ids[id] = true; });
          Object.keys(assessments).forEach(function (id) { ids[id] = true; });
          Object.keys(ids).forEach(function (id) { run(id, false); });
        }
      });
      seedExampleCheckins();
    },

    get: function (crewId) { return assessments[crewId] || null; },

    /* Recalculate without advancing hysteresis */
    evaluate: function (crewId) { return run(crewId, false); },

    all: function () {
      var out = {};
      Object.keys(assessments).forEach(function (id) { out[id] = assessments[id]; });
      return out;
    },
  };
})();
