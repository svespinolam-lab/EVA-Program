/* EVA · Log for the selected crew member: mental check-in (mood, energy, sleep); live HR, SpO₂, temperature, and on-demand blood pressure; suit pressure, oxygen, CO₂, and battery. IV crew see only mental health. Styles: css/views/log.css (.reg-). */
(function () {
  'use strict';
  var App = window.App = window.App || {};
  App.views = App.views || {};

  var C = App.config;
  var BIO_LIVE = ['hr', 'spo2', 'temp'];            // Continuous biomedical sensors
  var SUIT = C.groups.suit.metrics;                 // Pressure, O₂, CO₂, battery
  var SPARK_SPAN = { hr: 12, spo2: 3, temp: 0.6 };  // Minimum sparkline ranges

  var root = null;      // Main element containing the view
  var crewId = null;    // Displayed crew member
  var draft = null;     // Unsaved mental check-in: {mood, energy, sleepMin}
  var pending = {};     // Connections or measurements started from this view
  var lastBpAt = null;  // Timestamp of the last rendered blood pressure reading
  var agoTimer = null;

  /* Utilities */
  function esc(s) { return App.dom.esc(s); }
  function $(sel) { return root ? App.dom.$(sel, root) : null; }
  function T() { return App.telemetry || null; }
  function hhmm(ts) { return App.fmt.time(ts).slice(0, 5); }
  function cap(s) { return s ? s.charAt(0).toUpperCase() + s.slice(1) : ''; }
  function member() { return App.crew.byId(crewId); }

  function statusOf(key) {
    if (pending[key]) return key === 'bp' ? 'measuring' : 'connecting';
    var t = T();
    var fallback = key === 'bp' ? 'idle' : 'disconnected';
    return t && t.status ? (t.status(crewId, key) || fallback) : fallback;
  }
  function isOff(key) { return statusOf(key) === 'disconnected'; }
  function latest() {
    var t = T();
    return t && t.latest ? t.latest(crewId) : null;
  }
  function history(key) {
    var t = T();
    return t && t.history ? (t.history(crewId, key) || []) : [];
  }
  function wasLost(key) {
    var t = T();
    return !!(t && t.lost && t.lost(crewId).indexOf(key) >= 0);
  }
  function rawValue(key, data) {
    if (!data) return null;
    var v = key === 'bp' ? data.bpSys : data[key];
    return typeof v === 'number' && !isNaN(v) ? v : null;
  }

  /* Examples: Normal / OK; Elevated / Caution; Low / Critical */
  function stateText(key, level, value, short) {
    if (level === null || level === undefined) return 'No reading';
    if (level === 0) return short ? 'Normal' : 'Normal · OK';
    var m = C.metrics[key];
    var dir = App.levels.direction(key, value);
    var word = dir === 'low' ? m.low : dir === 'high' ? m.high : '';
    return (word ? cap(word) + ' · ' : '') + App.levels.meta(level).label;
  }

  function baselineText(key) {
    var b = (member() || {}).baseline || {};
    var m = C.metrics[key];
    if (key === 'bp') return b.bpSys ? 'Baseline ' + b.bpSys + '/' + b.bpDia + ' ' + m.unit : '';
    if (b[key] === undefined) return '';
    return 'Baseline ' + App.fmt.num(b[key], m.decimals) + ' ' + m.unit;
  }

  function valueHTML(key, data, medium) {
    return '<p class="value' + (medium ? ' value--md' : '') + '">' +
      '<span data-role="value">' + esc(App.fmt.metric(key, data)) + '</span>' +
      '<span class="value__unit">' + esc(C.metrics[key].unit) + '</span></p>';
  }

  function sparkHTML(key) {
    return App.ui.sparkline(history(key), { minSpan: SPARK_SPAN[key], capacity: C.sim.historyLength });
  }

  /* Replace a block while preserving keyboard focus */
  function replaceKeepingFocus(el, html) {
    if (!el || !el.parentNode) return;
    var active = document.activeElement;
    var hadFocus = !!active && el.contains(active);
    var activeId = hadFocus ? active.id : null;
    var blockId = el.id;
    el.outerHTML = html;
    if (!hadFocus) return;
    var next = activeId && document.getElementById(activeId);
    if (next && !next.disabled) { next.focus({ preventScroll: true }); return; }
    var fresh = blockId && document.getElementById(blockId);
    if (!fresh) return;
    var f = fresh.querySelector('button:not([disabled])');
    if (f) { f.focus({ preventScroll: true }); return; }
    fresh.setAttribute('tabindex', '-1');
    fresh.focus({ preventScroll: true });
  }

  /* 1 · Mental health */
  function lastCheckin() {
    return (App.store.get('checkins') || {})[crewId] || null;
  }
  function mentalHint() {
    var c = lastCheckin();
    return c && c.at ? 'Last check-in: ' + App.fmt.ago(c.at) : 'No check-in yet';
  }
  function recommendedText() {
    var rec = C.mental.sleep.recommendedMin;
    return rec % 60 ? App.fmt.minutes(rec) : (rec / 60) + ' h';
  }
  function sleepValueHTML(min) {
    var h = Math.floor(min / 60), mm = min % 60;
    return h + '<small> h </small>' + (mm < 10 ? '0' : '') + mm + '<small> min</small>';
  }
  function sleepHintHTML(min) {
    if (min >= C.mental.sleep.recommendedMin) return '';
    return '<p class="reg-hint" data-level="1">' + App.icon('info') +
      '<span>Less than the recommended ' + esc(recommendedText()) + '. Fatigue is possible.</span></p>';
  }

  function mentalCard() {
    var c = lastCheckin();
    var mm = C.mental;
    var step = mm.sleep.stepMin;
    var ready = draft.mood !== null && draft.energy !== null;
    return '<div class="card reg-card" id="reg-mental-card">' +
      (c && c.example
        ? '<p class="reg-note">' + App.icon('info') + '<span>Sample check-in. Save a new one to replace it.</span></p>'
        : '') +
      '<div class="group"><p class="group__label">' + esc(mm.mood.label) + '</p>' +
        App.ui.segmented({ name: 'reg-mood', label: mm.mood.label, options: mm.mood.options, icons: mm.mood.icons, value: draft.mood }) +
      '</div>' +
      '<div class="group"><p class="group__label">' + esc(mm.energy.label) + '</p>' +
        App.ui.segmented({ name: 'reg-energy', label: mm.energy.label, options: mm.energy.options, icons: mm.energy.icons, value: draft.energy }) +
      '</div>' +
      '<div class="group">' +
        '<p class="group__label"><span>' + esc(mm.sleep.label) + '</span><span class="subtle">Recommended ' + esc(recommendedText()) + '</span></p>' +
        '<div class="stepper">' +
          '<button type="button" class="stepper__btn" id="reg-sleep-minus" data-action="sleep" data-step="-1" aria-label="Subtract ' + step + ' minutes of sleep">' + App.icon('minus') + '</button>' +
          '<output class="stepper__value" id="reg-sleep-value" aria-live="polite" aria-label="Hours of sleep">' + sleepValueHTML(draft.sleepMin) + '</output>' +
          '<button type="button" class="stepper__btn" id="reg-sleep-plus" data-action="sleep" data-step="1" aria-label="Add ' + step + ' minutes of sleep">' + App.icon('plus') + '</button>' +
        '</div>' +
        '<div id="reg-sleep-hint">' + sleepHintHTML(draft.sleepMin) + '</div>' +
      '</div>' +
      '<div class="reg-actions">' +
        '<button type="button" class="btn btn--primary btn--block" id="reg-save" data-action="save"' +
          (ready ? '' : ' disabled aria-describedby="reg-save-help"') + '>' + App.icon('check') + 'Save check-in</button>' +
        (ready ? '' : '<p class="reg-save-help" id="reg-save-help">Choose mood and energy to save.</p>') +
      '</div>' +
    '</div>';
  }

  function mentalSection() {
    return '<section class="section" id="reg-mental-section">' +
      App.ui.sectionHead({ eyebrow: 'Check-in', title: 'Mental health', hint: mentalHint() }) +
      mentalCard() + '</section>';
  }

  function selectSeg(btn) {
    var name = btn.getAttribute('data-seg');
    draft[name === 'reg-mood' ? 'mood' : 'energy'] = Number(btn.getAttribute('data-value'));
    App.dom.$$('[data-seg="' + name + '"]', root).forEach(function (b) {
      b.setAttribute('aria-checked', String(b === btn));
    });
    var save = $('#reg-save');
    if (save && draft.mood !== null && draft.energy !== null) {
      save.disabled = false;
      save.removeAttribute('aria-describedby');
      var help = $('#reg-save-help');
      if (help) help.remove();
    }
  }

  function stepSleep(dir) {
    var s = C.mental.sleep;
    draft.sleepMin = Math.max(0, Math.min(s.maxMin, draft.sleepMin + dir * s.stepMin));
    var out = $('#reg-sleep-value');
    if (out) out.innerHTML = sleepValueHTML(draft.sleepMin);
    var hint = $('#reg-sleep-hint');
    if (hint) hint.innerHTML = sleepHintHTML(draft.sleepMin);
  }

  function saveCheckin() {
    if (draft.mood === null || draft.energy === null) return;
    var checkin = { mood: draft.mood, energy: draft.energy, sleepMin: draft.sleepMin, at: Date.now() };
    var all = Object.assign({}, App.store.get('checkins') || {});
    all[crewId] = checkin;
    App.store.set({ checkins: all });
    App.bus.emit('checkin', { crewId: crewId, checkin: checkin });
    App.ui.toast('Check-in saved · ' + hhmm(checkin.at), { icon: 'check' });
    replaceKeepingFocus($('#reg-mental-section'), mentalSection());
  }

  function refreshAgo() {
    var hint = $('#reg-mental-section .section__hint');
    if (hint) hint.textContent = mentalHint();
  }

  /* 2 · Physical health */
  function sensorHead(key, aside) {
    var m = C.metrics[key];
    var sub = key === 'bp'
      ? [baselineText('bp'), 'on demand'].filter(Boolean).join(' · ')
      : baselineText(key);
    return '<div class="sensor__head">' +
      '<span class="sensor__icon">' + App.icon(m.icon) + '</span>' +
      '<div class="sensor__label"><span class="sensor__name">' + esc(m.label) + '</span>' +
        (sub ? '<span class="sensor__sub">' + esc(sub) + '</span>' : '') + '</div>' +
      (aside || '') + '</div>';
  }

  /* Continuous sensor row: HR, SpO₂, temperature */
  function liveRow(key) {
    var st = statusOf(key);
    var connected = st === 'connected';
    var data = latest();
    var v = rawValue(key, data);
    var level = connected && v !== null ? App.levels.classify(key, v) : null;
    var realHardware = !!(App.telemetry && App.telemetry.isHardwareConnected && App.telemetry.isHardwareConnected(crewId,key));\n    var live = '<span class="badge badge--live reg-live"><span class="reg-live__dot" aria-hidden="true"></span>' + (realHardware ? 'Hardware live' : 'Demo · not paired') + '</span>';
    var body;

    if (connected) {
      body = '<div class="sensor__readout"><div class="reg-reading">' + valueHTML(key, data) +
        '<div data-role="status">' + (v === null
          ? '<p class="reg-waiting">Waiting for data…</p>'
          : App.ui.statusLine(level, stateText(key, level, v))) + '</div></div>' +
        '<div class="reg-spark" data-role="spark">' + sparkHTML(key) + '</div></div>';
    } else {
      var busy = st === 'connecting';
      body = (!busy && wasLost(key)
          ? '<p class="reg-lost" data-level="2">' + App.icon('unlink') + '<span>Suit signal lost.</span></p>'
          : '') +
        '<button type="button" class="btn btn--secondary btn--block" id="reg-connect-' + key + '" data-action="connect" data-key="' + key + '"' +
        (busy ? ' disabled aria-busy="true"' : '') + '>' +
        (busy ? '<span class="spinner" aria-hidden="true"></span>Connecting…' : App.icon('signal') + 'Start demo feed') +
        '</button>';
    }
    return '<div class="sensor reg-sensor" id="reg-row-' + key + '" data-key="' + key + '"' +
      (connected ? ' data-level="' + App.ui.lvlAttr(level) + '"' : '') + '>' +
      sensorHead(key, connected ? live : '') + body + '</div>';
  }

  /* Blood pressure row measured by tapping the button */
  function bpRow() {
    var saved = ((App.store.get('physicalManual') || {})[crewId]) || {};
    var has = Number.isFinite(saved.bpSys) && Number.isFinite(saved.bpDia);
    var data = has ? { bpSys: saved.bpSys, bpDia: saved.bpDia, bpAt: saved.bpAt || null } : null;
    var result = has
      ? '<div class="reg-result"><div class="reg-result__main"><p class="eyebrow">Last manual / cuff reading</p>' +
          valueHTML('bp', data, true) + '<p class="reg-waiting">Not assessed as live suit telemetry.</p></div>' +
          (data.bpAt ? '<p class="reg-result__time">' + esc(hhmm(data.bpAt)) + '</p>' : '') + '</div>'
      : '<p class="reg-waiting">No blood-pressure measurement recorded.</p>';
    return '<div class="sensor reg-sensor" id="reg-row-bp" data-key="bp">' +
      sensorHead('bp') + result +
      '<button type="button" class="btn btn--secondary btn--block" data-action="go-diagnosis">' +
        App.icon('clipboard') + 'Enter blood pressure' +
      '</button></div>';
  }

  function bioHead() {
    var anyOff = BIO_LIVE.concat(SUIT).some(isOff);
    return '<div id="reg-bio-head">' + App.ui.sectionHead({
      eyebrow: 'Biomedical sensors',
      title: 'Physical health',
      hint: 'Live from the suit',
      action: anyOff
        ? '<button type="button" class="btn btn--ghost btn--sm" id="reg-connect-all" data-action="connect-all">' + App.icon('link') + 'Start demo feed</button>'
        : '',
    }) + '</div>';
  }

  function bioSection() {
    return '<section class="section" id="reg-bio-section">' + bioHead() +
      '<div class="card" id="reg-bio-card">' + BIO_LIVE.map(liveRow).join('') + bpRow() + '</div></section>';
  }

  /* 3 · Suit and life support */
  function tile(key) {
    var m = C.metrics[key];
    var st = statusOf(key);
    var connected = st === 'connected';
    var data = latest();
    var v = connected ? rawValue(key, data) : null;
    var level = v !== null ? App.levels.classify(key, v) : null;
    var foot;
    if (st === 'connecting') foot = '<span class="spinner" aria-hidden="true"></span>Connecting…';
    else if (!connected) foot = wasLost(key) ? 'Signal lost' : 'No signal';
    else if (v === null) foot = 'Waiting for data…';
    else foot = App.telemetry && App.telemetry.isHardwareConnected && App.telemetry.isHardwareConnected(crewId,key) ? esc(stateText(key, level, v, true)) : 'Demo data · hardware not paired';

    return '<div class="tile reg-tile" id="reg-tile-' + key + '" data-key="' + key + '"' +
      ' data-conn="' + (connected ? '1' : '0') + '" data-level="' + (v !== null ? App.ui.lvlAttr(level) : 'off') + '"' +
      ' role="group" aria-label="' + esc(m.label) + '">' +
      '<p class="tile__head">' + App.icon(m.icon) + '<span class="eyebrow">' + esc(m.short) + '</span></p>' +
      valueHTML(key, connected ? data : null, true) +
      '<p class="tile__foot" data-role="status">' + foot + '</p></div>';
  }

  function suitSection() {
    var anyOff = SUIT.some(isOff);
    return '<section class="section" id="reg-suit-section">' +
      App.ui.sectionHead({ eyebrow: 'Suit telemetry', title: 'Suit and life support' }) +
      '<div class="tiles" id="reg-tiles">' + SUIT.map(tile).join('') + '</div>' +
      (anyOff
        ? '<button type="button" class="btn btn--secondary btn--block" id="reg-connect-suit" data-action="connect-suit">' + App.icon('suit') + 'Connect suit</button>'
        : '') +
    '</section>';
  }

  /* Updates */
  /* Redraw affected sensor rows or tiles and their headers */
  function refresh(keys) {
    if (!root || !App.crew.isEva(crewId)) return;
    var suitTouched = false;
    keys.forEach(function (k) {
      if (SUIT.indexOf(k) >= 0) { suitTouched = true; return; }
      var row = $('#reg-row-' + k);
      if (row) replaceKeepingFocus(row, k === 'bp' ? bpRow() : liveRow(k));
    });
    replaceKeepingFocus($('#reg-bio-head'), bioHead());
    if (suitTouched) replaceKeepingFocus($('#reg-suit-section'), suitSection());
  }

  /* Update values, status, and charts in place */
  function updateRow(key, data) {
    var row = $('#reg-row-' + key);
    if (!row) return;
    var connected = statusOf(key) === 'connected';
    var valEl = row.querySelector('[data-role="value"]');
    if (connected !== !!valEl) { refresh([key]); return; }
    if (!connected) return;
    var v = rawValue(key, data);
    var level = v !== null ? App.levels.classify(key, v) : null;
    valEl.textContent = App.fmt.metric(key, data);
    row.setAttribute('data-level', App.ui.lvlAttr(level));
    var st = row.querySelector('[data-role="status"]');
    if (st) {
      st.innerHTML = v === null
        ? '<p class="reg-waiting">Waiting for data…</p>'
        : App.ui.statusLine(level, stateText(key, level, v));
    }
    var sp = row.querySelector('[data-role="spark"]');
    if (sp) sp.innerHTML = sparkHTML(key);
  }

  function updateTile(key, data) {
    var el = $('#reg-tile-' + key);
    if (!el) return;
    var connected = statusOf(key) === 'connected';
    if (connected !== (el.getAttribute('data-conn') === '1')) { refresh([key]); return; }
    if (!connected) return;
    var v = rawValue(key, data);
    var level = v !== null ? App.levels.classify(key, v) : null;
    el.setAttribute('data-level', v !== null ? App.ui.lvlAttr(level) : 'off');
    var valEl = el.querySelector('[data-role="value"]');
    if (valEl) valEl.textContent = App.fmt.metric(key, data);
    var foot = el.querySelector('[data-role="status"]');
    if (foot) foot.textContent = v === null ? 'Waiting for data…' : stateText(key, level, v, true);
  }

  function onTelemetry(p) {
    if (!p || p.crewId !== crewId || !App.crew.isEva(crewId)) return;
    var data = p.data || latest();
    BIO_LIVE.forEach(function (k) { updateRow(k, data); });
    SUIT.forEach(function (k) { updateTile(k, data); });
    if (data && data.bpAt && data.bpAt !== lastBpAt && !pending.bp && statusOf('bp') !== 'measuring') refresh(['bp']);
  }

  /* Actions */
  function noTelemetry() {
    App.ui.toast('Suit link unavailable', { icon: 'unlink', level: 2 });
  }

  function connect(keys) {
    var t = T();
    if (!t || !t.connect) { noTelemetry(); return; }
    if (!keys.length) return;
    var id = crewId;
    keys.forEach(function (k) { pending[k] = true; });
    refresh(keys);
    Promise.all(keys.map(function (k) {
      return Promise.resolve(t.connect(id, k)).then(
        function () { return { ok: true }; },
        function (err) { return { ok: false, error: err && err.message }; });
    })).then(function (results) {
      if (!root || id !== crewId) return;          // Crew member or view changed
      keys.forEach(function (k) { delete pending[k]; });
      refresh(keys);
      var failed = results.filter(function (r) { return !r.ok; });
      if (failed.length) {
        App.ui.toast('Unable to connect: ' + (failed[0].error || 'try again').toLowerCase(), { icon: 'unlink', level: 2 });
      } else {
        App.ui.toast(keys.length === 1 ? 'Sensor connected · ' + C.metrics[keys[0]].label : 'Sensors connected', { icon: 'link' });
      }
    });
  }

  function connectAll() {
    var t = T();
    if (!t) { noTelemetry(); return; }
    var keys = BIO_LIVE.concat(SUIT).filter(isOff);
    if (!t.connectAll) { connect(keys); return; }
    var id = crewId;
    keys.forEach(function (k) { pending[k] = true; });
    refresh(keys);
    Promise.resolve(t.connectAll(id)).then(function (results) {
      if (!root || id !== crewId) return;
      keys.forEach(function (k) { delete pending[k]; });
      refresh(keys);
      var failed = (results || []).filter(function (r) { return r && r.ok === false; }).length;
      App.ui.toast(failed ? 'Remaining: ' + failed + ' sensors without a signal' : 'All sensors connected',
        { icon: failed ? 'unlink' : 'link', level: failed ? 2 : undefined });
    }, function () {
      if (!root || id !== crewId) return;
      keys.forEach(function (k) { delete pending[k]; });
      refresh(keys);
      App.ui.toast('Unable to connect the suit. Try again.', { icon: 'unlink', level: 2 });
    });
  }

  function measureBP() {
    var t = T();
    if (!t || !t.measureBP) { noTelemetry(); return; }
    var id = crewId;
    pending.bp = true;
    refresh(['bp']);
    Promise.resolve(t.measureBP(id)).then(function () { finish(true); }, function (err) { finish(false, err); });

    function finish(ok, err) {
      if (!root || id !== crewId) return;
      delete pending.bp;
      refresh(['bp']);
      if (ok) App.ui.toast('Pressure measured · ' + App.fmt.metric('bp', latest()) + ' mmHg', { icon: 'gauge' });
      else App.ui.toast('Unable to measure: ' + ((err && err.message) || 'try again').toLowerCase(), { icon: 'unlink', level: 2 });
    }
  }

  /* View events delegated to main */
  function onClick(e) {
    var seg = e.target.closest('[data-seg]');
    if (seg && root.contains(seg)) { selectSeg(seg); return; }
    var btn = e.target.closest('[data-action]');
    if (!btn || !root.contains(btn) || btn.disabled) return;
    var action = btn.getAttribute('data-action');
    if (action === 'sleep') stepSleep(Number(btn.getAttribute('data-step')));
    else if (action === 'save') saveCheckin();
    else if (action === 'connect') connect([btn.getAttribute('data-key')]);
    else if (action === 'connect-all') connectAll();
    else if (action === 'connect-suit') connect(SUIT.filter(isOff));
    else if (action === 'measure-bp') measureBP();
    else if (action === 'go-diagnosis') App.shell.go('diagnosis');
  }

  /* Arrow-key navigation for mood and energy options */
  function onKeydown(e) {
    var seg = e.target.closest && e.target.closest('[data-seg]');
    if (!seg) return;
    var dir = { ArrowRight: 1, ArrowDown: 1, ArrowLeft: -1, ArrowUp: -1 }[e.key];
    if (!dir) return;
    e.preventDefault();
    var group = App.dom.$$('[data-seg="' + seg.getAttribute('data-seg') + '"]', root);
    var next = group[(group.indexOf(seg) + dir + group.length) % group.length];
    next.focus();
    selectSeg(next);
  }

  /* View */
  App.views.log = {
    render: function (el) {
      root = el;
      pending = {};
      lastBpAt = null;
      var m = App.crew.selected();
      crewId = m ? m.id : null;
      if (!m) {
        root.innerHTML = App.ui.empty('users', 'No crew configured.');
        return;
      }
      var c = lastCheckin();
      draft = {
        mood: c && typeof c.mood === 'number' ? c.mood : null,
        energy: c && typeof c.energy === 'number' ? c.energy : null,
        sleepMin: c && typeof c.sleepMin === 'number' ? c.sleepMin : C.mental.sleep.defaultMin,
      };

      var html = mentalSection();
      if (App.crew.isEva(crewId)) {
        html += bioSection() + suitSection();
      } else {
        html += '<section class="section" id="reg-inside-section">' +
          App.ui.sectionHead({ eyebrow: 'Suit telemetry', title: 'Physical health' }) +
          App.ui.empty('suit', m.name + ' is inside the spacecraft. No suit telemetry to display.') +
          '</section>';
      }
      root.innerHTML = html;
      root.addEventListener('click', onClick);
      root.addEventListener('keydown', onKeydown);
      clearInterval(agoTimer);
      agoTimer = setInterval(refreshAgo, 30000);
    },

    onEvent: function (evt, p) {
      if (!root) return;
      if (evt === 'telemetry') onTelemetry(p);
      else if (evt === 'sensor') {
        if (!p || p.crewId !== crewId) return;
        var keys = p.key ? [p.key] : BIO_LIVE.concat(['bp'], SUIT);
        keys = keys.filter(function (k) { return !pending[k]; });
        if (keys.length) refresh(keys);
      } else if (evt === 'checkin') {
        if (p && p.crewId === crewId) refreshAgo();
      }
    },

    destroy: function () {
      if (root) {
        root.removeEventListener('click', onClick);
        root.removeEventListener('keydown', onKeydown);
      }
      clearInterval(agoTimer);
      agoTimer = null;
      root = null;
      pending = {};
    },
  };
})();
