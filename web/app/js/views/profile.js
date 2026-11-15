/* EVA · Profile: selected crew identity, mission, medical history, and pre-mission baseline. Edit name, age, sex, and specialty locally via App.store profiles; App.crew.all merges overrides with config.js. */
(function () {
  'use strict';
  var App = window.App;
  var C = App.config;
  var esc = App.dom.esc;

  var editing = false;   // Whether the identity form is open
  var lastRoot = null;   // Main element containing this view

  var SEX_OPTIONS = ['Female', 'Male', 'Other', 'Prefer not to say'];
  var ROLE_LABEL = {
    EV1: 'EV1 · spacewalk',
    EV2: 'EV2 · spacewalk',
    IV: 'IV · operator inside',
  };
  var ROLE_CHIP = { EV1: 'EV1 · outside', EV2: 'EV2 · outside', IV: 'IV · inside' };

  /* Pre-mission measurement order; live marks metrics compared against EVA telemetry. */
  var BASE = [
    { key: 'hr',     label: 'Resting HR',     icon: 'heart',       unit: 'bpm',       dec: 0, live: true },
    { key: 'bp',     label: 'Blood pressure', icon: 'gauge',       unit: 'mmHg',      dec: 0, live: true },
    { key: 'spo2',   label: 'SpO₂',             icon: 'droplet',     unit: '%',         dec: 0, live: true },
    { key: 'temp',   label: 'Temperature',      icon: 'thermometer', unit: '°C',        dec: 1, live: true },
    { key: 'weight', label: 'Weight',             icon: 'user',        unit: 'kg',        dec: 0 },
    { key: 'height', label: 'Height',         icon: 'arrowUp',     unit: 'cm',        dec: 0 },
    { key: 'vo2max', label: 'VO₂ max',         icon: 'wind',        unit: 'mL/kg/min', dec: 0 },
    { key: 'bmi',    label: 'BMI',              icon: 'activity',    unit: 'kg/m²',     dec: 1 },
  ];

  /* Data */
  function isEva(m) { return /^EV/.test(m.role); }

  function riskLevel(id) {
    var a = App.risk && App.risk.get ? App.risk.get(id) : null;
    return a && typeof a.level === 'number' ? a.level : null;
  }

  function latest(id) {
    return App.telemetry && App.telemetry.latest ? App.telemetry.latest(id) : null;
  }

  function initialsOf(name) {
    var parts = String(name).trim().split(/\s+/).filter(Boolean);
    if (!parts.length) return '?';
    if (parts.length === 1) return parts[0].slice(0, 2).toUpperCase();
    return (parts[0].charAt(0) + parts[parts.length - 1].charAt(0)).toUpperCase();
  }

  /* Read date-only strings as local dates to avoid shifting to the previous day. */
  function localDate(value) {
    var m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(String(value || ''));
    return m ? new Date(+m[1], +m[2] - 1, +m[3]) : value;
  }

  function hasValue(v) { return v !== null && v !== undefined && v !== '' && !(typeof v === 'number' && isNaN(v)); }

  /* Components */
  function kvRow(key, val, flag) {
    return '<div class="kv__row"><dt class="kv__key">' + esc(key) + '</dt>' +
      '<dd class="kv__val' + (flag ? ' pf-kv__flag' : '') + '">' +
      (flag ? App.icon('info') : '') + '<span>' + esc(hasValue(val) ? val : '—') + '</span></dd></div>';
  }

  function field(id, label, control) {
    return '<div class="field"><label class="field__label" for="' + id + '">' + esc(label) + '</label>' + control + '</div>';
  }

  function baseValue(b, def) {
    if (def.key === 'bp') return b.bpSys && b.bpDia ? Math.round(b.bpSys) + '/' + Math.round(b.bpDia) : '—';
    if (def.key === 'bmi') return b.weight && b.height ? App.fmt.num(b.weight / Math.pow(b.height / 100, 2), 1) : '—';
    return App.fmt.num(b[def.key], def.dec);
  }

  function diffText(diff, dec) {
    var r = Number(diff.toFixed(dec || 0));
    if (r === 0) return '±0';
    return (r > 0 ? '+' : '−') + App.fmt.num(Math.abs(r), dec);
  }

  /* Tile footer: current reading versus baseline */
  function liveText(m, def) {
    if (!def.live || !isEva(m)) return 'Baseline';
    var d = latest(m.id);
    var b = m.baseline || {};
    if (!d) return 'No live reading';
    if (def.key === 'bp') {
      if (!d.bpSys || !d.bpDia) return 'No measurement this EVA';
      return 'Now ' + Math.round(d.bpSys) + '/' + Math.round(d.bpDia) + ' (' + diffText(d.bpSys - b.bpSys, 0) + ')';
    }
    var v = d[def.key];
    if (!hasValue(v) || !hasValue(b[def.key])) return 'No live reading';
    return 'Now ' + App.fmt.num(v, def.dec) + ' (' + diffText(v - b[def.key], def.dec) + ')';
  }

  /* Sections */
  function hero(m) {
    var eva = isEva(m);
    var lvl = eva ? riskLevel(m.id) : null;
    var state = eva
      ? '<span class="pf-hero__risk" id="pf-risk">' + App.ui.badge(lvl) + '</span>' +
        '<button type="button" class="btn btn--ghost btn--sm" id="pf-go-diag" data-act="diag">View diagnosis' + App.icon('chevronRight') + '</button>'
      : '<span class="pf-hero__note">' + App.icon('suit') + 'No suit telemetry</span>';
    return '<section class="pf-hero" aria-label="Crew member">' +
      App.ui.avatar(m, lvl, true) +
      '<div class="pf-hero__text">' +
        '<h2 class="pf-hero__name">' + esc(m.name) + '</h2>' +
        '<p class="pf-hero__meta"><span class="pf-role">' + esc(ROLE_CHIP[m.role] || m.role) + '</span>' +
        '<span class="pf-hero__spec">' + esc(m.specialty) + '</span></p>' +
        '<div class="pf-hero__state">' + state + '</div>' +
      '</div></section>';
  }

  function editForm(m) {
    var hasOverride = !!(App.store.get('profiles') || {})[m.id];
    var sexOpts = SEX_OPTIONS.slice();
    if (m.sex && sexOpts.indexOf(m.sex) < 0) sexOpts.unshift(m.sex);
    return '<form class="pf-form" id="pf-form" novalidate>' +
      field('pf-name', 'Name', '<input class="input" id="pf-name" name="name" type="text" autocomplete="off" maxlength="40" required value="' + esc(m.name) + '">') +
      '<div class="pf-form__row">' +
        field('pf-age', 'Age', '<input class="input" id="pf-age" name="age" type="number" inputmode="numeric" min="18" max="80" step="1" required value="' + esc(m.age) + '">') +
        field('pf-sex', 'Sex', '<select class="input" id="pf-sex" name="sex">' + sexOpts.map(function (o) {
          return '<option' + (o === m.sex ? ' selected' : '') + '>' + esc(o) + '</option>';
        }).join('') + '</select>') +
      '</div>' +
      field('pf-specialty', 'Specialty', '<input class="input" id="pf-specialty" name="specialty" type="text" autocomplete="off" maxlength="48" value="' + esc(m.specialty) + '">') +
      '<p class="pf-error" id="pf-error" role="alert" hidden></p>' +
      '<div class="pf-form__actions">' +
        '<button type="button" class="btn btn--secondary" id="pf-cancel" data-act="cancel">Cancel</button>' +
        '<button type="submit" class="btn btn--primary" id="pf-save">' + App.icon('check') + 'Save</button>' +
      '</div>' +
      (hasOverride
        ? '<button type="button" class="btn btn--ghost btn--sm pf-form__restore" id="pf-restore" data-act="restore">' + App.icon('refresh') + 'Restore original data</button>'
        : '') +
      '</form>';
  }

  function identity(m) {
    var action = editing ? '' :
      '<button type="button" class="btn btn--ghost btn--sm" id="pf-edit" data-act="edit">' + App.icon('edit') + 'Edit</button>';
    var body = editing ? editForm(m) :
      '<dl class="kv pf-kv">' +
        kvRow('Name', m.name) +
        kvRow('Crew ID', m.id) +
        kvRow('Age', hasValue(m.age) ? m.age + ' years' : '') +
        kvRow('Sex', m.sex) +
        kvRow('Role', ROLE_LABEL[m.role] || m.role) +
        kvRow('Specialty', m.specialty) +
        kvRow('Blood type', m.bloodType) +
        kvRow('Mission', C.mission.name) +
        kvRow('Destination', C.mission.destination) +
        kvRow('Site', C.mission.site) +
        kvRow('Duration', durationText()) +
      '</dl>';
    return '<section class="section" aria-label="Identity and mission">' +
      App.ui.sectionHead({ eyebrow: 'Crew profile', title: 'Identity and mission', action: action }) +
      '<div class="card">' + body + '</div></section>';
  }

  /* Planned EVA (station value when linked) and mission duration */
  function durationText() {
    var sm = App.store.get('stationMission') || {};
    var h = Number(sm.plannedH) > 0 ? Number(sm.plannedH) : C.mission.plannedH;
    var parts = [];
    if (h) parts.push('EVA ' + h + ' h planned');
    if (C.mission.durationDays) parts.push('mission ' + C.mission.durationDays + ' days');
    return parts.join(' · ');
  }

  // The clinical record shipped in config.js is example data, never a real person.
  var EXAMPLE_HINT = 'Example record from the mission configuration: replace it with the real one before a mission.';

  function medical(m) {
    var md = m.medical || {};
    var allergy = md.allergies && !/^(none\b|no\s)/i.test(md.allergies);
    return '<section class="section" aria-label="Medical history">' +
      App.ui.sectionHead({ eyebrow: 'History', title: 'Medical history', hint: EXAMPLE_HINT }) +
      '<div class="card"><dl class="kv pf-kv">' +
        kvRow('Allergies', md.allergies, allergy) +
        kvRow('Conditions', md.conditions) +
        kvRow('Medication', md.medication) +
        kvRow('Supplements', md.supplements) +
        kvRow('Surgeries', md.surgeries) +
        kvRow('EVA hours', hasValue(md.evaHours) ? md.evaHours + ' h' : '') +
      '</dl></div></section>';
  }

  function contacts(m) {
    var list = ((m.medical || {}).contacts) || [];
    var body = list.length ? '<dl class="kv pf-kv">' + list.map(function (c) {
      return kvRow(c.relation || 'Contact', c.name + (c.channel ? ' · ' + c.channel : ''));
    }).join('') + '</dl>' : '<p class="muted">No emergency contact recorded.</p>';
    return '<section class="section" aria-label="Emergency contacts">' +
      App.ui.sectionHead({ eyebrow: 'Emergency', title: 'Emergency contacts', hint: 'Reached through mission control. ' + EXAMPLE_HINT }) +
      '<div class="card">' + body + '</div></section>';
  }

  function baseline(m) {
    var b = m.baseline || {};
    var hint = (b.measuredAt ? 'Measured on ' + App.fmt.date(localDate(b.measuredAt)) + '. ' : '') +
      'Reference values for comparison with live readings.';
    var tiles = BASE.map(function (def) {
      return '<div class="tile pf-tile">' +
        '<p class="tile__head">' + App.icon(def.icon) + '<span class="eyebrow">' + esc(def.label) + '</span></p>' +
        '<p class="value pf-tile__value"><span class="pf-tile__num">' + esc(baseValue(b, def)) + '</span>' +
        '<span class="value__unit">' + esc(def.unit) + '</span></p>' +
        '<p class="tile__foot pf-tile__now" data-pf-now="' + def.key + '">' + esc(liveText(m, def)) + '</p>' +
      '</div>';
    }).join('');
    return '<section class="section" aria-label="Pre-mission measurements">' +
      App.ui.sectionHead({ eyebrow: 'Baseline', title: 'Pre-mission measurements', hint: hint }) +
      '<div class="tiles">' + tiles + '</div></section>';
  }

  /* Actions */
  function saveProfiles(next, toastText, logText, crewId) {
    editing = false;
    App.store.set({ profiles: next });           // app.js refreshes the view and crew
    if (document.getElementById('pf-form') && lastRoot) render(lastRoot);   // Fallback if no listener refreshed the view
    App.ui.toast(toastText, { icon: 'check' });
    if (App.blackbox && App.blackbox.log) {
      App.blackbox.log({ type: 'system', crewId: crewId, level: 0, msg: logText });
    }
  }

  function onSubmit(e) {
    e.preventDefault();
    var form = e.currentTarget;
    var m = App.crew.selected();
    if (!m) return;
    var nameEl = form.querySelector('#pf-name');
    var ageEl = form.querySelector('#pf-age');
    var name = nameEl.value.trim().replace(/\s+/g, ' ');
    var age = parseInt(ageEl.value, 10);
    var sex = form.querySelector('#pf-sex').value;
    var specialty = form.querySelector('#pf-specialty').value.trim();
    var err = form.querySelector('#pf-error');

    var msg = '', bad = null;
    if (name.length < 2) { msg = 'Enter a name with at least 2 letters.'; bad = nameEl; }
    else if (isNaN(age) || age < 18 || age > 80) { msg = 'Age must be between 18 and 80.'; bad = ageEl; }
    if (msg) {
      err.textContent = msg;
      err.hidden = false;
      bad.setAttribute('aria-invalid', 'true');
      bad.focus();
      return;
    }

    var prev = App.store.get('profiles') || {};
    var next = Object.assign({}, prev);
    next[m.id] = Object.assign({}, prev[m.id] || {}, {
      name: name,
      initials: initialsOf(name),
      age: age,
      sex: sex,
      specialty: specialty || m.specialty,
    });
    saveProfiles(next, 'Profile updated', 'Profile edited: ' + name, m.id);
  }

  function restore() {
    var m = App.crew.selected();
    if (!m) return;
    var prev = App.store.get('profiles') || {};
    var next = Object.assign({}, prev);
    delete next[m.id];
    saveProfiles(next, 'Original data restored', 'Profile restored: ' + m.id, m.id);
  }

  function onClick(e) {
    var b = e.target.closest('[data-act]');
    if (!b) return;
    var act = b.getAttribute('data-act');
    if (act === 'edit') {
      editing = true;
      render(lastRoot);
      var nameEl = document.getElementById('pf-name');
      if (nameEl) nameEl.focus({ preventScroll: true });
    } else if (act === 'cancel') {
      editing = false;
      render(lastRoot);
      var editBtn = document.getElementById('pf-edit');
      if (editBtn) editBtn.focus({ preventScroll: true });
    } else if (act === 'restore') {
      restore();
    } else if (act === 'diag') {
      App.shell.go('diagnosis');
    }
  }

  /* View */
  function render(root) {
    if (!root) return;
    lastRoot = root;
    var m = App.crew.selected();
    if (!m) {
      root.innerHTML = App.ui.empty('user', 'No crew configured.');
      return;
    }
    root.innerHTML = '<div class="pf" id="pf">' + hero(m) + identity(m) + medical(m) + contacts(m) + baseline(m) + '</div>';
    var wrap = root.querySelector('#pf');
    wrap.addEventListener('click', onClick);
    var form = wrap.querySelector('#pf-form');
    if (form) form.addEventListener('submit', onSubmit);
  }

  /* Live data: update only changed text */
  function onEvent(name, p) {
    if (!lastRoot || editing || !p) return;
    var m = App.crew.selected();
    if (!m || p.crewId !== m.id) return;

    if (name === 'telemetry' || name === 'sensor') {
      BASE.forEach(function (def) {
        if (!def.live) return;
        var el = lastRoot.querySelector('[data-pf-now="' + def.key + '"]');
        if (!el) return;
        var t = liveText(m, def);
        if (el.textContent !== t) el.textContent = t;
      });
    } else if (name === 'risk') {
      var lvl = p.assessment && typeof p.assessment.level === 'number' ? p.assessment.level : null;
      var attr = App.ui.lvlAttr(lvl);
      var av = lastRoot.querySelector('.pf-hero .avatar');
      if (av && av.getAttribute('data-level') !== attr) av.setAttribute('data-level', attr);
      var slot = lastRoot.querySelector('#pf-risk');
      var badge = slot && slot.querySelector('.badge');
      if (slot && (!badge || badge.getAttribute('data-level') !== attr)) slot.innerHTML = App.ui.badge(lvl);
    }
  }

  function destroy() {
    editing = false;
    lastRoot = null;
  }

  App.views = App.views || {};
  App.views.profile = { render: render, onEvent: onEvent, destroy: destroy };
})();
