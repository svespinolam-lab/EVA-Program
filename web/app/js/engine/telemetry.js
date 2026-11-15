/* EVA · Simulated suit telemetry for EVA crew (EV*). A real sensor connection can replace this module while preserving App.telemetry APIs and events. Emits telemetry {crewId, data} each config.sim.tickMs; sensor {crewId, key, status, reason, initial} with connecting/connected/disconnected/measuring status and user/lost/null reason; scenario {crewId, id, action, label} when faults are injected or cleared. */
(function () {
  'use strict';
  var App = window.App = window.App || {};
  var C = App.config;

  /* Continuous sensors; blood pressure is measured on demand */
  var LIVE_KEYS = ['hr', 'spo2', 'temp', 'rr', 'hrv', 'suitP', 'o2', 'co2', 'batt', 'rad'];

  /* Signal dynamics: theta is mean reversion per second (0 to 1); sigma is noise per second. */
  var DYN = {
    rr:    { theta: 0.15, sigma: 0.5, min: 0, max: 60 },
    hrv:   { theta: 0.1, sigma: 1.5, min: 0, max: 160 },
    rad:   { theta: 0.3, sigma: 0.004, min: 0, max: 10 },
    hr:    { theta: 0.08, sigma: 1.2,   min: 30,  max: 220 },
    spo2:  { theta: 0.10, sigma: 0.12,  min: 70,  max: 100 },
    temp:  { theta: 0.05, sigma: 0.010, min: 33,  max: 42 },
    suitP: { theta: 0.20, sigma: 0.006, min: 0,   max: 5.5 },
    co2:   { theta: 0.05, sigma: 0.04,  min: 0,   max: 30 },
  };
  var O2_DRAIN_PER_SEC = 1 / 60;   // Drain one reserve minute per 60 real seconds
  var BATT_DRAIN_PER_SEC = 0.006;  // Battery percent per second
  var CLEAR_SEC = 20;              // Seconds to recover after clearing a fault

  /* Simulated faults (Settings > Simulate fault). metric: affected signal; target: final value; sec: transition duration. */
  var SCENARIOS = [
    { id: 'radiation', metric: 'rad', target: 3.2, sec: 10, label: 'Solar particle event',
      description: 'Radiation dose rate rises to 3.2 mSv/h in 10 seconds.' },
    { id: 'sensor', metric: 'hr', target: null, sec: 0, label: 'Detached HR sensor',
      description: 'The HR sensor cycles through implausible readings.' },
    { id: 'tachycardia', metric: 'hr',    target: 161,  sec: 30, label: 'Exertional tachycardia',
      description: 'Heart rate rises to about 160 bpm in 30 seconds.' },
    { id: 'leak',        metric: 'suitP', target: 3.5,  sec: 45, label: 'Slow pressure leak',
      description: 'Suit pressure drops from 4.3 to 3.5 psi, increasing oxygen use.' },
    { id: 'co2',         metric: 'co2',   target: 10.6, sec: 40, label: 'High helmet CO₂',
      description: 'The CO₂ scrubber fails, raising CO₂ to about 10.6 mmHg.' },
    { id: 'hypoxia',     metric: 'spo2',  target: 87,   sec: 35, label: 'Hypoxia',
      description: 'Oxygen saturation drops to 87%.' },
    { id: 'overheat',       metric: 'temp',  target: 38.6, sec: 60, label: 'Cooling failure',
      description: 'The cooling garment fails, raising body temperature to 38.6 °C.' },
    { id: 'battery',     metric: 'batt',  target: 8,    sec: 40, label: 'Low battery',
      description: 'Suit battery drops to 8%.' },
    { id: 'signal',       metric: 'link',  target: null, sec: 0,  label: 'Signal loss',
      description: 'The suit link drops and sensors stop sending data.' },
  ];

  var sims = {};       // Crew ID to suit simulation
  var remotes = {};
  function isRemote(id) {
    return !!(App.link && App.link.isLinked() && App.crew.isEva(id) && id !== App.link.deviceCrewId());
  }
  function remoteFresh(id) {
    var r = remotes[id];
    return !!(r && r.data && Date.now() - r.data.t < 5000);
  }
  function managedError() { return new Error('Managed by that explorer’s phone'); }
  var timer = null;
  var lastTick = 0;

  /* Numeric utilities */
  function gauss() {
    var u = 1 - Math.random(), v = Math.random();
    return Math.sqrt(-2 * Math.log(u)) * Math.cos(2 * Math.PI * v);
  }
  function clamp(v, lo, hi) { return Math.min(hi, Math.max(lo, v)); }
  function lerp(a, b, t) { return a + (b - a) * t; }
  function ease(t) { t = clamp(t, 0, 1); return t * t * (3 - 2 * t); }
  function round(v, d) { var f = Math.pow(10, d || 0); return Math.round(v * f) / f; }
  function decimalsOf(key) { return C.metrics[key] ? C.metrics[key].decimals : 1; }
  function scenarioById(id) { return SCENARIOS.filter(function (s) { return s.id === id; })[0] || null; }
  function noop() {}

  /* Create a suit simulation */
  function makeSim(member) {
    var b = member.baseline || {};
    var stable = member.role === 'EV2';   // Demo: EV2 starts with steady HR at caution
    var o2Start = stable ? 372 : 380;
    var battStart = stable ? 79 : 82;
    var mean = {
      hr: (b.hr || 65) + 8,
      rr: b.rr || 14, hrv: b.hrv || 58, rad: 0.055,
      spo2: clamp((b.spo2 || 98) - 0.3, 96.2, 98.8),
      temp: (b.temp || 36.6) + 0.45,
      suitP: 4.30,
      co2: stable ? 2.6 : 2.2,
    };
    var sim = {
      crewId: member.id,
      role: member.role,
      baseline: b,
      stable: stable,
      mean: mean,
      val: Object.assign({}, mean, { o2: o2Start, batt: battStart }),
      norm: { o2: o2Start, batt: battStart },   // Reserves consumed over time
      sensors: {}, lost: {}, pending: {}, hist: {}, hardware: {},
      bp: null, bpStatus: 'idle', bpPromise: null,
      scen: {},                                  // Active faults: id to {p (0..1), clearing, at}
      phase: Math.random() * Math.PI * 2,
      cycleStart: Date.now(),
      cycleOffset: Math.max(0, C.crew.findIndex(function (m) { return m.id === member.id; })) * 90,
      effort: 0,
      t: Date.now(),
    };
    LIVE_KEYS.forEach(function (k) { sim.sensors[k] = 'disconnected'; sim.hist[k] = []; });

    // Blood pressure measured 20 minutes ago near baseline
    var sys = Math.round((b.bpSys || 120) + 2 + gauss() * 2);
    var dia = Math.round((b.bpDia || 78) + 1 + gauss() * 1.5);
    sim.bp = { bpSys: sys, bpDia: dia, bpAt: Date.now() - 20 * 60000 };
    sim.hist.bp = [sys];
    return sim;
  }

  function setStatus(sim, key, status, reason, initial) {
    sim.sensors[key] = status;
    App.bus.emit('sensor', { crewId: sim.crewId, key: key, status: status, reason: reason || null, initial: !!initial });
  }

  function linkDown(sim) {
    return !!(sim.scen.signal && !sim.scen.signal.clearing);
  }

  function pushHist(arr, v) {
    arr.push(v);
    while (arr.length > C.sim.historyLength) arr.shift();
  }

  /* One simulation step; dt in seconds */
  function step(sim, dt, warmup) {
    var now = Date.now();
    var dtw = Math.min(dt, 2);   // Avoid jumps after backgrounding
    var phase = ((now - sim.cycleStart) / 1000 + sim.cycleOffset) % 360;
    sim.effort = phase < 60 ? 0 : phase < 240 ? 1 : phase < 330 ? 2 : 1;
    sim.mean.hr = (sim.baseline.hr || 65) + [8, 30, 55][sim.effort];
    sim.mean.rr = (sim.baseline.rr || 14) + [0, 5, 10][sim.effort];
    sim.mean.hrv = (sim.baseline.hrv || 58) * [1, 0.72, 0.5][sim.effort];
    var targets = {};
    var o2Mult = 1, hrBoost = 0;

    // 1) Advance active faults
    Object.keys(sim.scen).forEach(function (id) {
      var s = sim.scen[id], def = scenarioById(id);
      if (!def) return;
      if (s.clearing) {
        s.p = Math.max(0, s.p - dt / CLEAR_SEC);
        if (s.p <= 0) { delete sim.scen[id]; return; }
      } else {
        s.p = def.sec > 0 ? Math.min(1, s.p + dt / def.sec) : 1;
      }
      var e = ease(s.p);
      if (def.metric !== 'link' && id !== 'sensor') targets[def.metric] = { target: def.target, e: e };
      if (id === 'leak') o2Mult = 1 + 11 * e;     // Leaks consume oxygen faster
      if (id === 'overheat') hrBoost = 10 * e;       // Heat also raises heart rate
    });

    // 2) Noisy signals reverting toward their means
    Object.keys(DYN).forEach(function (k) {
      var d = DYN[k];
      var m = sim.mean[k];
      var theta = d.theta, sigma = d.sigma;
      if (k === 'hr') {
        m += hrBoost;
      }
      var tg = targets[k];
      if (tg) {
        m = lerp(m, tg.target, tg.e);
        sigma *= 1 - 0.5 * tg.e;
        theta = Math.max(theta, 0.15);
      }
      var v = sim.val[k] + theta * (m - sim.val[k]) * dtw + sigma * Math.sqrt(dtw) * gauss();
      sim.val[k] = clamp(v, d.min, d.max);
      if (k === 'rad' && tg) sim.val[k] = lerp(sim.mean.rad, tg.target, tg.e);
    });
    if (sim.scen.sensor && !sim.scen.sensor.clearing) {
      sim.val.hr = [0, 0, 238, 0, 12][Math.floor((now - sim.scen.sensor.at) / 1000) % 5];
    }

    // 3) Oxygen and battery reserves
    if (!warmup) {
      sim.norm.o2 = Math.max(0, sim.norm.o2 - O2_DRAIN_PER_SEC * o2Mult * dt);
      sim.norm.batt = Math.max(0, sim.norm.batt - BATT_DRAIN_PER_SEC * dt);
    }
    sim.val.o2 = sim.norm.o2;
    sim.val.batt = targets.batt ? lerp(sim.norm.batt, targets.batt.target, targets.batt.e) : sim.norm.batt;
    sim.t = now;

    // 4) Connected sensor history for sparklines
    LIVE_KEYS.forEach(function (k) {
      if (sim.sensors[k] === 'connected') pushHist(sim.hist[k], sim.val[k]);
    });
  }

  /* Create or remove suit simulations based on crew roles */
  function syncSims() {
    var evaIds = {};
    App.crew.eva().forEach(function (m) {
      if (isRemote(m.id)) {
        delete sims[m.id];
        remotes[m.id] = remotes[m.id] || { data: null, hist: {}, fresh: false };
        return;
      }
      delete remotes[m.id];
      evaIds[m.id] = true;
      var sim = sims[m.id];
      if (sim) { sim.role = m.role; return; }
      sim = sims[m.id] = makeSim(m);
      // Connect config.sim.autoConnect sensors immediately
      (C.sim.autoConnect || []).forEach(function (k) {
        if (LIVE_KEYS.indexOf(k) >= 0) setStatus(sim, k, 'connected', null, true);
      });
      // Warm up history so charts have data at startup
      for (var i = 0; i < C.sim.historyLength; i++) step(sim, 1, true);
    });
    Object.keys(sims).forEach(function (id) {
      if (!evaIds[id]) delete sims[id];
    });
    Object.keys(remotes).forEach(function (id) { if (!isRemote(id)) delete remotes[id]; });
  }

  function tick() {
    var now = Date.now();
    var dt = clamp((now - lastTick) / 1000, 0, 120);
    lastTick = now;
    Object.keys(sims).forEach(function (id) {
      step(sims[id], dt, false);
      App.bus.emit('telemetry', { crewId: id, data: api.latest(id) });
    });
    Object.keys(remotes).forEach(function (id) {
      var fresh = remoteFresh(id);
      if (remotes[id].fresh !== fresh) {
        remotes[id].fresh = fresh;
        App.bus.emit('sensor', { crewId: id, status: fresh ? 'connected' : 'disconnected', reason: fresh ? null : 'lost' });
        App.bus.emit('telemetry', { crewId: id, data: api.latest(id), remote: true });
      }
    });
  }

  /* Public API */
  var api = {
    scenarios: SCENARIOS,

    init: function () {
      App.bus.on('link', function () { if (timer) syncSims(); });
      App.bus.on('state', function (p) {
        if (p && p.patch && ('roles' in p.patch || 'linkCrewId' in p.patch || 'linkEnabled' in p.patch) && timer) {
          syncSims();
          tick();
        }
      });
    },

    start: function () {
      if (timer) return;
      syncSims();
      lastTick = Date.now();
      timer = setInterval(tick, C.sim.tickMs);
      tick();   // Emit the first reading immediately
    },

    stop: function () {
      clearInterval(timer);
      timer = null;
    },

    isRunning: function () { return !!timer; },

    /* Pair a sensor; simulated delay of 0.8 to 1.6 seconds */
    connect: function (crewId, key) {
      if (isRemote(crewId)) return Promise.reject(managedError());
      var sim = sims[crewId];
      if (!sim) return Promise.reject(new Error('This crew member is not on EVA'));
      if (key === 'bp') {
        return linkDown(sim) ? Promise.reject(new Error('No suit signal')) : Promise.resolve(key);
      }
      if (LIVE_KEYS.indexOf(key) < 0) return Promise.reject(new Error('Unknown sensor: ' + key));
      if (sim.sensors[key] === 'connected') return Promise.resolve(key);
      if (sim.pending[key]) return sim.pending[key];

      setStatus(sim, key, 'connecting');
      var p = new Promise(function (resolve, reject) {
        setTimeout(function () {
          delete sim.pending[key];
          if (sims[crewId] !== sim) { reject(new Error('This crew member is no longer on EVA')); return; }
          if (linkDown(sim)) {
            sim.lost[key] = true;
            setStatus(sim, key, 'disconnected', 'lost');
            reject(new Error('No suit signal'));
            return;
          }
          delete sim.lost[key];
          setStatus(sim, key, 'connected');
          resolve(key);
        }, 800 + Math.random() * 800);
      });
      sim.pending[key] = p;
      return p;
    },

    disconnect: function (crewId, key) {
      var sim = sims[crewId];
      if (!sim || LIVE_KEYS.indexOf(key) < 0) return;
      delete sim.lost[key];
      setStatus(sim, key, 'disconnected', 'user');
    },

    /* Connect all continuous sensors; return per-sensor results without rejecting. */
    connectAll: function (crewId) {
      return Promise.all(LIVE_KEYS.map(function (k) {
        return api.connect(crewId, k).then(function () { return { key: k, ok: true }; },
          function (err) { return { key: k, ok: false, error: err.message }; });
      }));
    },

    status: function (crewId, key) {
      if (isRemote(crewId)) return remoteFresh(crewId) && remotes[crewId].data[key === 'bp' ? 'bpSys' : key] != null ? 'connected' : 'disconnected';
      var sim = sims[crewId];
      if (key === 'bp') return sim ? sim.bpStatus : 'idle';
      return sim && sim.sensors[key] ? sim.sensors[key] : 'disconnected';
    },

    isHardwareConnected: function(crewId,key) { var sim=sims[crewId]; return !!(sim && Number.isFinite(sim.hardware[key])); },

    isConnected: function (crewId, key) {
      if (isRemote(crewId)) return api.status(crewId, key) === 'connected';
      var sim = sims[crewId];
      if (!sim) return false;
      if (key === 'bp') return !linkDown(sim);
      return sim.sensors[key] === 'connected';
    },

    /* Previously connected sensors with lost links, excluding manual disconnections */
    lost: function (crewId) {
      if (isRemote(crewId)) return remoteFresh(crewId) ? LIVE_KEYS.filter(function (k) { return remotes[crewId].data[k] == null; }) : LIVE_KEYS.slice();
      var sim = sims[crewId];
      return sim ? Object.keys(sim.lost) : [];
    },

    /* Latest reading per sensor; null when disconnected */
    latest: function (crewId) {
      if (isRemote(crewId)) {
        var r = remotes[crewId], data = Object.assign({ t: 0 }, r && r.data);
        if (!remoteFresh(crewId)) LIVE_KEYS.concat(['bpSys', 'bpDia', 'bpAt', 'effort']).forEach(function (k) { data[k] = null; });
        data.lost = !remoteFresh(crewId);
        return data;
      }
      var sim = sims[crewId];
      if (!sim) return null;
      var d = { t: sim.t, effort: sim.effort };
      LIVE_KEYS.forEach(function (k) {
        d[k] = sim.sensors[k] === 'connected' ? round(Number.isFinite(sim.hardware[k]) ? sim.hardware[k] : sim.val[k], decimalsOf(k)) : null;
      });
      d.bpSys = sim.bp ? sim.bp.bpSys : null;
      d.bpDia = sim.bp ? sim.bp.bpDia : null;
      d.bpAt = sim.bp ? sim.bp.bpAt : null;
      d.source = Object.keys(sim.hardware).length ? 'device' : 'virtual';
      return d;
    },

    ingestHardware: function(crewId,key,value) {
      var sim=sims[crewId];
      if(!sim || LIVE_KEYS.indexOf(key)<0 || !Number.isFinite(value)) return false;
      sim.hardware[key]=value;
      delete sim.lost[key];
      setStatus(sim,key,'connected');
      App.bus.emit('telemetry',{crewId:crewId,data:api.latest(crewId),hardware:true});
      return true;
    },
    clearHardware: function(crewId,key) {
      var sim=sims[crewId]; if(!sim) return;
      delete sim.hardware[key];
      setStatus(sim,key,'disconnected','lost');
      App.bus.emit('telemetry',{crewId:crewId,data:api.latest(crewId),hardware:true});
    },

    history: function (crewId, key) {
      if (isRemote(crewId)) return remotes[crewId] && remotes[crewId].hist[key] ? remotes[crewId].hist[key].slice() : [];
      var sim = sims[crewId];
      return sim && sim.hist[key] ? sim.hist[key].slice() : [];
    },

    /* Measure blood pressure in about 3 seconds */
    measureBP: function (crewId) {
      if (isRemote(crewId)) return Promise.reject(managedError());
      var sim = sims[crewId];
      if (!sim) return Promise.reject(new Error('This crew member is not on EVA'));
      if (sim.bpPromise) return sim.bpPromise;
      if (linkDown(sim)) return Promise.reject(new Error('No suit signal'));

      sim.bpStatus = 'measuring';
      App.bus.emit('sensor', { crewId: crewId, key: 'bp', status: 'measuring', reason: null, initial: false });
      sim.bpPromise = new Promise(function (resolve, reject) {
        setTimeout(function () {
          sim.bpPromise = null;
          sim.bpStatus = 'idle';
          if (sims[crewId] !== sim || linkDown(sim)) {
            App.bus.emit('sensor', { crewId: crewId, key: 'bp', status: 'idle', reason: 'lost', initial: false });
            reject(new Error('No suit signal'));
            return;
          }
          // Active fault effects on blood pressure
          var boost = 0;
          Object.keys(sim.scen).forEach(function (id) {
            var e = ease(sim.scen[id].p);
            if (id === 'tachycardia') boost += 28 * e;
            if (id === 'hypoxia') boost += 10 * e;
            if (id === 'overheat') boost += 6 * e;
            if (id === 'leak') boost += 4 * e;
          });
          var b = sim.baseline;
          var sys = Math.round((b.bpSys || 120) + 6 + boost + gauss() * 3);
          var dia = Math.round((b.bpDia || 78) + 3 + boost * 0.4 + gauss() * 2);
          sim.bp = { bpSys: sys, bpDia: dia, bpAt: Date.now() };
          pushHist(sim.hist.bp, sys);
          App.bus.emit('sensor', { crewId: crewId, key: 'bp', status: 'idle', reason: null, initial: false });
          App.bus.emit('telemetry', { crewId: crewId, data: api.latest(crewId) });
          resolve({ bpSys: sys, bpDia: dia, bpAt: sim.bp.bpAt });
        }, 2600 + Math.random() * 800);
      });
      return sim.bpPromise;
    },

    /* Inject a simulated fault to demonstrate alerts and voice */
    inject: function (crewId, scenarioId) {
      if (isRemote(crewId)) throw managedError();
      var sim = sims[crewId], def = scenarioById(scenarioId);
      if (!sim || !def) return false;
      var s = sim.scen[scenarioId];
      if (s) s.clearing = false;
      else sim.scen[scenarioId] = { p: 0, clearing: false, at: Date.now() };

      if (scenarioId === 'signal') {
        // Mark all sensors lost before emitting events for a single outage
        var cut = LIVE_KEYS.filter(function (k) { return sim.sensors[k] === 'connected'; });
        cut.forEach(function (k) { sim.lost[k] = true; sim.sensors[k] = 'disconnected'; });
        cut.forEach(function (k) {
          App.bus.emit('sensor', { crewId: crewId, key: k, status: 'disconnected', reason: 'lost', initial: false });
        });
      }
      App.bus.emit('scenario', { crewId: crewId, id: scenarioId, action: 'inject', label: def.label });
      return true;
    },

    /* Clear faults for one or all crew members; values recover gradually. */
    clearScenarios: function (crewId) {
      var ids = crewId ? [crewId] : Object.keys(sims);
      ids.forEach(function (cid) {
        var sim = sims[cid];
        if (!sim || !Object.keys(sim.scen).length) return;
        Object.keys(sim.scen).forEach(function (id) {
          if (id === 'signal') {
            delete sim.scen.signal;
            Object.keys(sim.lost).forEach(function (k) { api.connect(cid, k).catch(noop); });
          } else {
            sim.scen[id].clearing = true;
          }
        });
        App.bus.emit('scenario', { crewId: cid, id: null, action: 'clear', label: 'Simulated faults cleared' });
      });
    },

    activeScenarios: function (crewId) {
      var sim = sims[crewId];
      if (!sim) return [];
      return Object.keys(sim.scen).filter(function (id) { return !sim.scen[id].clearing; });
    },

    scenarioById: scenarioById,
    isRemote: isRemote,
    ingestRemote: function (crewId, vitals) {
      if (!isRemote(crewId) || !vitals || !Number.isFinite(vitals.t)) return false;
      var r = remotes[crewId] = remotes[crewId] || { data: null, hist: {}, fresh: false };
      if (r.data && vitals.t <= r.data.t) return false;
      var d = { t: vitals.t };
      LIVE_KEYS.concat(['effort', 'bpSys', 'bpDia']).forEach(function (k) {
        d[k] = Number.isFinite(vitals[k]) ? vitals[k] : null;
      });
      d.bpAt = d.bpSys === null ? null : vitals.t;
      r.data = d;
      LIVE_KEYS.concat(['bp']).forEach(function (k) {
        var v = d[k === 'bp' ? 'bpSys' : k];
        if (v !== null) pushHist(r.hist[k] = r.hist[k] || [], v);
      });
      r.fresh = remoteFresh(crewId);
      App.bus.emit('telemetry', { crewId: crewId, data: api.latest(crewId), remote: true });
      return true;
    },
  };

  App.telemetry = api;
})();
