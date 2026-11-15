/* =====================================================================
   EVA STATION · core
   ---------------------------------------------------------------------
   Live link with the Go server (/ws/console), shared state, routing,
   formatting, alarm sound, toasts and modals. The views live in
   views.js (window.Station.views). Icons come from /shared/js/icons.js
   (App.icon), the same set used by the phone app.

   Server → console messages
     snapshot      full state on connect
     tick          every second: mission + crew triage
     alert         new or upgraded alert      alert-update  response/escalation/resolution
     incident      black-box incident opened/sealed
     oplog         operator/system action     mission       mission status change
     readiness     new daily verdict          sim           simulator state
   ===================================================================== */
(function () {
  'use strict';
  var S = window.Station = window.Station || {};
  var icon = function (name, cls, label) { return window.App.icon(name, cls, label); };

  S.state = {
    connected: false, mission: null, missions: [], crew: [], trends: {}, openAlerts: {}, alerts: [], incidents: [],
    oplog: [], sim: null, info: null, startedAt: 0,
  };
  S.offset = 0;
  S.now = function () { return Date.now() + S.offset; };
  S.views = S.views || {};
  S.icon = icon;

  /* ---------- Formatting ---------- */
  function pad(n) { return (n < 10 ? '0' : '') + n; }
  S.esc = function (s) {
    return String(s === null || s === undefined ? '' : s).replace(/&/g, '&amp;').replace(/</g, '&lt;')
      .replace(/>/g, '&gt;').replace(/"/g, '&quot;').replace(/'/g, '&#39;');
  };
  S.num = function (v, d) {
    if (v === null || v === undefined || isNaN(v)) return '—';
    return Number(v).toFixed(d || 0);
  };
  S.time = function (ms) { if (!ms) return '—'; var d = new Date(ms); return pad(d.getHours()) + ':' + pad(d.getMinutes()) + ':' + pad(d.getSeconds()); };
  S.hm = function (ms) { if (!ms) return '—'; var d = new Date(ms); return pad(d.getHours()) + ':' + pad(d.getMinutes()); };
  S.date = function (ms) { return new Date(ms).toLocaleDateString('en-US', { day: 'numeric', month: 'short', year: 'numeric' }); };
  S.clock = function (ms) {
    var s = Math.max(0, Math.floor(ms / 1000));
    return pad(Math.floor(s / 3600)) + ':' + pad(Math.floor((s % 3600) / 60)) + ':' + pad(s % 60);
  };
  S.ago = function (ms) {
    if (!ms) return '—';
    var s = Math.round((S.now() - ms) / 1000);
    if (s < 5) return 'now';
    if (s < 60) return s + ' s ago';
    if (s < 3600) return Math.round(s / 60) + ' min ago';
    return Math.round(s / 3600) + ' h ago';
  };
  S.human = function (s) { s = String(s || '').replace(/_/g, ' ').toLowerCase(); return s.charAt(0).toUpperCase() + s.slice(1); };

  /* ---------- Risk vocabulary (same words as the phone app) ---------- */
  var LEVELS = {
    0: { label: 'Nominal', icon: 'check' }, 1: { label: 'Caution', icon: 'info' },
    2: { label: 'High risk', icon: 'triangle' }, 3: { label: 'Critical', icon: 'octagon' },
    off: { label: 'No data', icon: 'unlink' },
  };
  var STATUS = {
    EMERGENCY: { level: 3, label: 'Emergency' }, RISK: { level: 2, label: 'Risk' },
    VULNERABLE: { level: 1, label: 'Preventive watch' }, STABLE: { level: 0, label: 'Stable' }, NO_DATA: { level: 'off', label: 'No data' },
  };
  S.lvl = function (l) { return typeof l === 'number' && l >= 0 ? String(Math.min(3, l)) : 'off'; };
  S.levelMeta = function (l) { return LEVELS[S.lvl(l)] || LEVELS.off; };
  S.status = function (st) { return STATUS[st] || STATUS.NO_DATA; };
  S.alertLevel = function (a) { return { EMERGENCY: 3, RISK: 2, ADVISORY: 1 }[a.level] || 0; };
  S.badge = function (level, text) {
    var m = S.levelMeta(level);
    return '<span class="badge" data-level="' + S.lvl(level) + '">' + icon(m.icon) + S.esc(text || m.label) + '</span>';
  };
  S.ring = function (score, level, caption) {
    var r = 56, c = 2 * Math.PI * r, pct = Math.max(0, Math.min(100, score || 0)) / 100;
    return '<div class="ring" data-level="' + S.lvl(level) + '"><svg viewBox="0 0 132 132" aria-hidden="true">' +
      '<circle class="ring__track" cx="66" cy="66" r="' + r + '" stroke-width="11"/>' +
      '<circle class="ring__value" cx="66" cy="66" r="' + r + '" stroke-width="11" stroke-dasharray="' + c.toFixed(1) + '" stroke-dashoffset="' + (c * (1 - pct)).toFixed(1) + '"/>' +
      '</svg><div class="ring__label"><span class="ring__num">' + (level === 'off' ? '—' : Math.round(score || 0)) + '</span><span class="eyebrow">' + S.esc(caption || 'IPV') + '</span></div></div>';
  };
  S.avatar = function (m, level) {
    return '<span class="avatar avatar--lg" data-level="' + S.lvl(level) + '" aria-hidden="true">' + S.esc(m.initials || '?') + '</span>';
  };

  /* Sparkline with a shaded nominal band. opts: { band:[lo,hi], minSpan } */
  S.spark = function (values, opts) {
    opts = opts || {};
    var W = 200, H = 46, P = 3;
    var vals = values.filter(function (v) { return typeof v === 'number'; });
    var svg = '<svg viewBox="0 0 ' + W + ' ' + H + '" preserveAspectRatio="none" aria-hidden="true">';
    if (vals.length < 2) return svg + '</svg>';
    var lo = Math.min.apply(null, vals), hi = Math.max.apply(null, vals);
    if (opts.band) { lo = Math.min(lo, opts.band[0]); hi = Math.max(hi, opts.band[1]); }
    var span = Math.max(hi - lo, opts.minSpan || 1), mid = (hi + lo) / 2;
    lo = mid - span / 2 - span * 0.06; hi = mid + span / 2 + span * 0.06;
    var y = function (v) { return (H - P - ((v - lo) / (hi - lo)) * (H - 2 * P)).toFixed(1); };
    if (opts.band) svg += '<rect class="band" x="0" width="' + W + '" y="' + y(opts.band[1]) + '" height="' + Math.max(0, y(opts.band[0]) - y(opts.band[1])).toFixed(1) + '"/>';
    var n = values.length, d = '', pen = false;
    values.forEach(function (v, i) {
      if (typeof v !== 'number') { pen = false; return; }
      var x = (P + (i / Math.max(1, n - 1)) * (W - 2 * P)).toFixed(1);
      d += (pen ? 'L' : 'M') + x + ' ' + y(v);
      pen = true;
    });
    return svg + '<path class="line" d="' + d + '"/></svg>';
  };

  /* ---------- Toasts and modal ---------- */
  var toastTimer = null;
  S.toast = function (text, opts) {
    opts = opts || {};
    var el = document.getElementById('toast');
    el.innerHTML = icon(opts.icon || 'check') + '<span>' + S.esc(text) + '</span>';
    if (opts.level !== undefined) el.setAttribute('data-level', S.lvl(opts.level)); else el.removeAttribute('data-level');
    el.classList.add('is-open');
    clearTimeout(toastTimer);
    toastTimer = setTimeout(function () { el.classList.remove('is-open'); }, opts.ms || 3200);
  };
  var modalClose = null;
  S.modal = function (o) {
    var m = document.getElementById('modal');
    document.getElementById('modal-title').textContent = o.title || '';
    var body = document.getElementById('modal-body');
    var fresh = body.cloneNode(false);
    body.parentNode.replaceChild(fresh, body);
    fresh.innerHTML = o.html || '';
    m.hidden = false;
    modalClose = o.onClose || null;
    if (o.onMount) o.onMount(fresh);
    setTimeout(function () { var f = fresh.querySelector('[autofocus], input, textarea, button'); if (f) f.focus(); }, 30);
  };
  S.closeModal = function () {
    var m = document.getElementById('modal');
    if (m.hidden) return;
    m.hidden = true;
    var fn = modalClose; modalClose = null;
    if (fn) fn();
  };
  /* Confirmation; with o.input a text field is shown and its value resolved */
  S.confirm = function (o) {
    return new Promise(function (resolve) {
      var done = false;
      function finish(v) { if (done) return; done = true; S.closeModal(); resolve(v); }
      S.modal({
        title: o.title,
        html: '<p class="muted">' + S.esc(o.message) + '</p>' +
          (o.input ? '<div class="field"><label class="field__label" for="cf-input">' + S.esc(o.input) + '</label><input class="input" id="cf-input" maxlength="140" value="' + S.esc(o.value || '') + '"></div>' : '') +
          '<div class="st-actions" style="justify-content:flex-end"><button type="button" class="btn btn--secondary" data-a="no">Cancel</button>' +
          '<button type="button" class="btn ' + (o.danger ? 'btn--danger' : 'btn--primary') + '" data-a="yes">' + S.esc(o.confirmLabel || 'Confirm') + '</button></div>',
        onMount: function (b) {
          b.addEventListener('click', function (e) {
            var x = e.target.closest('[data-a]');
            if (!x) return;
            if (x.getAttribute('data-a') !== 'yes') { finish(false); return; }
            var inp = b.querySelector('#cf-input');
            finish(inp ? (inp.value.trim() || o.value || true) : true);
          });
        },
        onClose: function () { finish(false); },
      });
    });
  };

  /* ---------- API ---------- */
  S.api = function (method, path, body) {
    return fetch(path, {
      method: method, headers: body ? { 'Content-Type': 'application/json' } : {},
      body: body ? JSON.stringify(body) : undefined,
    }).then(function (r) {
      return r.json().catch(function () { return {}; }).then(function (j) {
        if (!r.ok || j.ok === false) throw new Error(j.error || ('HTTP ' + r.status));
        return j;
      });
    });
  };
  S.post = function (path, body, okText) {
    return S.api('POST', path, body || {}).then(function (j) {
      if (okText) S.toast(okText);
      return j;
    }, function (err) {
      S.toast(err.message, { icon: 'triangle', level: 2 });
      throw err;
    });
  };

  /* ---------- Crew helpers ---------- */
  S.crew = function (id) { return S.state.crew.filter(function (c) { return c.id === id; })[0] || null; };
  S.who = function (id) { var c = S.crew(id); return c ? c.role + ' · ' + c.name : id; };
  S.openAlertsList = function () {
    return Object.keys(S.state.openAlerts).map(function (k) { return S.state.openAlerts[k]; })
      .sort(function (a, b) { return (S.alertLevel(b) - S.alertLevel(a)) || (b.t - a.t); });
  };
  S.alertFor = function (crewId) {
    return S.openAlertsList().filter(function (a) { return a.crewId === crewId && S.alertLevel(a) >= 2; })[0] || null;
  };

  /* ---------- Trend buffers (15 min, one point every 5 s) ---------- */
  function pushTrend(c) {
    var v = c.vitals;
    if (!v || !v.t) return;
    var tr = S.state.trends[c.id] || (S.state.trends[c.id] = { t: [], hr: [], spo2: [], temp: [], rr: [], ipv: [] });
    var last = tr.t[tr.t.length - 1] || 0;
    if (v.t - last < 5000) return;
    tr.t.push(v.t); tr.hr.push(v.hr); tr.spo2.push(v.spo2); tr.temp.push(v.temp); tr.rr.push(v.rr); tr.ipv.push(c.eval.ipv);
    while (tr.t.length && tr.t[0] < v.t - 15 * 60000) { ['t', 'hr', 'spo2', 'temp', 'rr', 'ipv'].forEach(function (k) { tr[k].shift(); }); }
  }

  /* ---------- Alarm sound (cabin) ---------- */
  var audio = null, soundOn = true, lastBeep = 0;
  try { soundOn = localStorage.getItem('eva.station.sound') !== 'off'; } catch (e) { /* storage blocked */ }
  function unlockAudio() {
    if (audio) return;
    try { var C = window.AudioContext || window.webkitAudioContext; if (C) audio = new C(); } catch (e) { audio = null; }
  }
  function tone(f, t, d) {
    var o = audio.createOscillator(), g = audio.createGain();
    o.frequency.value = f; o.type = 'sine';
    g.gain.setValueAtTime(0.0001, t); g.gain.exponentialRampToValueAtTime(0.18, t + 0.02); g.gain.exponentialRampToValueAtTime(0.0001, t + d);
    o.connect(g); g.connect(audio.destination); o.start(t); o.stop(t + d + 0.02);
  }
  S.beep = function (level) {
    if (!soundOn || !audio) return;
    try {
      if (audio.state === 'suspended') audio.resume();
      var t = audio.currentTime + 0.01;
      if (level >= 3) { tone(988, t, 0.18); tone(740, t + 0.24, 0.22); } else tone(880, t, 0.16);
    } catch (e) { /* no audio */ }
  };
  function alarmLoop() {
    var crit = S.state.crew.some(function (c) { return c.eva && c.eval && c.eval.level === 3; });
    var pending = S.openAlertsList().some(function (a) { return S.alertLevel(a) >= 3 && a.response === 'PENDING'; });
    if ((crit && pending) || S.state.crew.some(function (c) { return c.help; })) {
      if (S.now() - lastBeep > 2500) { lastBeep = S.now(); S.beep(3); }
    }
  }

  /* ---------- Shell rendering ---------- */
  function renderTop() {
    var m = S.state.mission;
    var el = document.getElementById('top-mission');
    if (!m) { el.innerHTML = '<span class="muted">No mission planned</span>'; return; }
    var tone = { ACTIVE: 'accent', PLANNED: '', COMPLETED: '', ABORTED: '' }[m.status] || '';
    var lvl = m.status === 'ABORTED' ? ' data-level="3"' : '';
    var clock = m.status === 'ACTIVE' && m.startedAt ? 'T+' + S.clock(S.now() - m.startedAt)
      : m.status === 'PLANNED' ? 'Not started' : (m.startedAt && m.endedAt ? 'Duration ' + S.clock(m.endedAt - m.startedAt) : '');
    el.innerHTML = '<span class="chip" data-tone="' + tone + '"' + lvl + '>' + S.esc(m.status) + '</span>' +
      '<span class="st-mission__name">' + S.esc(m.name) + '</span>' +
      '<span class="st-mission__clock" id="top-clock">' + S.esc(clock) + '</span>';
  }
  function tickClock() {
    var m = S.state.mission, el = document.getElementById('top-clock');
    if (el && m && m.status === 'ACTIVE' && m.startedAt) el.textContent = 'T+' + S.clock(S.now() - m.startedAt);
  }
  function renderLink() {
    var el = document.getElementById('top-link');
    var phones = S.state.crew.filter(function (c) { return c.device && c.device.online; }).length;
    el.setAttribute('data-state', S.state.connected ? 'on' : 'off');
    document.getElementById('top-link-text').textContent = S.state.connected
      ? 'Server linked · ' + phones + (phones === 1 ? ' phone' : ' phones')
      : 'Server offline — reconnecting…';
  }
  function renderBadges() {
    var open = S.openAlertsList();
    var crit = open.filter(function (a) { return S.alertLevel(a) >= 2; });
    var b = document.getElementById('badge-console');
    b.hidden = !crit.length;
    b.textContent = crit.length;
    b.setAttribute('data-level', crit.some(function (a) { return S.alertLevel(a) === 3; }) ? '3' : '2');
    var t = document.getElementById('badge-triage');
    var worst = S.state.crew.reduce(function (mx, c) { return c.eva && c.eval && c.eval.level > mx ? c.eval.level : mx; }, -1);
    t.hidden = worst < 2;
    t.textContent = '!';
    t.setAttribute('data-level', String(Math.max(worst, 2)));
  }
  function renderFoot() {
    var i = S.state.info || {};
    document.getElementById('side-foot').innerHTML =
      '<span>EVA Station ' + S.esc(i.version || '') + '</span>' +
      '<span class="mono">' + S.esc(i.host || '') + '</span>' +
      '<span>Up since ' + (S.state.startedAt ? S.hm(S.state.startedAt) : '—') + '</span>';
  }
  /* Emergency banner on every screen */
  function renderAlarm() {
    var slot = document.getElementById('alarm-slot');
    var worst = null;
    S.state.crew.forEach(function (c) {
      if (c.eva && c.eval && c.eval.level === 3 && (!worst || c.eval.ipv > worst.eval.ipv)) worst = c;
    });
    if (!worst) { slot.innerHTML = ''; return; }
    var a = S.alertFor(worst.id);
    slot.innerHTML = '<div class="st-alarm" data-level="3">' + icon('octagon') +
      '<div class="st-alarm__text"><span class="eyebrow st-alarm__who">Emergency · ' + S.esc(worst.role + ' ' + worst.name) + ' · IPV ' + worst.eval.ipv + '</span>' +
      '<span class="st-alarm__what">' + S.esc(worst.eval.diagnosis) + '</span></div>' +
      (a && a.response === 'PENDING' ? '<button type="button" class="btn btn--primary btn--sm" data-ack="' + a.id + '">' + icon('check') + 'Acknowledge</button>' : '') +
      '<a class="btn btn--secondary btn--sm" href="#console" data-focus-crew="' + S.esc(worst.id) + '">Open protocol' + icon('chevronRight') + '</a></div>';
  }
  S.renderShell = function () { renderTop(); renderLink(); renderBadges(); renderAlarm(); renderFoot(); };

  /* ---------- Router ---------- */
  var VIEWS = ['triage', 'premission', 'console', 'crew', 'reports', 'blackbox', 'system', 'simulator'];
  S.current = null;
  S.focusCrew = null;
  function route() {
    var v = (location.hash || '').replace('#', '');
    if (VIEWS.indexOf(v) < 0) v = 'triage';
    var prev = S.views[S.current];
    if (prev && prev.destroy) prev.destroy();
    S.current = v;
    document.querySelectorAll('.st-nav').forEach(function (a) {
      if (a.getAttribute('data-view') === v) a.setAttribute('aria-current', 'page'); else a.removeAttribute('aria-current');
    });
    var root = document.getElementById('view');
    root.style.animation = 'none'; void root.offsetWidth; root.style.animation = '';
    var view = S.views[v];
    try { view.render(root); } catch (e) { console.error(e); root.innerHTML = '<div class="empty">' + icon('triangle') + '<p>This view failed to render. See the console.</p></div>'; }
    window.scrollTo(0, 0);
  }
  S.refreshView = function (kind) {
    var view = S.views[S.current];
    if (!view) return;
    try { if (view.update) view.update(kind); else view.render(document.getElementById('view')); } catch (e) { console.error(e); }
  };

  /* ---------- Live link ---------- */
  var ws = null, retry = 1000;
  function upsertAlert(a) {
    if (a.resolvedAt) delete S.state.openAlerts[a.id]; else S.state.openAlerts[a.id] = a;
    var i = S.state.alerts.findIndex(function (x) { return x.id === a.id; });
    if (i >= 0) S.state.alerts[i] = a; else S.state.alerts.unshift(a);
  }
  function onMessage(msg) {
    var st = S.state;
    switch (msg.type) {
      case 'snapshot':
        S.offset = msg.t - Date.now();
        st.mission = msg.mission; st.missions = msg.missions || []; st.crew = msg.crew || []; st.trends = msg.trends || {};
        st.alerts = msg.alerts || []; st.incidents = msg.incidents || []; st.oplog = msg.oplog || []; st.sim = msg.sim || null;
        st.info = msg.info || null; st.startedAt = msg.startedAt;
        st.openAlerts = {};
        (msg.openAlerts || []).forEach(function (a) { st.openAlerts[a.id] = a; });
        S.renderShell(); route();
        return;
      case 'tick':
        S.offset = msg.t - Date.now();
        st.mission = msg.mission; st.crew = msg.crew || [];
        st.crew.forEach(pushTrend);
        S.renderShell(); alarmLoop(); S.refreshView('tick');
        return;
      case 'alert':
        upsertAlert(msg.alert);
        var lv = S.alertLevel(msg.alert);
        if (lv >= 2) {
          S.beep(lv);
          S.toast((lv >= 3 ? 'EMERGENCY · ' : 'Risk · ') + S.who(msg.alert.crewId) + ': ' + msg.alert.reason, { icon: lv >= 3 ? 'octagon' : 'triangle', level: lv, ms: 6000 });
        }
        S.renderShell(); S.refreshView('alert');
        return;
      case 'alert-update':
        upsertAlert(msg.alert); S.renderShell(); S.refreshView('alert'); return;
      case 'incident':
        var i = st.incidents.findIndex(function (x) { return x.id === msg.incident.id; });
        if (i >= 0) st.incidents[i] = msg.incident; else st.incidents.unshift(msg.incident);
        S.refreshView('incident'); return;
      case 'oplog':
        st.oplog.unshift(msg.entry); if (st.oplog.length > 200) st.oplog.pop();
        S.refreshView('oplog'); return;
      case 'devices':
        if (S.loadDevices) S.loadDevices(); return;
      case 'mission':
        st.mission = msg.mission;
        var k = st.missions.findIndex(function (x) { return x.id === msg.mission.id; });
        if (k >= 0) st.missions[k] = msg.mission; else st.missions.unshift(msg.mission);
        S.renderShell(); S.refreshView('mission'); return;
      case 'readiness':
        var c = S.crew(msg.crewId); if (c) c.readiness = msg.readiness;
        S.toast('Readiness received · ' + S.who(msg.crewId) + ': ' + msg.readiness.status, { icon: 'clipboard' });
        S.refreshView('readiness'); return;
      case 'sim':
        st.sim = msg.sim; S.refreshView('sim'); return;
    }
  }
  function connect() {
    var url = (location.protocol === 'https:' ? 'wss://' : 'ws://') + location.host + '/ws/console';
    try { ws = new WebSocket(url); } catch (e) { setTimeout(connect, retry); return; }
    ws.onopen = function () { S.state.connected = true; retry = 1000; renderLink(); };
    ws.onmessage = function (ev) { try { onMessage(JSON.parse(ev.data)); } catch (e) { console.error(e); } };
    ws.onclose = function () {
      S.state.connected = false; renderLink();
      setTimeout(connect, retry); retry = Math.min(retry * 2, 5000);
    };
    ws.onerror = function () { try { ws.close(); } catch (e) { /* nothing */ } };
  }

  /* ---------- Theme and sound buttons ---------- */
  function applyTheme(t) {
    document.documentElement.setAttribute('data-theme', t);
    var b = document.getElementById('btn-theme');
    b.innerHTML = icon(t === 'dark' ? 'sun' : 'moon');
    b.setAttribute('aria-label', t === 'dark' ? 'Switch to light mode' : 'Switch to dark cabin mode');
    try { localStorage.setItem('eva.station.theme', t); } catch (e) { /* storage blocked */ }
  }
  function applySound() {
    var b = document.getElementById('btn-sound');
    b.setAttribute('aria-pressed', String(soundOn));
    b.setAttribute('aria-label', soundOn ? 'Mute the cabin alarm' : 'Unmute the cabin alarm');
    b.innerHTML = icon(soundOn ? 'bell' : 'volumeOff');
    try { localStorage.setItem('eva.station.sound', soundOn ? 'on' : 'off'); } catch (e) { /* storage blocked */ }
  }

  S.showConnect = function () {
    var urls = (S.state.info && S.state.info.urls) || [];
    S.modal({
      title: 'Connect an explorer’s phone',
      html: '<p class="muted">1 · Connect the phone to this PC’s Wi-Fi hotspot (no internet needed).<br>2 · Open one of these addresses in Chrome or Safari:</p>' +
        '<div class="urls">' + (urls.length ? urls.map(function (u) { return '<span class="url">' + S.esc(u) + '/app/</span>'; }).join('') : '<span class="url">http://&lt;PC-IP&gt;:' + S.esc((S.state.info || {}).port || 8080) + '/app/</span>') + '</div>' +
        '<p class="muted">3 · In the app: Settings → Station link → choose who wears the phone → Link.</p>' +
        '<p class="subtle">Windows may ask to allow EVA Station through the firewall the first time: allow it on private networks.</p>',
    });
  };

  /* ---------- Boot ---------- */
  function hydrate(root) {
    root.querySelectorAll('[data-icon]').forEach(function (el) { el.outerHTML = icon(el.getAttribute('data-icon')); });
  }
  document.addEventListener('DOMContentLoaded', function () {
    hydrate(document);
    var theme = 'light';
    try { theme = localStorage.getItem('eva.station.theme') || 'light'; } catch (e) { /* storage blocked */ }
    applyTheme(theme);
    applySound();
    document.getElementById('btn-theme').addEventListener('click', function () {
      applyTheme(document.documentElement.getAttribute('data-theme') === 'dark' ? 'light' : 'dark');
    });
    document.getElementById('btn-sound').addEventListener('click', function () { unlockAudio(); soundOn = !soundOn; applySound(); });
    document.getElementById('btn-connect').addEventListener('click', S.showConnect);
    document.addEventListener('click', function (e) {
      unlockAudio(); // browsers only allow sound after a click
      if (e.target.closest('[data-close-modal]')) { S.closeModal(); return; }
      var ack = e.target.closest('[data-ack]');
      if (ack) { S.post('/api/alerts/' + ack.getAttribute('data-ack') + '/ack', { response: 'COPIED' }, 'Alert acknowledged by the operator'); return; }
      var fc = e.target.closest('[data-focus-crew]');
      if (fc) S.focusCrew = fc.getAttribute('data-focus-crew');
    });
    document.addEventListener('keydown', function (e) { if (e.key === 'Escape') S.closeModal(); });
    window.addEventListener('hashchange', route);
    setInterval(tickClock, 1000);
    S.renderShell();
    document.getElementById('view').innerHTML = '<div class="empty">' + icon('signal') + '<p>Connecting to EVA Station server…</p></div>';
    connect();
  });
})();
