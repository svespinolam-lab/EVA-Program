/* EVA · Core: events, state, safe storage, crew, risk levels, and number/date formatting. No rendering. App.bus events: state {patch}; telemetry {crewId, data}; sensor {crewId, key, status}; risk {crewId, assessment, prevLevel}; alert {crewId, finding}; checkin {crewId, checkin}; blackbox {entry}. */
(function () {
  'use strict';
  var App = window.App = window.App || {};
  var C = App.config;

  /* Event bus */
  var handlers = {};
  App.bus = {
    on: function (evt, fn) {
      (handlers[evt] = handlers[evt] || []).push(fn);
      return function () { App.bus.off(evt, fn); };
    },
    off: function (evt, fn) {
      var list = handlers[evt];
      if (!list) return;
      var i = list.indexOf(fn);
      if (i >= 0) list.splice(i, 1);
    },
    emit: function (evt, payload) {
      (handlers[evt] || []).slice().forEach(function (fn) {
        try { fn(payload); } catch (err) { console.error('[EVA] event error "' + evt + '"', err); }
      });
    },
  };

  /* Safe storage: if localStorage fails in private mode or previews, continue with in-memory storage. */
  var memory = {};
  App.storage = {
    get: function (key, fallback) {
      var k = C.storage.prefix + key;
      try {
        var raw = window.localStorage.getItem(k);
        if (raw !== null) return JSON.parse(raw);
      } catch (e) { /* No access: use memory */ }
      return Object.prototype.hasOwnProperty.call(memory, k) ? memory[k] : fallback;
    },
    set: function (key, value) {
      var k = C.storage.prefix + key;
      memory[k] = value;
      try { window.localStorage.setItem(k, JSON.stringify(value)); } catch (e) { /* Full or blocked */ }
    },
    remove: function (key) {
      var k = C.storage.prefix + key;
      delete memory[k];
      try { window.localStorage.removeItem(k); } catch (e) { /* Nothing to remove */ }
    },
  };

  /* App state */
  var defaultRoles = {};
  C.crew.forEach(function (m) { defaultRoles[m.id] = m.role; });

  var PERSIST = ['theme', 'roles', 'checkins', 'profiles', 'selectedCrewId', 'voiceRate', 'linkEnabled', 'linkCrewId', 'stationHost', 'dailyLog'];
  var servedByStation = /^https?:$/.test(location.protocol) && location.pathname.indexOf('/app') === 0;
  var state = {
    linkEnabled: App.storage.get('linkEnabled', servedByStation),
    linkCrewId: App.storage.get('linkCrewId', 'AST-001'),
    stationHost: App.storage.get('stationHost', ''),
    dailyLog: App.storage.get('dailyLog', {}),
    view: 'log',                                   // Current view
    selectedCrewId: App.storage.get('selectedCrewId', null),
    theme: App.storage.get('theme', C.app.defaultTheme),
    voiceOn: false,                                     // Browsers require a tap to enable speech
    voiceRate: App.storage.get('voiceRate', C.voice.rate),
    roles: App.storage.get('roles', defaultRoles),      // Crew ID to role, e.g. AST-001: EV1
    checkins: App.storage.get('checkins', {}),          // Crew ID to { mood, energy, sleepMin, at }
    profiles: App.storage.get('profiles', {}),          // Identity overrides edited in Profile
    evaStart: Date.now(),                               // EVA start time for the T+ clock
  };

  App.store = {
    get: function (key) { return key ? state[key] : state; },
    set: function (patch) {
      Object.keys(patch).forEach(function (k) {
        state[k] = patch[k];
        if (PERSIST.indexOf(k) >= 0) App.storage.set(k, patch[k]);
      });
      App.bus.emit('state', { patch: patch });
    },
  };

  /* Crew */
  App.crew = {
    /* All crew with current roles and profile overrides */
    all: function () {
      return C.crew.map(function (m) {
        var merged = Object.assign({}, m, state.profiles[m.id] || {});
        merged.role = state.roles[m.id] || m.role;
        return merged;
      });
    },
    byId: function (id) {
      return App.crew.all().filter(function (m) { return m.id === id; })[0] || null;
    },
    /* Outside crew (EV1, EV2), sorted by role */
    eva: function () {
      return App.crew.all()
        .filter(function (m) { return /^EV/.test(m.role); })
        .sort(function (a, b) { return a.role.localeCompare(b.role); });
    },
    /* App operator inside the spacecraft */
    iv: function () {
      return App.crew.all().filter(function (m) { return m.role === 'IV'; })[0] || null;
    },
    /* Selected crew member, or the first EVA member */
    selected: function () {
      var m = state.selectedCrewId && App.crew.byId(state.selectedCrewId);
      return m || App.crew.eva()[0] || App.crew.all()[0];
    },
    isEva: function (id) {
      var m = App.crew.byId(id);
      return !!m && /^EV/.test(m.role);
    },
  };

  /* Risk levels */
  var OFFLINE = { id: 'off', key: 'offline', label: 'No signal', short: 'NO SIGNAL', icon: 'unlink', voice: 'no signal' };

  function inRange(v, range) {
    if (!range) return true;
    var lo = range[0], hi = range[1];
    return (lo === null || v >= lo) && (hi === null || v <= hi);
  }

  App.levels = {
    OFFLINE: OFFLINE,
    meta: function (level) {
      return (typeof level === 'number' && C.levels[level]) || OFFLINE;
    },
    /* Value to level 0..3 using config.metrics; null for missing values. */
    classify: function (metricKey, value, effort) {
      var m = C.metrics[metricKey];
      if (!m || value === null || value === undefined || isNaN(value)) return null;
      if (inRange(value, App.levels.range(metricKey, 'nominal', effort))) return 0;
      if (inRange(value, App.levels.range(metricKey, 'caution', effort))) return 1;
      if (inRange(value, App.levels.range(metricKey, 'warning', effort))) return 2;
      return 3;
    },
    range: function (key, band, effort) {
      var m = C.metrics[key], r = m[band].slice();
      if (key === 'hr' && r[1] !== null) r[1] += m.effortShift[effort] || 0;
      return r;
    },
    /* Direction outside the nominal range: low, high, or null */
    direction: function (metricKey, value, effort) {
      var m = C.metrics[metricKey];
      if (!m || value === null || value === undefined) return null;
      var r = App.levels.range(metricKey, 'nominal', effort);
      var lo = r[0], hi = r[1];
      if (lo !== null && value < lo) return 'low';
      if (hi !== null && value > hi) return 'high';
      return null;
    },
    max: function (levels) {
      return levels.reduce(function (acc, l) {
        return typeof l === 'number' && l > acc ? l : acc;
      }, 0);
    },
  };

  /* Formatting (US English) */
  var LOCALE = 'en-US';
  var nfCache = {};
  function pad(n) { return (n < 10 ? '0' : '') + n; }

  App.fmt = {
    num: function (v, decimals) {
      if (v === null || v === undefined || isNaN(v)) return '—';
      var d = decimals || 0;
      var nf = nfCache[d] || (nfCache[d] = new Intl.NumberFormat(LOCALE, { minimumFractionDigits: d, maximumFractionDigits: d }));
      return nf.format(v);
    },
    /* Metric precision; blood pressure displays as 118/76 */
    metric: function (key, data) {
      if (!data) return '—';
      if (key === 'bp') {
        return data.bpSys ? Math.round(data.bpSys) + '/' + Math.round(data.bpDia) : '—';
      }
      var m = C.metrics[key];
      return App.fmt.num(data[key], m ? m.decimals : 0);
    },
    time: function (ts) {
      var d = new Date(ts);
      return pad(d.getHours()) + ':' + pad(d.getMinutes()) + ':' + pad(d.getSeconds());
    },
    clock: function (ms) {
      var s = Math.max(0, Math.floor(ms / 1000));
      return pad(Math.floor(s / 3600)) + ':' + pad(Math.floor((s % 3600) / 60)) + ':' + pad(s % 60);
    },
    ago: function (ts) {
      var s = Math.round((Date.now() - ts) / 1000);
      if (s < 10) return 'now';
      if (s < 60) return s + ' s ago';
      var m = Math.round(s / 60);
      if (m < 60) return m + ' min ago';
      var h = Math.round(m / 60);
      return h + ' h ago';
    },
    date: function (value) {
      // Interpret YYYY-MM-DD as a local date to avoid shifting to the previous day.
      var m = typeof value === 'string' && /^(\d{4})-(\d{2})-(\d{2})$/.exec(value);
      var d = m ? new Date(+m[1], +m[2] - 1, +m[3]) : new Date(value);
      if (isNaN(d)) return String(value);
      return d.toLocaleDateString(LOCALE, { day: 'numeric', month: 'short', year: 'numeric' });
    },
    minutes: function (min) {
      var h = Math.floor(min / 60), m = Math.round(min % 60);
      return h + ' h ' + pad(m) + ' min';
    },
  };

  /* DOM utilities */
  App.dom = {
    $: function (sel, root) { return (root || document).querySelector(sel); },
    $$: function (sel, root) { return Array.prototype.slice.call((root || document).querySelectorAll(sel)); },
    /* Escape text before inserting it into HTML */
    esc: function (s) {
      return String(s === null || s === undefined ? '' : s)
        .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
        .replace(/"/g, '&quot;').replace(/'/g, '&#39;');
    },
  };
})();
