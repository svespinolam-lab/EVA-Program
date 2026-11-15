/* EVA Station link. Only explicit telemetry/readiness fields cross the wire.
 * isLinked means enabled (including outages), so remote suits never become local simulations.
 * Compact outbox rows: crewId, seq, t, then FIELDS in order. */
(function () {
  'use strict';
  var App = window.App;
  var FIELDS = ['hr', 'spo2', 'temp', 'rr', 'hrv', 'rad', 'effort', 'suitP', 'o2', 'co2', 'batt', 'bpSys', 'bpDia'];
  var served = /^https?:$/.test(location.protocol) && location.pathname.indexOf('/app') === 0;
  var socket = null, retry = null, batchTimer = null, delay = 1000, initialized = false;
  var state = 'off', lastAck = 0, replay = [], inFlight = null, sent = new Set();
  var monthlyOutbox = App.storage.get('monthlyOutbox', []);
  if (!Array.isArray(monthlyOutbox)) monthlyOutbox = [];
  var monthlyInFlight = null;
  var alerts = {}, seen = {}, abortTimer = null, abortKey = null;
  var esc = App.dom.esc;
  var saved = App.storage.get('outbox', []);
  var outbox = (Array.isArray(saved) ? saved : []).map(function (row) {
    if (!Array.isArray(row)) return row;
    var item = { type: 'telemetry', crewId: row[0], seq: row[1], t: row[2] };
    FIELDS.forEach(function (k, i) { if (Number.isFinite(row[i + 3])) item[k] = row[i + 3]; });
    item.source = row[16] || 'virtual';
    return item;
  }).filter(function (item) { return item && Number.isSafeInteger(item.seq) && item.seq > 0 && Number.isFinite(item.t) && typeof item.crewId === 'string'; }).slice(-20000);
  var seq = Math.max(Number(App.storage.get('linkSeq', 0)) || 0, outbox.reduce(function (n, item) { return Math.max(n, item.seq); }, 0));

  function host() { return served ? location.host : (App.store.get('stationHost') || ''); }
  function crewId() { return App.store.get('linkCrewId') || 'AST-001'; }
  function enabled() { return !!App.store.get('linkEnabled'); }
  function toast(text) { if (App.ui) App.ui.toast(text, { icon: 'link' }); }
  function say(text, priority) { if (App.voice) App.voice.say(text, { priority: priority }); }
  function log(type, msg, level, data) {
    if (App.blackbox) App.blackbox.log({ crewId: crewId(), type: type, msg: msg, level: level, data: data });
  }
  function persist() {
    App.storage.set('outbox', outbox.map(function (item) {
      return [item.crewId, item.seq, item.t].concat(FIELDS.map(function (k) { return item[k] == null ? null : item[k]; }), item.source || 'virtual');
    }));
  }
  function persistMonthly() { App.storage.set('monthlyOutbox', monthlyOutbox); }
  function flushMonthly() {
    if (state !== 'online' || monthlyInFlight || !monthlyOutbox.length) return;
    var item = monthlyOutbox[0];
    if (send(Object.assign({ type: 'monthly-health' }, item))) monthlyInFlight = item.month;
  }
  function queueMonthlyHealth(record) {
    if (!record || !/^\\d{4}-\\d{2}$/.test(record.month)) throw new Error('Monthly panel requires YYYY-MM');
    var clean = { month: record.month };
    ['hemoglobin','wbc','platelets','glucose','orthostaticDrop','weightKg','radiationMonth'].forEach(function(k) {
      if (Number.isFinite(record[k])) clean[k] = record[k];
    });
    if (Object.keys(clean).length === 1) throw new Error('Enter at least one monthly clinical value first');
    var i = monthlyOutbox.findIndex(function(x){return x.month===clean.month;});
    if (i >= 0) monthlyOutbox[i] = Object.assign({}, monthlyOutbox[i], clean); else monthlyOutbox.push(clean);
    persistMonthly(); flushMonthly(); changed(); return true;
  }
  function status() { return { state: state, host: host(), crewId: crewId(), queued: outbox.length, lastAck: lastAck }; }
  function changed() {
    var chip = document.getElementById('station-chip');
    if (chip) {
      chip.dataset.state = state;
      chip.textContent = state === 'online' ? '● Station' : state === 'off' ? '○ Station off' :
        (state === 'connecting' ? '◌ Connecting' : '○ Offline') + (outbox.length ? ' · ' + outbox.length + ' queued' : '');
    }
    App.bus.emit('link', { status: status() });
  }
  function send(message) {
    if (!socket || socket.readyState !== 1) return false;
    try { socket.send(JSON.stringify(message)); return true; }
    catch (err) { socket.close(); return false; }
  }
  function queueCount() { send({ type: 'queue', n: outbox.length }); }
  function prune(ack) {
    // A higher live seq must never discard an older, not-yet-sent replay item.
    outbox = outbox.filter(function (item) {
      return item.crewId !== crewId() || item.seq > ack || !sent.has(item.seq);
    });
    sent.forEach(function (n) { if (n <= ack) sent.delete(n); });
    persist(); changed();
  }
  function nextBatch() {
    if (inFlight || !replay.length || state !== 'online') return;
    var items = replay.splice(0, 300);
    inFlight = items;
    if (!send({ type: 'batch', items: items })) { replay = items.concat(replay); inFlight = null; return; }
    items.forEach(function (item) { sent.add(item.seq); });
    clearTimeout(batchTimer);
    batchTimer = setTimeout(function () { if (socket) socket.close(); }, 15000);
  }
  function close() {
    clearTimeout(retry); clearTimeout(batchTimer); retry = batchTimer = null;
    var old = socket; socket = null;
    if (old) { old.onopen = old.onmessage = old.onclose = old.onerror = null; old.close(); }
    replay = []; inFlight = null; sent.clear();
  }
  function schedule() {
    if (!enabled()) return;
    state = 'offline'; changed();
    clearTimeout(retry);
    retry = setTimeout(connect, delay);
    delay = Math.min(10000, delay * 2);
  }
  function deviceLabel() {
    var ua = navigator.userAgent || '';
    var os = /Android/i.test(ua) ? 'Android' : /iPhone|iPad/i.test(ua) ? 'iOS' : /Windows/i.test(ua) ? 'Windows' : 'Phone';
    var browser = /Edg/i.test(ua) ? 'Edge' : /Chrome|CriOS/i.test(ua) ? 'Chrome' : /Firefox|FxiOS/i.test(ua) ? 'Firefox' : /Safari/i.test(ua) ? 'Safari' : 'Browser';
    return os + ' · ' + browser;
  }
  function connect() {
    close();
    if (!enabled()) { state = 'off'; changed(); return; }
    if (!host() || !window.WebSocket) { schedule(); return; }
    state = 'connecting'; changed();
    var ws;
    try { ws = socket = new WebSocket((location.protocol === 'https:' ? 'wss://' : 'ws://') + host() + '/ws/device'); }
    catch (err) { schedule(); return; }
    ws.onopen = function () {
      if (ws !== socket) return;
      send({ type: 'hello', crewId: crewId(), device: deviceLabel() });
    };
    ws.onmessage = function (event) {
      if (ws !== socket) return;
      try { receive(JSON.parse(event.data)); }
      catch (err) { console.error('[EVA] station message', err); }
    };
    ws.onerror = function () { if (ws === socket) ws.close(); };
    ws.onclose = function () {
      if (ws !== socket) return;
      socket = null; clearTimeout(batchTimer); inFlight = null; sent.clear();
      schedule();
    };
  }
  function restart() {
    lastAck = 0; delay = 1000; alerts = {}; renderAlerts(); connect();
  }
  function mission(value) {
    if (!value) return;
    var patch = { stationMission: value };
    var start = typeof value.startedAt === 'number' ? value.startedAt : Date.parse(value.startedAt);
    if (value.status === 'ACTIVE' && Number.isFinite(start)) patch.evaStart = start;
    App.store.set(patch);
  }
  function crew(items) {
    if (!Array.isArray(items)) return;
    var roles = Object.assign({}, App.store.get('roles')), dirty = false;
    items.forEach(function (m) {
      if (App.crew.byId(m.id) && /^EV\d+$|^IV$/.test(m.role) && roles[m.id] !== m.role) { roles[m.id] = m.role; dirty = true; }
    });
    if (dirty) App.store.set({ roles: roles });
    items.forEach(function (m) { if (m.id !== crewId() && m.vitals) App.telemetry.ingestRemote(m.id, m.vitals); });
  }
  function renderAlerts() {
    var slot = document.getElementById('station-acks');
    if (!slot) return;
    var active = document.activeElement;
    var focusedId = active && active.dataset.alertId, focusedResponse = active && active.dataset.response;
    slot.innerHTML = Object.keys(alerts).map(function (id) {
      var a = alerts[id];
      return '<section class="station-ack" data-level="' + (a.level === 'EMERGENCY' ? 3 : 2) + '">' +
        '<strong>' + esc(a.level + ' · EVA Station') + '</strong><p>' + esc(a.reason) + '</p><p>' + esc(a.voice) + '</p>' +
        '<div class="station-ack__buttons">' + ['COPIED', 'IM_OK', 'NEED_HELP'].map(function (response, i) {
          return '<button type="button" class="btn btn--secondary" data-alert-id="' + esc(id) + '" data-response="' + response + '">' + ['Copy', "I’m OK", 'Need help'][i] + '</button>';
        }).join('') + '</div></section>';
    }).join('');
    document.body.classList.toggle('has-station-acks', !!Object.keys(alerts).length);
    if (focusedId) Array.prototype.some.call(slot.querySelectorAll('button'), function (b) {
      if (b.dataset.alertId === focusedId && b.dataset.response === focusedResponse) { b.focus(); return true; }
      return false;
    });
  }
  function alert(a) {
    if (!a || a.crewId !== crewId()) return;
    if (a.resolvedAt || a.response !== 'PENDING' || !/^(RISK|EMERGENCY)$/.test(a.level)) {
      delete alerts[a.id]; renderAlerts(); return;
    }
    alerts[a.id] = a; renderAlerts();
    var signature = a.id + ':' + a.level;
    if (seen[signature]) return;
    seen[signature] = true;
    var risk = App.risk && App.risk.get(crewId());
    var duplicate = risk && risk.findings.some(function (f) { return f.key === a.metric && f.level >= 2; });
    if (!duplicate) say(a.voice, a.level === 'EMERGENCY' ? 3 : 2);
    log('alert', a.reason, a.level === 'EMERGENCY' ? 3 : 2, a);
  }
  function banner(title, text, critical) {
    var slot = document.getElementById('station-banners');
    var box = document.createElement('section');
    box.className = 'station-banner'; box.setAttribute('role', 'alert');
    if (critical) box.dataset.level = '3';
    box.innerHTML = '<strong>' + esc(title) + '</strong><p>' + esc(text) + '</p><button type="button" class="btn btn--secondary">Dismiss</button>';
    box.querySelector('button').onclick = function () { box.remove(); };
    slot.appendChild(box);
  }
  function abort(message) {
    var key = String(message.missionId) + ':' + message.reason;
    if (abortKey === key) return;
    abortKey = key;
    clearInterval(abortTimer);
    var overlay = document.getElementById('station-abort');
    overlay.hidden = false;
    overlay.innerHTML = '<div><h2 id="station-abort-title">EVA ABORTED · Return to the airlock now</h2><p>' + esc(message.reason) +
      '</p><button type="button" class="btn btn--primary">Copy</button></div>';
    var previousFocus = document.activeElement;
    var copy = overlay.querySelector('button');
    copy.onclick = function () {
      clearInterval(abortTimer); abortTimer = null; overlay.hidden = true;
      if (previousFocus && previousFocus.isConnected) previousFocus.focus();
    };
    overlay.onkeydown = function (e) { if (e.key === 'Tab') { e.preventDefault(); copy.focus(); } };
    copy.focus();
    var voice = message.voice || 'EVA aborted. Return to the airlock now. ' + message.reason;
    say(voice, 3); abortTimer = setInterval(function () { say(voice, 3); }, 20000);
    log('alert', 'EVA ABORTED: ' + message.reason, 3, message);
  }
  function receive(message) {
    switch (message.type) {
      case 'welcome':
        state = 'online'; delay = 1000;
        mission(message.mission); crew(message.crew);
        alerts = {};
        (message.alerts || []).forEach(alert); renderAlerts();
        replay = outbox.filter(function (item) { return item.crewId === crewId(); });
        changed(); api.sendReadiness(); queueCount(); nextBatch(); flushMonthly(); break;
      case 'ack-seq':
        if (Number.isSafeInteger(message.seq) && message.seq >= 0) {
          lastAck = Math.max(lastAck, message.seq); prune(message.seq);
        }
        break;
      case 'monthly-health-ok':
        monthlyOutbox = monthlyOutbox.filter(function(x){return x.month !== message.month;}); persistMonthly(); monthlyInFlight = null; flushMonthly(); toast('Monthly clinical panel synced'); break;
      case 'batch-ok':
        if (!inFlight) break;
        clearTimeout(batchTimer);
        if (message.n !== inFlight.length) { toast('Station could not accept the full batch; queued readings retained.'); if (socket) socket.close(); break; }
        inFlight = null; nextBatch(); break;
      case 'mission': mission(message.mission); break;
      case 'crew': mission(message.mission); crew(message.crew); break;
      case 'alert': case 'alert-update': alert(message.alert); break;
      case 'buddy':
        banner(message.role + ' ' + message.name + ' needs assistance', message.reason, true);
        say(message.voice, 3); log('alert', message.name + ' needs assistance: ' + message.reason, 3, message); break;
      case 'instruction':
        banner('From EVA Station', message.text, false); say(message.text, 2); log('system', message.text, 1, message); break;
      case 'abort': abort(message); break;
      case 'inject': App.telemetry.inject(crewId(), message.scenario); break;
      case 'clear': App.telemetry.clearScenarios(crewId()); break;
      case 'replaced': toast(message.reason || 'This phone was replaced'); api.setEnabled(false); break;
      case 'error': monthlyInFlight = null; toast(message.error || 'Station error'); break;
      // pong and readiness-ok need no state change.
    }
  }
  function localDay(time) {
    var d = new Date(time);
    return d.getFullYear() + '-' + String(d.getMonth() + 1).padStart(2, '0') + '-' + String(d.getDate()).padStart(2, '0');
  }
  function saveDaily(id, checkin) {
    if (!checkin || checkin.example) return;
    var all = Object.assign({}, App.store.get('dailyLog') || {});
    var days = Object.assign({}, all[id] || {});
    days[localDay(checkin.at || Date.now())] = {
      mood: checkin.mood, energy: checkin.energy, sleepMin: checkin.sleepMin,
      hydration: checkin.hydration || 3,
    };
    var cutoff = new Date(); cutoff.setDate(cutoff.getDate() - 29);
    var oldest = localDay(cutoff);
    Object.keys(days).forEach(function (day) { if (day < oldest) delete days[day]; });
    all[id] = days; App.store.set({ dailyLog: all });
  }
  function readiness() {
    var today = localDay(Date.now()), history = (App.store.get('dailyLog') || {})[crewId()] || {};
    var ck = history[today];
    if (!ck) {
      var current = (App.store.get('checkins') || {})[crewId()];
      if (!current || current.example || localDay(current.at) !== today) return null;
      saveDaily(crewId(), current); ck = current;
    }
    var vulnerability = 0, factors = [], reasons = [];
    function add(points, code, reason) { vulnerability += points; factors.push(code); if (reason) reasons.push(reason); }
    if (ck.sleepMin < 360) add(25, 'SLEEP_SHORT', 'short sleep');
    var threeShort = [0, 1, 2].every(function (offset) {
      var date = new Date(); date.setDate(date.getDate() - offset);
      var day = history[localDay(date)] || (offset === 0 ? ck : null);
      return day && Number.isFinite(day.sleepMin) && day.sleepMin < 360;
    });
    if (threeShort) add(0, 'SLEEP_LT6_3D');
    var fatigue = ck.energy === 0 || ck.energy === 'Low';
    if (fatigue) add(15, 'FATIGUE_HIGH', 'high fatigue');
    var hydration = Math.max(1, Math.min(8, Number(ck.hydration) || 3));
    if (hydration >= 6) add(15, 'HYDRATION_LOW', 'low hydration');
    if (ck.mood === 0 || ck.mood === 'Low') add(20, 'BEHAVIORAL_LOAD', 'behavioral load');
    var verdict = vulnerability >= 60 ? 'RED' : vulnerability >= 30 ? 'YELLOW' : 'GREEN';
    return { type: 'readiness', day: today, status: verdict, vulnerability: vulnerability,
      summary: verdict === 'GREEN' ? 'Ready for EVA: baseline parameters optimal.' : verdict === 'RED' ?
        'Not recommended for EVA: private medical review required.' : 'With reservations: ' + reasons.join(', '),
      factors: factors, sleepH: ck.sleepMin / 60, hydration: hydration, fatigueHigh: fatigue };
  }
  function telemetry(event) {
    if (!enabled() || !event || event.remote || event.crewId !== crewId() || !App.crew.isEva(crewId()) || !event.data) return;
    var item = { type: 'telemetry', crewId: crewId(), seq: ++seq, t: Date.now(), source: event.data.source || 'virtual' };
    App.storage.set('linkSeq', seq);
    FIELDS.forEach(function (k) { if (Number.isFinite(event.data[k])) item[k] = event.data[k]; });
    outbox.push(item);
    if (outbox.length > 20000) outbox.splice(0, outbox.length - 20000);
    persist();
    if (state === 'online' && send(item)) sent.add(item.seq);
    changed();
  }
  function openSheet() {
    var members = App.crew.all(), unsubscribe;
    App.ui.sheet.open({ title: 'Station link',
      html: '<div class="cfg-sheet station-settings"><div class="field"><span class="field__label">This phone belongs to</span>' +
        App.ui.segmented({ name: 'station-crew', label: 'This phone belongs to', options: members.map(function (m) { return m.role + ' · ' + m.name; }), value: members.findIndex(function (m) { return m.id === crewId(); }) }) + '</div>' +
        '<label class="field">Station address<input id="station-host" type="text" placeholder="192.168.137.1:8080" autocomplete="off" autocapitalize="off" spellcheck="false" value="' + esc(host()) + '"' + (served ? ' readonly' : '') + '></label>' +
        '<div class="cfg-row"><span id="station-switch-label">Link</span><button type="button" id="station-switch" class="switch" role="switch" aria-labelledby="station-switch-label" aria-checked="' + enabled() + '"></button></div>' +
        '<p id="station-status" role="status"></p><button type="button" class="btn btn--primary" id="station-sync">Sync now</button>' +
        '<p class="muted">Suit readings and readiness summaries sync to the station. Your mood, energy and notes stay on this phone.</p></div>',
      onMount: function (body) {
        function refresh() {
          var s = status();
          body.querySelector('#station-status').textContent = s.state + ' · ' + s.queued + ' queued · last ACK ' + s.lastAck;
          body.querySelector('#station-switch').setAttribute('aria-checked', String(enabled()));
        }
        refresh(); unsubscribe = App.bus.on('link', refresh);
        body.querySelector('#station-host').onchange = function (event) { try { api.setHost(event.target.value); } catch (err) { toast(err.message); } };
        body.querySelector('#station-switch').onclick = function () { api.setEnabled(!enabled()); };
        body.querySelector('#station-sync').onclick = function () {
          if (!enabled()) { toast('Enable the station link to sync'); return; }
          if (state === 'online') { api.sendReadiness(); queueCount(); nextBatch(); } else connect();
        };
        body.querySelector('[data-seg="station-crew"]').parentElement.addEventListener('click', function (e) {
          var b = e.target.closest('[data-seg]');
          if (!b) return;
          api.setCrew(members[Number(b.dataset.value)].id);
          Array.prototype.forEach.call(body.querySelectorAll('[data-seg]'), function (el) { el.setAttribute('aria-checked', String(el === b)); });
        });
      },
      onClose: function () { if (unsubscribe) unsubscribe(); },
    });
  }
  var api = App.link = {
    init: function () {
      if (initialized) return; initialized = true;
      document.getElementById('station-chip').onclick = openSheet;
      document.getElementById('station-acks').onclick = function (e) {
        var button = e.target.closest('[data-alert-id]');
        if (button) api.ack(button.dataset.alertId, button.dataset.response);
      };
      App.bus.on('telemetry', telemetry);
      App.bus.on('checkin', function (event) {
        if (!event || !event.checkin) return;
        saveDaily(event.crewId, event.checkin);
        if (event.crewId === crewId()) api.sendReadiness();
      });
      App.bus.on('state', function (event) {
        if (['linkEnabled', 'linkCrewId', 'stationHost'].some(function (k) { return k in event.patch; })) restart();
      });
      setInterval(queueCount, 10000);
      window.addEventListener('online', function () { if (enabled() && state !== 'online') connect(); });
      connect();
    },
    isLinked: enabled,
    status: status,
    deviceCrewId: crewId,
    setEnabled: function (value) { App.store.set({ linkEnabled: !!value }); },
    setCrew: function (id) {
      if (!App.crew.byId(id)) throw new Error('Unknown crew member');
      App.store.set({ linkCrewId: id });
    },
    setHost: function (value) {
      if (served) return;
      var h = String(value || '').trim();
      if (h && (!/^(?:[a-z\d.-]+|\[[a-f\d:]+\])(?::\d{1,5})?$/i.test(h))) throw new Error('Enter a station host and optional port, without a URL path');
      App.store.set({ stationHost: h });
    },
    ack: function (id, response) {
      if (!/^(COPIED|IM_OK|NEED_HELP)$/.test(response) || !alerts[id]) return false;
      if (!send({ type: 'ack', alertId: alerts[id].id, response: response })) { toast('Station offline. Reconnect to send your response.'); return false; }
      // Keep the bar until the station confirms with alert-update.
      toast('Response sent'); return true;
    },
    reportIncident: function () {
      if (!enabled() || !send({ type: 'incident' })) { toast('Station offline. Reconnect to report the incident.'); return false; }
      log('system', 'Incident reported to EVA Station', 1); toast('Incident reported'); return true;
    },
    sendReadiness: function () { var message = readiness(); return !!(enabled() && state === 'online' && message && send(message)); },
    sendMonthlyHealth: queueMonthlyHealth,
    openSheet: openSheet,
  };
})();
