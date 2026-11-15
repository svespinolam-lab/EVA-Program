/* EVA · Biomedical blackbox. Logs alerts, risk changes, check-ins, sensors, simulated faults, and periodic telemetry snapshots. Stored in localStorage with an event limit. Event: {id, t, crewId, type, level, key, value, msg, data?}. Types: telemetry, alert, risk, checkin, sensor, system. A level-0 alert resolves an incident. Emits blackbox {entry} except for telemetry snapshots. */
(function () {
  'use strict';
  var App = window.App = window.App || {};
  var C = App.config;

  var STORAGE_KEY = 'blackbox';
  var SAVE_EVERY_MS = 2000;

  var events = [];          // Oldest to newest
  var seq = 1;
  var saveTimer = null;
  var lastSave = 0;
  var snapTimer = null;
  var linked = {};          // Crew IDs whose initial link was logged
  var openLive = {};        // Crew ID and metric key to open incident message

  /* Throttled saves: at most once every 2 seconds */
  function flush() {
    clearTimeout(saveTimer);
    saveTimer = null;
    lastSave = Date.now();
    App.storage.set(STORAGE_KEY, events);
  }
  function scheduleSave() {
    if (saveTimer) return;
    var wait = Math.max(0, SAVE_EVERY_MS - (Date.now() - lastSave));
    saveTimer = setTimeout(flush, wait);
  }

  /* Text */
  function metricLabel(key) {
    if (key === 'link') return 'Suit signal';
    return C.metrics[key] ? C.metrics[key].label : key;
  }

  function snapshotText(d) {
    var parts = [];
    ['hr', 'spo2', 'temp', 'suitP', 'o2', 'co2', 'batt'].forEach(function (k) {
      parts.push(C.metrics[k].short + ' ' + App.fmt.metric(k, d));
    });
    if (d.bpSys) parts.push('BP ' + App.fmt.metric('bp', d));
    return parts.join(' · ');
  }

  function checkinText(ck) {
    if (!ck) return 'Mental health check-in';
    var M = C.mental;
    function opt(list, i) { return list[i] !== undefined ? String(list[i]).toLowerCase() : '—'; }
    return 'Check-in · mood ' + opt(M.mood.options, ck.mood) +
      ', energy ' + opt(M.energy.options, ck.energy) +
      ', sleep ' + (typeof ck.sleepMin === 'number' ? App.fmt.minutes(ck.sleepMin) : '—');
  }

  function csvCell(v) {
    if (v === null || v === undefined) return '';
    var s = String(v);
    return /[",\n\r]/.test(s) ? '"' + s.replace(/"/g, '""') + '"' : s;
  }

  /* Periodic telemetry snapshots */
  function snapshot() {
    if (!App.telemetry || !App.telemetry.latest) return;
    App.crew.eva().forEach(function (m) {
      var d = App.telemetry.latest(m.id);
      if (!d) return;
      var a = App.risk && App.risk.get ? App.risk.get(m.id) : null;
      api.log({
        type: 'telemetry', crewId: m.id,
        level: a && typeof a.level === 'number' ? a.level : null,
        msg: snapshotText(d),
        data: { hr: d.hr, bpSys: d.bpSys, bpDia: d.bpDia, spo2: d.spo2, temp: d.temp, suitP: d.suitP, o2: d.o2, co2: d.co2, batt: d.batt },
      });
    });
  }

  /* Close incidents left open in a previous session */
  function closeStaleIncidents() {
    api.incidents().forEach(function (inc) {
      if (inc.end === null) {
        api.log({ type: 'alert', crewId: inc.crewId, level: 0, key: inc.key, msg: 'Closed on app restart: ' + inc.message });
      }
    });
  }

  /* Public API */
  var api = {
    init: function () {
      var saved = App.storage.get(STORAGE_KEY, []);
      events = Array.isArray(saved) ? saved : [];
      seq = events.reduce(function (max, e) { return Math.max(max, Number(e.id) || 0); }, 0) + 1;
      closeStaleIncidents();
      api.log({ type: 'system', msg: 'App started · mission ' + C.mission.name + ', EVA ' + C.mission.evaNumber });

      // Alerts: a finding appeared or increased in severity
      App.bus.on('alert', function (p) {
        if (!p || !p.finding) return;
        var f = p.finding;
        openLive[p.crewId + '|' + f.key] = f.message;
        api.log({
          type: 'alert', crewId: p.crewId, level: f.level, key: f.key,
          value: typeof f.value === 'number' ? f.value : null,
          msg: f.message + (f.valueText ? ' · ' + f.valueText : ''),
          data: { message: f.message, valueText: f.valueText || '', advice: f.advice || '' },
        });
      });

      // Risk changes and resolved findings
      App.bus.on('risk', function (p) {
        if (!p || !p.assessment) return;
        var a = p.assessment;
        Object.keys(openLive).forEach(function (k) {
          var parts = k.split('|');
          if (parts[0] !== p.crewId) return;
          var still = a.findings.some(function (f) { return f.key === parts[1]; });
          if (!still) {
            api.log({ type: 'alert', crewId: p.crewId, level: 0, key: parts[1], msg: 'Resolved: ' + openLive[k] });
            delete openLive[k];
          }
        });
        if (p.changed) {
          api.log({
            type: 'risk', crewId: p.crewId, level: a.level, value: a.score,
            msg: 'Level ' + App.levels.meta(a.level).label.toLowerCase() + ' · ' + a.summary,
          });
        }
      });

      // Mental health check-in
      App.bus.on('checkin', function (p) {
        if (!p || !p.crewId) return;
        var ck = p.checkin || (App.store.get('checkins') || {})[p.crewId];
        api.log({ type: 'checkin', crewId: p.crewId, key: 'mental', msg: checkinText(ck) });
      });

      // Sensors
      App.bus.on('sensor', function (p) {
        if (!p || !p.crewId) return;
        if (p.initial) {
          if (!linked[p.crewId]) {
            linked[p.crewId] = true;
            api.log({ type: 'sensor', crewId: p.crewId, msg: 'Suit link established' });
          }
          return;
        }
        if (p.key === 'bp') {
          if (p.status !== 'idle') return;
          if (p.reason === 'lost') {
            api.log({ type: 'sensor', crewId: p.crewId, level: 2, key: 'bp', msg: 'Blood pressure measurement failed: no signal' });
            return;
          }
          var d = App.telemetry && App.telemetry.latest ? App.telemetry.latest(p.crewId) : null;
          if (d && d.bpSys) {
            api.log({ type: 'sensor', crewId: p.crewId, key: 'bp', value: d.bpSys, msg: 'Blood pressure measured: ' + App.fmt.metric('bp', d) + ' mmHg' });
          }
          return;
        }
        var label = metricLabel(p.key);
        if (p.status === 'connected') {
          api.log({ type: 'sensor', crewId: p.crewId, key: p.key, msg: 'Sensor connected: ' + label });
        } else if (p.status === 'disconnected') {
          if (p.reason === 'lost') api.log({ type: 'sensor', crewId: p.crewId, level: 2, key: p.key, msg: 'Signal lost: ' + label });
          else api.log({ type: 'sensor', crewId: p.crewId, key: p.key, msg: 'Sensor disconnected: ' + label });
        }
      });

      // Simulated faults (demo mode)
      App.bus.on('scenario', function (p) {
        if (!p || !p.crewId) return;
        api.log({
          type: 'system', crewId: p.crewId, key: p.id || null,
          msg: p.action === 'inject' ? 'Simulation started: ' + p.label : 'Simulation: faults cleared',
        });
      });

      clearInterval(snapTimer);
      snapTimer = setInterval(snapshot, (C.sim.blackboxEverySec || 5) * 1000);

      // Save before closing or backgrounding
      window.addEventListener('pagehide', flush);
      document.addEventListener('visibilitychange', function () { if (document.hidden) flush(); });
    },

    /* Save and return a normalized event */
    log: function (entry) {
      entry = entry || {};
      var e = {
        id: seq++,
        t: new Date().toISOString(),
        crewId: entry.crewId || null,
        type: entry.type || 'system',
        level: typeof entry.level === 'number' ? entry.level : null,
        key: entry.key || null,
        value: typeof entry.value === 'number' ? entry.value : null,
        msg: String(entry.msg || ''),
      };
      if (entry.data) e.data = entry.data;
      events.push(e);
      var max = C.sim.blackboxMaxEvents || 4000;
      if (events.length > max) events.splice(0, events.length - max);
      scheduleSave();
      if (e.type !== 'telemetry') App.bus.emit('blackbox', { entry: e });
      return e;
    },

    /* Newest events first. Options: crewId, type (string or list), minLevel, limit. */
    list: function (opts) {
      opts = opts || {};
      var types = opts.type ? [].concat(opts.type) : null;
      var out = [];
      for (var i = events.length - 1; i >= 0; i--) {
        var e = events[i];
        if (opts.crewId && e.crewId !== opts.crewId) continue;
        if (types && types.indexOf(e.type) < 0) continue;
        if (typeof opts.minLevel === 'number' && (e.level === null || e.level < opts.minLevel)) continue;
        out.push(e);
        if (opts.limit && out.length >= opts.limit) break;
      }
      return out;
    },

    /* Incidents from first finding to resolution, newest first */
    incidents: function () {
      var list = [], open = {};
      events.forEach(function (e) {
        if (e.type !== 'alert' || !e.key) return;
        var k = (e.crewId || '') + '|' + e.key;
        if (e.level >= 1) {
          var message = e.data && e.data.message ? e.data.message : e.msg;
          var peak = e.data && e.data.valueText ? e.data.valueText : '';
          var inc = open[k];
          if (!inc) {
            inc = { id: 'inc-' + e.id, crewId: e.crewId, key: e.key, level: e.level, start: e.t, end: null, message: message, peakValueText: peak };
            open[k] = inc;
            list.push(inc);
          } else if (e.level >= inc.level) {
            inc.level = e.level;
            inc.message = message;
            inc.peakValueText = peak || inc.peakValueText;
          }
        } else if (open[k]) {
          open[k].end = e.t;
          delete open[k];
        }
      });
      return list.reverse();
    },

    count: function () { return events.length; },

    exportJSON: function () {
      return JSON.stringify({
        app: { name: C.app.name, version: C.app.version },
        mission: C.mission,
        exportedAt: new Date().toISOString(),
        crew: App.crew.all().map(function (m) {
          return { id: m.id, name: m.name, role: m.role, age: m.age, sex: m.sex, bloodType: m.bloodType, baseline: m.baseline };
        }),
        events: events,
      }, null, 2);
    },

    exportCSV: function () {
      var crew = {};
      App.crew.all().forEach(function (m) { crew[m.id] = m; });
      var rows = ['t,crewId,role,name,type,level,key,value,msg'];
      events.forEach(function (e) {
        var m = e.crewId ? crew[e.crewId] : null;
        rows.push([e.t, e.crewId, m ? m.role : '', m ? m.name : '', e.type, e.level, e.key, e.value, e.msg].map(csvCell).join(','));
      });
      return rows.join('\n');
    },

    clear: function () {
      events = [];
      openLive = {};
      flush();
      api.log({ type: 'system', msg: 'Blackbox cleared' });
      flush();
      return true;
    },
  };

  App.blackbox = api;
})();
