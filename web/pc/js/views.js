/* =====================================================================
   EVA STATION · views
   ---------------------------------------------------------------------
   triage      Live triage: explorers ordered by IPV, 15-min trends,
               contextual diagnosis, active alert and quick actions
   premission  Go/No-Go board: privacy-filtered readiness, vulnerability,
               baseline phase, radiation and recovery
   console     Protocols & control: open alerts, protocol checklist,
               acknowledge, instructions, incident, EVA abort, operator log
   crew        Profiles and personal baselines
   reports     Post-EVA, black-box incident and monthly reports + CSV export
   blackbox    Tamper-evident event chain and integrity check
   simulator   Virtual suits and failure injection (demonstration)
   Each view: render(root) and optional update(kind) for live data.
   ===================================================================== */
(function () {
  'use strict';
  var S = window.Station;
  var esc = S.esc, icon = S.icon;

  var VITALS = [
    { k: 'hr', label: 'HR', unit: 'bpm', d: 0, codes: ['HR_REST', 'HR_EFFORT', 'HR_MAX', 'HR_LOW', 'SENSOR_HR'] },
    { k: 'spo2', label: 'SpO₂', unit: '%', d: 0, codes: ['SPO2', 'SENSOR_SPO2'] },
    { k: 'temp', label: 'Temp', unit: '°C', d: 1, codes: ['TEMP_HIGH', 'TEMP_LOW', 'SENSOR_TEMP'] },
    { k: 'rr', label: 'Resp.', unit: 'rpm', d: 0, codes: ['RR'] },
    { k: 'hrv', label: 'HRV', unit: 'ms', d: 0, codes: ['HRV'] },
    { k: 'suitP', label: 'Suit P', unit: 'psi', d: 2, codes: ['SUIT_P'] },
    { k: 'o2', label: 'O₂ res.', unit: 'min', d: 0, codes: ['O2_RES'] },
    { k: 'co2', label: 'CO₂', unit: 'mmHg', d: 1, codes: ['CO2'] },
    { k: 'batt', label: 'Battery', unit: '%', d: 0, codes: ['BATT'] },
    { k: 'rad', label: 'Dose rate', unit: 'mSv/h', d: 2, codes: ['RAD'] },
  ];
  var EFFORT = ['Rest', 'Moderate effort', 'High effort'];
  var QUICK = ['Return to airlock now.', 'Pause three minutes and hydrate.', 'Increase suit cooling.', 'Check your O₂ flow.',
    'Report your status by radio.', 'Switch to backup CO₂ filter.', 'Seek shelter now.', 'Buddy, check on your partner.'];

  function evaCrew() { return S.state.crew.filter(function (c) { return c.eva; }); }
  function lvlOf(c) { return c.eval && c.eval.status !== 'NO_DATA' ? c.eval.level : 'off'; }
  function vitalLevel(c, codes) {
    var l = null;
    (c.eval.findings || []).forEach(function (f) { if (codes.indexOf(f.code) >= 0 && (l === null || f.level > l)) l = f.level; });
    return l;
  }
  function deviceChip(c) {
    var d = c.device || {};
    if (d.source === 'device') return '<span class="chip" data-tone="accent">' + icon('link') + 'Phone linked</span>';
    if (d.source === 'virtual') return '<span class="chip">' + icon('suit') + 'Virtual suit</span>';
    return '<span class="chip" data-level="off">' + icon('unlink') + 'No link</span>';
  }
  function readinessChip(r) {
    if (!r) return '<span class="chip" data-level="off">No check-in today</span>';
    var l = { GREEN: 0, YELLOW: 1, RED: 3 }[r.status];
    return '<span class="chip" data-level="' + l + '">Readiness ' + esc(r.status.toLowerCase()) + '</span>';
  }

  /* ---------- Shared mission controls ---------- */
  function missionActions() {
    var m = S.state.mission;
    if (!m) return '<button type="button" class="btn btn--primary" data-mission="new">' + icon('plus') + 'Plan an EVA</button>';
    if (m.status === 'PLANNED') return '<button type="button" class="btn btn--primary" data-mission="start">' + icon('play') + 'Start EVA</button>';
    if (m.status === 'ACTIVE') {
      return '<a class="btn btn--secondary" href="/reports/mission/' + encodeURIComponent(m.id) + '" target="_blank" rel="noopener">' + icon('fileText') + 'Live report</a>' +
        '<button type="button" class="btn btn--secondary" data-mission="end">' + icon('check') + 'End EVA</button>' +
        '<button type="button" class="btn btn--danger" data-mission="abort">' + icon('octagon') + 'Abort EVA</button>';
    }
    return '<a class="btn btn--secondary" href="/reports/mission/' + encodeURIComponent(m.id) + '" target="_blank" rel="noopener">' + icon('fileText') + 'Post-EVA report</a>' +
      '<button type="button" class="btn btn--primary" data-mission="new">' + icon('plus') + 'Plan next EVA</button>';
  }
  S.missionAction = function (act) {
    if (act === 'start') return S.post('/api/mission/start', {}, 'EVA started · T+ clock running');
    if (act === 'new') return S.post('/api/mission/new', {}, 'Next EVA planned');
    if (act === 'end') {
      return S.confirm({ title: 'End the EVA', message: 'Close the EVA, compute each explorer’s physiological load and recovery, and generate the post-EVA report.', confirmLabel: 'End EVA' })
        .then(function (ok) { if (ok) return S.post('/api/mission/end', {}, 'EVA completed · post-EVA report ready'); });
    }
    if (act === 'abort') {
      return S.confirm({ title: 'Abort the EVA', message: 'Every explorer’s phone will announce the abort and the black box will freeze an incident for each explorer.', input: 'Reason', value: 'Operator decision', confirmLabel: 'Abort EVA', danger: true })
        .then(function (reason) { if (reason) return S.post('/api/mission/abort', { reason: typeof reason === 'string' ? reason : '' }, 'EVA ABORTED · explorers notified'); });
    }
  };
  document.addEventListener('click', function (e) {
    var b = e.target.closest('[data-mission]');
    if (b) S.missionAction(b.getAttribute('data-mission'));
    var ins = e.target.closest('[data-instruct]');
    if (ins) S.instruct(ins.getAttribute('data-instruct'));
    var inc = e.target.closest('[data-incident]');
    if (inc) {
      var id = inc.getAttribute('data-incident');
      S.confirm({ title: 'Report incident', message: 'Freeze a black-box window (30 min before to 15 min after now) for ' + S.who(id) + '.', confirmLabel: 'Report incident' })
        .then(function (ok) { if (ok) S.post('/api/crew/' + encodeURIComponent(id) + '/incident', {}, 'Incident opened in the black box'); });
    }
    var help = e.target.closest('[data-help-clear]');
    if (help) S.post('/api/crew/' + encodeURIComponent(help.getAttribute('data-help-clear')) + '/help-clear', {}, 'Help request marked as handled');
  });

  /* Instruction composer (quick phrases ≤ 8 words) */
  S.instruct = function (crewId) {
    var target = crewId ? S.who(crewId) : 'all explorers';
    S.modal({
      title: 'Instruction to ' + target,
      html: '<p class="muted">Shown and spoken on the phone. Keep it short: one action, 8 words or fewer.</p>' +
        '<div class="quick">' + QUICK.map(function (q) { return '<button type="button" data-q="' + esc(q) + '">' + esc(q) + '</button>'; }).join('') + '</div>' +
        '<div class="field"><label class="field__label" for="ins-text">Message</label><input class="input" id="ins-text" maxlength="140" autofocus></div>' +
        '<div class="st-actions" style="justify-content:flex-end"><button type="button" class="btn btn--primary" id="ins-send">' + icon('volume') + 'Send</button></div>',
      onMount: function (b) {
        var inp = b.querySelector('#ins-text');
        b.addEventListener('click', function (e) {
          var q = e.target.closest('[data-q]');
          if (q) { inp.value = q.getAttribute('data-q'); inp.focus(); return; }
          if (e.target.closest('#ins-send')) send();
        });
        inp.addEventListener('keydown', function (e) { if (e.key === 'Enter') send(); });
        function send() {
          var t = inp.value.trim();
          if (!t) { inp.focus(); return; }
          S.post('/api/instruction', { crewId: crewId || '', text: t }).then(function (j) {
            S.closeModal();
            S.toast(j.delivered ? 'Instruction delivered to ' + target : 'Saved in the log · the phone is offline', { icon: j.delivered ? 'check' : 'unlink' });
          });
        }
      },
    });
  };

  /* =====================================================================
     LIVE TRIAGE
     ===================================================================== */
  function trendBlock(c, key, label, unit, band, minSpan) {
    var tr = S.state.trends[c.id] || {};
    var vals = tr[key] || [];
    var last = vals.length ? vals[vals.length - 1] : null;
    var l = vitalLevel(c, (VITALS.filter(function (v) { return v.k === key; })[0] || {}).codes || []);
    return '<div class="trend"' + (l ? ' data-level="' + l + '"' : '') + '><p class="trend__k"><span>' + esc(label) + ' · 15 min</span><span>' +
      (typeof last === 'number' ? esc(S.num(last, key === 'temp' ? 1 : 0) + ' ' + unit) : '—') + '</span></p>' +
      S.spark(vals, { band: band, minSpan: minSpan }) + '</div>';
  }

  function alertBlock(c) {
    var a = S.alertFor(c.id);
    if (!a) return '';
    var lv = S.alertLevel(a);
    var left = Math.ceil(30 - (S.now() - a.t) / 1000);
    var state = a.response !== 'PENDING' ? 'Answered: ' + S.human(a.response) + (a.respondedBy ? ' (' + a.respondedBy + ')' : '')
      : a.escalated ? 'No answer · escalated to buddy' : 'Waiting for answer · escalates in ' + Math.max(0, left) + ' s';
    return '<div class="tcard__alert" data-level="' + lv + '"><div class="tcard__alert-head">' + icon(S.levelMeta(lv).icon) +
      '<b>' + esc(S.human(a.level)) + ' alert</b><span class="mono subtle">' + esc(a.code) + ' · ' + S.time(a.t) + '</span>' +
      '<span class="countdown">' + esc(state) + '</span></div>' +
      '<p class="subtle">Voice on the phone: “' + esc(a.voice) + '”</p>' +
      '<div class="tcard__actions">' + (a.response === 'PENDING' ? '<button type="button" class="btn btn--primary btn--sm" data-ack="' + a.id + '">' + icon('check') + 'Acknowledge</button>' : '') +
      '<a class="btn btn--secondary btn--sm" href="#console" data-focus-crew="' + esc(c.id) + '">' + icon('shield') + 'Protocol</a>' +
      (c.help ? '<button type="button" class="btn btn--secondary btn--sm" data-help-clear="' + esc(c.id) + '">Help handled</button>' : '') + '</div></div>';
  }

  function cardHTML(c, rank) {
    var lvl = lvlOf(c), e = c.eval || {}, v = c.vitals;
    var st = S.status(e.status);
    var eff = v && typeof v.effort === 'number' ? '<span class="chip">' + icon('activity') + esc(EFFORT[v.effort]) + '</span>' : '';
    var vitals = v ? '<div class="vitals">' + VITALS.map(function (x) {
      var val = v[x.k];
      if (val === undefined || val === null) return '';
      var l = vitalLevel(c, x.codes);
      return '<div class="vital"' + (l ? ' data-level="' + l + '"' : '') + '><span class="vital__k">' + esc(x.label) + '</span>' +
        '<span class="vital__v">' + esc(S.num(val, x.d)) + '<small>' + esc(x.unit) + '</small></span></div>';
    }).join('') +
      (v.bpSys ? '<div class="vital"><span class="vital__k">BP</span><span class="vital__v">' + Math.round(v.bpSys) + '/' + Math.round(v.bpDia) + '<small>mmHg</small></span></div>' : '') +
      '<div class="vital"><span class="vital__k">EVA dose</span><span class="vital__v">' + esc(S.num(c.dose, 3)) + '<small>mSv</small></span></div></div>'
      : '<div class="empty">' + icon('unlink') + '<p>Waiting for telemetry. Link this explorer’s phone or enable a virtual suit in Simulator.</p></div>';
    var base = c.baseline || {};
    var trends = v ? '<div class="trends">' +
      trendBlock(c, 'hr', 'Heart rate', 'bpm', [Math.max(40, (base.hrRest || 62) - 10), (base.hrRest || 62) + 55], 12) +
      trendBlock(c, 'spo2', 'SpO₂', '%', [94, 100], 3) +
      trendBlock(c, 'temp', 'Core temp', '°C', [36.0, 37.6], 0.6) + '</div>' : '';
    var findings = (e.findings || []).map(function (f) {
      return '<span class="badge" data-level="' + f.level + '">' + icon(S.levelMeta(f.level).icon) + esc(f.label + (f.value ? ' · ' + f.value : '')) + '</span>';
    }).join('');
    var factors = (c.factors || []).map(function (f) { return '<span class="chip" data-level="1">' + esc(f.label) + '</span>'; }).join('');
    return '<article class="tcard" data-crew="' + esc(c.id) + '" data-level="' + S.lvl(lvl) + '" aria-label="' + esc(c.role + ' ' + c.name + ', ' + st.label) + '">' +
      '<span class="tcard__rank">#' + rank + ' · ' + esc(c.id) + '</span>' +
      '<div class="tcard__head">' + S.avatar(c, lvl) +
        '<div class="tcard__who"><span class="tcard__name">' + esc(c.role + ' · ' + c.name) + '</span>' +
        '<div class="tcard__meta">' + S.badge(lvl, st.label) + deviceChip(c) + eff + readinessChip(c.readiness) + '</div></div>' +
        S.ring(e.ipv, lvl, 'IPV') + '</div>' +
      '<div class="tcard__diag" data-level="' + S.lvl(lvl) + '">' + icon(S.levelMeta(lvl).icon) + '<div><b>' + esc(e.diagnosis || '') + '</b>' +
        (e.protocol && e.protocol[0] && lvl !== 0 && lvl !== 'off' ? '<br><span class="muted">Next step: ' + esc(e.protocol[0]) + '</span>' : '') +
        '<br><span class="subtle">' + (v ? 'Last reading ' + S.ago(v.t) : 'No readings yet') + '</span></div></div>' +
      alertBlock(c) + vitals + trends +
      (findings || factors ? '<div class="chips">' + findings + factors + '</div>' : '') +
      '<div class="tcard__actions"><button type="button" class="btn btn--secondary btn--sm" data-instruct="' + esc(c.id) + '">' + icon('volume') + 'Instruction</button>' +
        '<button type="button" class="btn btn--ghost btn--sm" data-incident="' + esc(c.id) + '">' + icon('blackbox') + 'Report incident</button></div>' +
      '</article>';
  }

  function kpis() {
    var counts = { EMERGENCY: 0, RISK: 0, VULNERABLE: 0, STABLE: 0 };
    evaCrew().forEach(function (c) { if (counts[c.eval.status] !== undefined) counts[c.eval.status]++; });
    var open = S.openAlertsList().filter(function (a) { return S.alertLevel(a) >= 2; }).length;
    function k(level, n, label, sub) {
      return '<div class="kpi" data-level="' + level + '"><span class="eyebrow">' + label + '</span><span class="kpi__v">' + n + '</span><span class="kpi__s">' + sub + '</span></div>';
    }
    return k(3, counts.EMERGENCY, 'Emergency', 'IPV 90–100') + k(2, counts.RISK, 'Risk', 'IPV 70–89') +
      k(1, counts.VULNERABLE, 'Preventive watch', 'IPV 40–69') + k(0, counts.STABLE, 'Stable', 'IPV 0–39') +
      '<div class="kpi"><span class="eyebrow">Open alerts</span><span class="kpi__v">' + open + '</span><span class="kpi__s">risk or emergency</span></div>';
  }

  S.views.triage = {
    render: function (root) {
      root.innerHTML = '<div class="st-head"><div><p class="eyebrow">Guy in the chair</p><h1>Live triage</h1>' +
        '<p>Explorers ordered by Surveillance Priority Index (acute risk + accumulated vulnerability).</p></div>' +
        '<div class="st-actions" id="tri-actions">' + missionActions() + '</div></div>' +
        '<div class="grid grid--4" id="tri-kpis" style="grid-template-columns:repeat(auto-fit,minmax(150px,1fr))">' + kpis() + '</div>' +
        '<div class="triage" id="tri-cards"></div>' +
        '<div id="tri-iv"></div>';
      this.update();
    },
    update: function (kind) {
      var root = document.getElementById('tri-cards');
      if (!root) return;
      if (kind === 'mission' || !kind) document.getElementById('tri-actions').innerHTML = missionActions();
      document.getElementById('tri-kpis').innerHTML = kpis();
      var list = evaCrew();
      if (!list.length) { root.innerHTML = '<div class="empty">' + icon('users') + '<p>No crew assigned to the EVA.</p></div>'; return; }
      var existing = {};
      root.querySelectorAll('.tcard').forEach(function (el) { existing[el.getAttribute('data-crew')] = el; });
      list.forEach(function (c, i) {
        var html = cardHTML(c, i + 1);
        var el = existing[c.id];
        if (!el) {
          var tmp = document.createElement('div');
          tmp.innerHTML = html;
          el = tmp.firstChild;
        } else if (el._html !== html) {
          if (!el.contains(document.activeElement)) {
            var t2 = document.createElement('div');
            t2.innerHTML = html;
            var fresh = t2.firstChild;
            el.replaceWith(fresh);
            el = fresh;
          }
        }
        el._html = html;
        delete existing[c.id];
        if (root.children[i] !== el) root.insertBefore(el, root.children[i] || null);
      });
      Object.keys(existing).forEach(function (k) { existing[k].remove(); });
      var iv = S.state.crew.filter(function (c) { return !c.eva; });
      document.getElementById('tri-iv').innerHTML = iv.length ? '<p class="subtle">In the cabin (no suit telemetry): ' +
        iv.map(function (c) { return esc(c.role + ' · ' + c.name); }).join(', ') + '</p>' : '';
    },
  };

  /* =====================================================================
     PRE-MISSION · GO / NO-GO
     ===================================================================== */
  S.hist = S.hist || {};
  function loadHist(id) {
    if (S.hist[id] && Date.now() - S.hist[id].at < 30000) return;
    S.hist[id] = { at: Date.now(), list: (S.hist[id] && S.hist[id].list) || [] };
    S.api('GET', '/api/crew/' + encodeURIComponent(id) + '/readiness?days=14').then(function (list) {
      S.hist[id].list = list;
      if (S.current === 'premission') S.views.premission.update('hist');
    }, function () { /* ignore */ });
  }
  function verdict(c) {
    var r = c.readiness, v = c.vuln || 0;
    if (!r) return { level: 'off', icon: 'clock', title: 'Pending', text: 'No check-in received today from this explorer’s phone.' };
    if (r.status === 'RED' || v >= 75) return { level: 3, icon: 'octagon', title: 'NO-GO', text: 'Private medical review required before any EVA.' };
    if (r.status === 'YELLOW' || v >= 15) return { level: 1, icon: 'info', title: 'GO with reservations', text: 'Limit the EVA to 2 h · radio check-in every 30 min · preventive watch.' };
    return { level: 0, icon: 'check', title: 'GO', text: 'Cleared for EVA.' };
  }
  function gcard(c) {
    var vd = verdict(c), r = c.readiness, b = c.baseline || {};
    var radPct = c.radBudget ? Math.min(100, (c.radAccum + (c.dose || 0)) / c.radBudget * 100) : 0;
    var h = (S.hist[c.id] && S.hist[c.id].list) || [];
    var byDay = {};
    h.forEach(function (x) { byDay[x.day] = x; });
    var days = '';
    for (var i = 13; i >= 0; i--) {
      var d = new Date(); d.setDate(d.getDate() - i);
      var key = d.getFullYear() + '-' + String(d.getMonth() + 1).padStart(2, '0') + '-' + String(d.getDate()).padStart(2, '0');
      var x = byDay[key];
      var l = x ? { GREEN: 0, YELLOW: 1, RED: 3 }[x.status] : 'off';
      days += '<span data-level="' + l + '" title="' + key + (x ? ' · ' + x.status + ' · sleep ' + S.num(x.sleepH, 1) + ' h' : ' · no record') + '"></span>';
    }
    var rec = '';
    if (c.lastEva) {
      var left = (c.lastEva.endedAt + c.lastEva.recoveryH * 3600000 - S.now()) / 3600000;
      rec = '<div class="kv__row"><dt class="kv__key">Recovery after ' + esc(c.lastEva.missionId) + '</dt><dd class="kv__val">' +
        (left > 0 ? S.num(left, 0) + ' h left of ' + S.num(c.lastEva.recoveryH, 0) + ' h' : 'Complete (' + S.num(c.lastEva.recoveryH, 0) + ' h)') + '</dd></div>';
    }
    return '<article class="gcard"><div class="row-between"><div class="row">' + S.avatar(c, vd.level === 'off' ? null : vd.level) +
      '<div><b class="tcard__name">' + esc(c.role + ' · ' + c.name) + '</b><br><span class="subtle">' + esc(c.specialty) + '</span></div></div>' +
      (c.eva ? '<span class="chip" data-tone="accent">EVA</span>' : '<span class="chip">Cabin</span>') + '</div>' +
      '<div class="gcard__verdict" data-level="' + S.lvl(vd.level) + '">' + icon(vd.icon) + '<div><b>' + esc(vd.title) + '</b><span class="muted">' + esc(vd.text) + '</span></div></div>' +
      (r ? '<p>' + esc(r.summary) + '</p>' : '') +
      ((c.factors || []).length ? '<div class="chips">' + c.factors.map(function (f) { return '<span class="chip" data-level="1">' + esc(f.label) + '</span>'; }).join('') + '</div>' : '') +
      '<div class="stack" style="gap:6px"><div class="row-between"><span class="eyebrow">Vulnerability</span><span class="mono">' + (c.vuln || 0) + '/100</span></div>' +
      '<div class="meter" data-level="' + (c.vuln >= 60 ? 3 : c.vuln >= 15 ? 1 : 0) + '"><span style="width:' + Math.min(100, c.vuln || 0) + '%"></span></div></div>' +
      '<div class="stack" style="gap:6px"><div class="row-between"><span class="eyebrow">Career radiation</span><span class="mono">' + S.num(radPct, 1) + ' % of ' + S.num(c.radBudget, 0) + ' mSv</span></div>' +
      '<div class="meter" data-level="' + (radPct > 80 ? 3 : radPct > 50 ? 1 : 0) + '"><span style="width:' + radPct.toFixed(1) + '%"></span></div></div>' +
      '<div class="stack" style="gap:6px"><span class="eyebrow">Readiness · last 14 days</span><div class="days">' + days + '</div></div>' +
      '<dl class="kv">' +
        '<div class="kv__row"><dt class="kv__key">Baseline</dt><dd class="kv__val">' + (b.phase === 'ROLLING_7D' ? 'Rolling 7-day mean' : 'Adaptation day ' + (b.days || 0) + ' of 14') + '</dd></div>' +
        '<div class="kv__row"><dt class="kv__key">Resting HR · HRV</dt><dd class="kv__val mono">' + S.num(b.hrRest, 0) + ' ± ' + S.num(b.hrRestSd, 1) + ' bpm · ' + S.num(b.hrv, 0) + ' ms</dd></div>' +
        '<div class="kv__row"><dt class="kv__key">Habitual sleep</dt><dd class="kv__val mono">' + S.num(b.sleepH, 1) + ' h</dd></div>' + rec +
      '</dl></article>';
  }
  S.views.premission = {
    render: function (root) {
      var m = S.state.mission;
      root.innerHTML = '<div class="st-head"><div><p class="eyebrow">Before the EVA</p><h1>Pre-mission · Go / No-Go</h1>' +
        '<p>' + (m ? esc(m.name + ' · ' + m.environment + ' · ' + m.site + ' · planned ' + m.plannedH + ' h') : 'No mission planned') + '</p></div>' +
        '<div class="st-actions" id="pm-actions">' + missionActions() + '</div></div>' +
        '<p class="note subtle">' + icon('shield') + ' Privacy: each phone processes its private diary (mood, notes, cycle) locally and sends only this verdict, a vulnerability index and factor codes.</p>' +
        '<div class="gng" id="pm-cards"></div>';
      S.state.crew.forEach(function (c) { loadHist(c.id); });
      this.update();
    },
    update: function (kind) {
      var el = document.getElementById('pm-cards');
      if (!el) return;
      if (kind === 'tick' && el._t && Date.now() - el._t < 5000) return; // refresh every 5 s
      el._t = Date.now();
      document.getElementById('pm-actions').innerHTML = missionActions();
      el.innerHTML = S.state.crew.map(gcard).join('');
    },
  };

  /* =====================================================================
     PROTOCOLS & CONTROL CONSOLE
     ===================================================================== */
  S.checks = S.checks || {};
  S.sel = null;
  function consoleList() {
    var list = S.openAlertsList();
    if (S.focusCrew) {
      var f = list.filter(function (a) { return a.crewId === S.focusCrew; })[0];
      if (f) S.sel = f.id;
      S.focusCrew = null;
    }
    if (!list.some(function (a) { return a.id === S.sel; })) S.sel = list.length ? list[0].id : null;
    if (!list.length) return '<div class="empty">' + icon('check') + '<p>No open alerts. The station keeps watching.</p></div>';
    return '<div class="alist">' + list.map(function (a) {
      var lv = S.alertLevel(a);
      var st = a.response !== 'PENDING' ? S.human(a.response) : a.escalated ? 'Escalated' : 'Pending';
      return '<button type="button" class="aitem" data-sel="' + a.id + '" data-level="' + lv + '" aria-pressed="' + (a.id === S.sel) + '">' + icon(S.levelMeta(lv).icon) +
        '<span class="aitem__t"><b>' + esc(S.who(a.crewId)) + ' · ' + esc(S.human(a.level)) + '</b><span>' + esc(a.reason) + '</span></span>' +
        '<span class="badge" data-level="' + (a.response === 'PENDING' ? (a.escalated ? 3 : lv) : 0) + '">' + esc(st) + '</span></button>';
    }).join('') + '</div>';
  }
  function consoleDetail() {
    var a = S.state.openAlerts[S.sel];
    if (!a) return '<div class="card stack"><h2 class="section__title">Quick actions</h2><p class="muted">Select an alert to see its protocol. You can still send instructions or report an incident.</p>' +
      '<div class="st-actions"><button type="button" class="btn btn--secondary" data-instruct="">' + icon('volume') + 'Instruction to all explorers</button></div></div>';
    var c = S.crew(a.crewId) || {};
    var lv = S.alertLevel(a);
    var checks = S.checks[a.id] || (S.checks[a.id] = {});
    var left = Math.ceil(30 - (S.now() - a.t) / 1000);
    return '<div class="card stack" data-level="' + lv + '">' +
      '<div class="row-between"><div><p class="eyebrow">' + esc(a.code) + ' · raised ' + S.time(a.t) + '</p><h2 class="section__title">' + esc(S.who(a.crewId)) + '</h2></div>' + S.badge(lv, S.human(a.level)) + '</div>' +
      '<p><b>' + esc((c.eval && c.eval.diagnosis) || a.reason) + '</b></p>' +
      '<p class="subtle">Voice on the explorer’s phone: “' + esc(a.voice) + '”</p>' +
      '<p class="mono" id="con-state">' + esc(a.response !== 'PENDING' ? 'Answer: ' + S.human(a.response) + ' by ' + (a.respondedBy || '—') + ' in ' + S.num(a.responseS, 1) + ' s'
        : a.escalated ? 'No answer in 30 s · relayed to the other explorers' : 'Waiting for an answer · escalation in ' + Math.max(0, left) + ' s') + '</p>' +
      '<h3 class="eyebrow">Suggested protocol</h3><ol class="steps">' + (a.protocol || []).map(function (s, i) {
        return '<li><label><input type="checkbox" data-check="' + a.id + ':' + i + '"' + (checks[i] ? ' checked' : '') + '><span>' + esc(s) + '</span></label></li>';
      }).join('') + '</ol>' +
      '<div class="st-actions">' + (a.response === 'PENDING' ? '<button type="button" class="btn btn--primary" data-ack="' + a.id + '">' + icon('check') + 'Acknowledge</button>' : '') +
        '<button type="button" class="btn btn--secondary" data-instruct="' + esc(a.crewId) + '">' + icon('volume') + 'Send instruction</button>' +
        (c.help ? '<button type="button" class="btn btn--secondary" data-help-clear="' + esc(a.crewId) + '">Help handled</button>' : '') +
        '<button type="button" class="btn btn--ghost" data-incident="' + esc(a.crewId) + '">' + icon('blackbox') + 'Report incident</button>' +
        (S.state.mission && S.state.mission.status === 'ACTIVE' ? '<button type="button" class="btn btn--danger" data-mission="abort">' + icon('octagon') + 'Abort EVA</button>' : '') + '</div></div>';
  }
  function oplogHTML() {
    var l = S.state.oplog.slice(0, 80);
    if (!l.length) return '<p class="subtle">No actions yet.</p>';
    return '<ul class="oplog">' + l.map(function (e) {
      return '<li><span class="mono">' + S.time(e.t) + '</span><span class="oplog__who">' + esc(e.actor) + '</span><span>' + esc(e.detail) + '</span></li>';
    }).join('') + '</ul>';
  }
  S.views.console = {
    render: function (root) {
      root.innerHTML = '<div class="vwrap"><div class="st-head"><div><p class="eyebrow">Assist the operator</p><h1>Protocols &amp; control</h1>' +
        '<p>Open alerts from most to least serious. Confirm reception, guide the explorer, or abort the EVA.</p></div>' +
        '<div class="st-actions">' + missionActions() + '</div></div>' +
        '<div class="split"><div class="stack"><div id="con-list"></div></div><div id="con-detail"></div></div>' +
        '<section class="section"><div class="section__head"><h2 class="section__title">Operator log</h2><span class="subtle">“Cockpit voice recorder” of the station</span></div>' +
        '<div class="card" id="con-log"></div></section></div>';
      root = root.firstElementChild; // listeners live on the view's own element
      root.addEventListener('click', function (e) {
        var s = e.target.closest('[data-sel]');
        if (s) { S.sel = Number(s.getAttribute('data-sel')); S.views.console.update('alert'); }
      });
      root.addEventListener('change', function (e) {
        var c = e.target.closest('[data-check]');
        if (!c) return;
        var p = c.getAttribute('data-check').split(':');
        (S.checks[p[0]] = S.checks[p[0]] || {})[p[1]] = c.checked;
      });
      this.update();
    },
    update: function (kind) {
      var list = document.getElementById('con-list');
      if (!list) return;
      list.innerHTML = consoleList();
      var det = document.getElementById('con-detail');
      if (kind === 'tick' && det._sel === S.sel) {
        var a = S.state.openAlerts[S.sel], st = document.getElementById('con-state');
        if (a && st && a.response === 'PENDING' && !a.escalated) st.textContent = 'Waiting for an answer · escalation in ' + Math.max(0, Math.ceil(30 - (S.now() - a.t) / 1000)) + ' s';
      } else if (!det.contains(document.activeElement) || kind !== 'tick') {
        det.innerHTML = consoleDetail();
        det._sel = S.sel;
      }
      if (kind !== 'tick') document.getElementById('con-log').innerHTML = oplogHTML();
    },
  };

  /* =====================================================================
     CREW
     ===================================================================== */
  S.views.crew = {
    render: function (root) {
      root.innerHTML = '<div class="st-head"><div><p class="eyebrow">Records</p><h1>Crew</h1><p>Identity, medical history and personal baseline (the reference used by the triage engine).</p></div></div>' +
        '<div class="gng">' + S.state.crew.map(function (c) {
          var b = c.baseline || {};
          var allergy = c.allergies && !/^none/i.test(c.allergies);
          return '<article class="gcard"><div class="row">' + S.avatar(c, null) + '<div><b class="tcard__name">' + esc(c.name) + '</b><br><span class="subtle">' + esc(c.id + ' · ' + c.role + ' · ' + c.specialty) + '</span></div></div>' +
            '<dl class="kv">' +
              '<div class="kv__row"><dt class="kv__key">Age · sex</dt><dd class="kv__val">' + c.age + ' · ' + (c.sex === 'M' ? 'Male' : 'Female') + '</dd></div>' +
              '<div class="kv__row"><dt class="kv__key">Height · weight</dt><dd class="kv__val mono">' + S.num(c.heightCm, 0) + ' cm · ' + S.num(c.weightKg, 0) + ' kg</dd></div>' +
              '<div class="kv__row"><dt class="kv__key">Blood type</dt><dd class="kv__val">' + esc(c.bloodType) + '</dd></div>' +
              '<div class="kv__row"><dt class="kv__key">Allergies</dt><dd class="kv__val"' + (allergy ? ' style="color:var(--critical)"' : '') + '>' + esc(c.allergies) + '</dd></div>' +
              '<div class="kv__row"><dt class="kv__key">Conditions</dt><dd class="kv__val">' + esc(c.conditions) + '</dd></div>' +
              '<div class="kv__row"><dt class="kv__key">Medication</dt><dd class="kv__val">' + esc(c.medication) + '</dd></div>' +
              '<div class="kv__row"><dt class="kv__key">Baseline phase</dt><dd class="kv__val">' + (b.phase === 'ROLLING_7D' ? 'Rolling 7 days' : 'Adaptation (day ' + b.days + '/14)') + '</dd></div>' +
              '<div class="kv__row"><dt class="kv__key">Resting HR · HRV</dt><dd class="kv__val mono">' + S.num(b.hrRest, 0) + ' ± ' + S.num(b.hrRestSd, 1) + ' bpm · ' + S.num(b.hrv, 0) + ' ms</dd></div>' +
              '<div class="kv__row"><dt class="kv__key">SpO₂ · temp · resp.</dt><dd class="kv__val mono">' + S.num(b.spo2, 0) + ' % · ' + S.num(b.temp, 1) + ' °C · ' + S.num(b.rr, 0) + ' rpm</dd></div>' +
              '<div class="kv__row"><dt class="kv__key">Blood pressure</dt><dd class="kv__val mono">' + S.num(b.bpSys, 0) + '/' + S.num(b.bpDia, 0) + ' mmHg</dd></div>' +
              '<div class="kv__row"><dt class="kv__key">Career radiation</dt><dd class="kv__val mono">' + S.num(c.radAccum, 1) + ' / ' + S.num(c.radBudget, 0) + ' mSv</dd></div>' +
            '</dl><a class="btn btn--secondary btn--sm" href="/reports/monthly/' + encodeURIComponent(c.id) + '" target="_blank" rel="noopener">' + icon('fileText') + 'Monthly health report</a></article>';
        }).join('') + '</div>';
    },
  };

  /* =====================================================================
     REPORTS
     ===================================================================== */
  var TABLES = [['telemetry', 'Telemetry (1 Hz)'], ['alerts', 'Alerts'], ['incidents', 'Incidents'], ['readiness', 'Readiness'],
    ['oplog', 'Operator log'], ['eva_summary', 'Post-EVA summaries'], ['monthly_health', 'Monthly panels'], ['missions', 'Missions'], ['blackbox_chain', 'Black-box chain']];
  function incidentRows() {
    var l = S.state.incidents;
    if (!l.length) return '<tr><td colspan="7" class="subtle">No incidents. They open automatically on an emergency, on an EVA abort, or with “Report incident”.</td></tr>';
    return l.map(function (i) {
      var lv = { SUDDEN: 2, PROGRESSIVE: 1, SENSOR_FAULT: 0 }[i.classification];
      return '<tr><td class="mono">#' + i.id + '</td><td>' + esc(S.who(i.crewId)) + '</td><td>' + esc(S.human(i.trigger)) + '</td><td class="mono">' + S.date(i.eventT) + ' ' + S.time(i.eventT) + '</td>' +
        '<td>' + (i.classification ? S.badge(lv, S.human(i.classification)) : '<span class="subtle">pending</span>') + '</td>' +
        '<td>' + (i.status === 'RECORDING' ? '<span class="chip" data-tone="accent">Recording · until ' + S.hm(i.windowEnd) + '</span>' : '<span class="hash">sealed · ' + esc((i.sha256 || '').slice(0, 12)) + '…</span>') + '</td>' +
        '<td class="r"><a class="btn btn--ghost btn--sm" href="/reports/incident/' + i.id + '" target="_blank" rel="noopener">Report</a>' +
        (i.status === 'RECORDING' ? '<button type="button" class="btn btn--secondary btn--sm" data-seal="' + i.id + '">Seal now</button>' : '') + '</td></tr>';
    }).join('');
  }
  S.views.reports = {
    render: function (root) {
      root.innerHTML = '<div class="vwrap"><div class="st-head"><div><p class="eyebrow">Deterministic rules · no machine learning</p><h1>Reports</h1>' +
        '<p>Reports open as printable pages: use Print → Save as PDF to send them to the flight surgeon.</p></div></div>' +
        '<section class="section"><h2 class="section__title">Post-EVA reports</h2><div class="tbl-wrap"><table class="tbl"><thead><tr><th>EVA</th><th>Status</th><th>Started</th><th>Duration</th><th></th></tr></thead><tbody id="rep-missions"></tbody></table></div></section>' +
        '<section class="section"><h2 class="section__title">Black box · incidents</h2><div class="tbl-wrap"><table class="tbl"><thead><tr><th>#</th><th>Astronaut</th><th>Trigger</th><th>Event</th><th>Classification</th><th>Status</th><th></th></tr></thead><tbody id="rep-incidents"></tbody></table></div></section>' +
        '<section class="section"><h2 class="section__title">Monthly health reports</h2><div class="st-actions">' + S.state.crew.map(function (c) {
          return '<a class="btn btn--secondary" href="/reports/monthly/' + encodeURIComponent(c.id) + '" target="_blank" rel="noopener">' + icon('user') + esc(c.id + ' · ' + c.name) + '</a>';
        }).join('') + '</div></section>' +
        '<section class="section"><h2 class="section__title">Export data (CSV)</h2><div class="st-actions">' + TABLES.map(function (t) {
          return '<a class="btn btn--ghost" href="/api/export/' + t[0] + '" download>' + icon('download') + esc(t[1]) + '</a>';
        }).join('') + '</div></section></div>';
      root.firstElementChild.addEventListener('click', function (e) {
        var s = e.target.closest('[data-seal]');
        if (s) S.post('/api/incidents/' + s.getAttribute('data-seal') + '/seal', {}, 'Incident sealed (SHA-256)');
      });
      this.update();
    },
    update: function (kind) {
      var m = document.getElementById('rep-missions');
      if (!m) return;
      if (kind === 'tick') return;
      m.innerHTML = S.state.missions.map(function (x) {
        var dur = x.startedAt ? S.clock((x.endedAt || S.now()) - x.startedAt) : '—';
        var lv = { ACTIVE: null, ABORTED: 3, COMPLETED: 0, PLANNED: null }[x.status];
        return '<tr><td><b>' + esc(x.name) + '</b><br><span class="hash">' + esc(x.id) + '</span></td><td>' + (lv === null ? '<span class="chip" data-tone="accent">' + esc(x.status) + '</span>' : S.badge(lv, S.human(x.status))) + '</td>' +
          '<td class="mono">' + (x.startedAt ? S.date(x.startedAt) + ' ' + S.hm(x.startedAt) : '—') + '</td><td class="mono">' + dur + '</td>' +
          '<td class="r">' + (x.startedAt ? '<a class="btn btn--ghost btn--sm" href="/reports/mission/' + encodeURIComponent(x.id) + '" target="_blank" rel="noopener">Open report</a>' : '') + '</td></tr>';
      }).join('');
      document.getElementById('rep-incidents').innerHTML = incidentRows();
    },
  };

  /* =====================================================================
     BLACK BOX
     ===================================================================== */
  S.views.blackbox = {
    render: function (root) {
      root.innerHTML = '<div class="st-head"><div><p class="eyebrow">Like an aircraft flight data recorder</p><h1>Black box</h1>' +
        '<p>Every reading is recorded continuously; every event is chained with SHA-256 so any later edit is detectable. Incidents freeze T−30 min … T+15 min.</p></div>' +
        '<div class="st-actions"><button type="button" class="btn btn--primary" id="bb-verify">' + icon('shield') + 'Verify integrity</button>' +
        '<button type="button" class="btn btn--secondary" id="bb-refresh">' + icon('refresh') + 'Refresh</button></div></div>' +
        '<div id="bb-result"></div><div class="tbl-wrap"><table class="tbl"><thead><tr><th>#</th><th>Time</th><th>Event</th><th>Crew</th><th>Detail</th><th>Hash</th></tr></thead><tbody id="bb-rows"><tr><td colspan="6" class="subtle">Loading…</td></tr></tbody></table></div>';
      var load = function () {
        S.api('GET', '/api/blackbox?limit=200').then(function (list) {
          document.getElementById('bb-rows').innerHTML = list.map(function (e) {
            return '<tr><td class="mono">' + e.id + '</td><td class="mono">' + S.date(e.t) + ' ' + S.time(e.t) + '</td><td>' + esc(S.human(e.kind)) + '</td><td>' + esc(e.crewId || '—') + '</td>' +
              '<td class="hash" style="max-width:340px;overflow-wrap:anywhere">' + esc(e.payload) + '</td><td class="hash">' + esc(e.hash.slice(0, 16)) + '…</td></tr>';
          }).join('') || '<tr><td colspan="6" class="subtle">Empty.</td></tr>';
        });
      };
      root.querySelector('#bb-refresh').addEventListener('click', load);
      root.querySelector('#bb-verify').addEventListener('click', function () {
        S.api('GET', '/api/blackbox/verify').then(function (r) {
          document.getElementById('bb-result').innerHTML = '<p class="flag card" data-level="' + (r.ok ? 0 : 3) + '">' + icon(r.ok ? 'check' : 'octagon') +
            (r.ok ? ' Chain intact · ' + r.entries + ' events verified · last hash <span class="hash">' + esc(r.lastHash.slice(0, 24)) + '…</span>'
              : ' Chain BROKEN at event #' + r.brokenAt + ' — the record was altered after it was written.') + '</p>';
        });
      });
      load();
    },
  };

  /* =====================================================================
     SIMULATOR (demonstration)
     ===================================================================== */
  function simHTML() {
    var sim = S.state.sim || { scenarios: [], virtual: {}, active: {} };
    var urls = (S.state.info && S.state.info.urls) || [];
    return '<div class="card stack"><h2 class="section__title">Connect a phone</h2><p class="muted">On the same Wi-Fi as this PC, open:</p><div class="urls">' +
      (urls.length ? urls.map(function (u) { return '<span class="url">' + esc(u) + '/app/</span>'; }).join('') : '<span class="url">http://&lt;PC-IP&gt;/app/</span>') +
      '</div><p class="subtle">When a phone is linked, failures are injected on the phone itself (edge processing, voice alert, local black box) and reach the station like real data.</p></div>' +
      evaCrew().map(function (c) {
        var on = !!sim.virtual[c.id], phone = c.device && c.device.online, act = sim.active[c.id] || [];
        return '<section class="card stack"><div class="row-between"><div class="row">' + S.avatar(c, lvlOf(c)) + '<div><b class="tcard__name">' + esc(c.role + ' · ' + c.name) + '</b><br>' + deviceChip(c) + '</div></div>' +
          '<div class="row"><span class="subtle">Virtual suit</span><button type="button" class="switch" role="switch" aria-checked="' + on + '" data-virtual="' + esc(c.id) + '"' + (phone ? ' disabled title="The phone provides the telemetry"' : '') + '></button></div></div>' +
          '<div class="sim-grid">' + sim.scenarios.map(function (s) {
            return '<button type="button" class="sim-btn" data-inject="' + esc(c.id) + '" data-scenario="' + esc(s.id) + '" data-active="' + (act.indexOf(s.id) >= 0) + '"><b>' + esc(s.label) + '</b><span>' + esc(s.description) + '</span></button>';
          }).join('') + '</div>' +
          '<div class="st-actions"><button type="button" class="btn btn--secondary btn--sm" data-clear="' + esc(c.id) + '">' + icon('refresh') + 'Restore normal values</button></div></section>';
      }).join('');
  }
  S.views.simulator = {
    render: function (root) {
      root.innerHTML = '<div class="vwrap"><div class="st-head"><div><p class="eyebrow">Demonstration · no real sensors</p><h1>Simulator</h1>' +
        '<p>Virtual suits give telemetry to explorers without a phone. Inject a failure to show detection, voice alert, escalation and the black box.</p></div>' +
        '<div class="st-actions"><button type="button" class="btn btn--secondary" data-clear="">' + icon('refresh') + 'Restore all</button></div></div><div class="stack" id="sim-body"></div></div>';
      root.firstElementChild.addEventListener('click', function (e) {
        var v = e.target.closest('[data-virtual]');
        if (v && !v.disabled) {
          var on = v.getAttribute('aria-checked') !== 'true';
          S.post('/api/sim/virtual', { crewId: v.getAttribute('data-virtual'), on: on }, on ? 'Virtual suit on' : 'Virtual suit off');
          return;
        }
        var inj = e.target.closest('[data-inject]');
        if (inj) {
          S.post('/api/sim/inject', { crewId: inj.getAttribute('data-inject'), scenario: inj.getAttribute('data-scenario') }).then(function (j) {
            S.toast('Failure injected on the ' + j.target + ' of ' + S.who(inj.getAttribute('data-inject')), { icon: 'play', level: 2 });
          });
          return;
        }
        var cl = e.target.closest('[data-clear]');
        if (cl) S.post('/api/sim/clear', { crewId: cl.getAttribute('data-clear') }, 'Values returning to normal');
      });
      this.update();
    },
    update: function (kind) {
      var el = document.getElementById('sim-body');
      if (!el) return;
      if (kind === 'tick' && el._t && Date.now() - el._t < 3000) return;
      el._t = Date.now();
      if (!el.contains(document.activeElement)) el.innerHTML = simHTML();
    },
  };
})();
