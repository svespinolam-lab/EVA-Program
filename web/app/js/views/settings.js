/* EVA · Settings drawer and sheets. Menu: IV operator, mission, export, reports, roles, voice, demo, and theme. Sheets: App.views.settings.open('export', 'reports', 'roles', 'voice', 'simulation'). Engine modules are optional so missing files do not break the UI. */
(function () {
  'use strict';
  var App = window.App;
  var C = App.config;
  var esc = App.dom.esc;

  var ROLES = ['EV1', 'EV2', 'IV'];
  var RATES = [
    { label: 'Slow', value: 0.85 },
    { label: 'Normal', value: C.voice.rate },
    { label: 'Fast', value: 1.3 },
  ];

  var drawerEl = null;
  var exportFormat = 'json';
  var simCrewId = null;
  var simScenarioId = null;
  var draftRoles = null;

  /* Utilities */
  function plural(n, one, many) { return n + ' ' + (n === 1 ? one : many); }
  function firstName(name) { return String(name || '').split(/\s+/)[0]; }
  function isEvaRole(role) { return /^EV/.test(role || ''); }
  function eventCount() { return App.blackbox && App.blackbox.count ? App.blackbox.count() : 0; }
  function incidents() { return (App.blackbox && App.blackbox.incidents && App.blackbox.incidents()) || []; }
  function voiceOn() { return !!App.store.get('voiceOn'); }
  function voiceSupported() { return !!App.voice && (!App.voice.isSupported || App.voice.isSupported()); }
  function scenarios() { return (App.telemetry && App.telemetry.scenarios) || []; }

  function activeIds(crewId) {
    if (!App.telemetry || !App.telemetry.activeScenarios) return [];
    return (App.telemetry.activeScenarios(crewId) || []).map(function (a) {
      return typeof a === 'string' ? a : a && (a.id || a.scenarioId);
    });
  }
  function activeTotal() {
    return App.crew.eva().reduce(function (n, m) { return n + activeIds(m.id).length; }, 0);
  }

  function themeNow() {
    var t = App.store.get('theme');
    if (t === 'light' || t === 'dark') return t;
    return window.matchMedia && window.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark';
  }

  function rateIndex() {
    var r = App.store.get('voiceRate') || C.voice.rate, best = 1;
    RATES.forEach(function (x, i) { if (Math.abs(x.value - r) < Math.abs(RATES[best].value - r)) best = i; });
    return best;
  }

  function rolesSummary() {
    return App.crew.all().slice().sort(function (a, b) {
      return ROLES.indexOf(a.role) - ROLES.indexOf(b.role);
    }).map(function (m) { return m.role + ' ' + firstName(m.name); }).join(' · ');
  }

  function duration(ms) {
    var s = Math.max(0, Math.round(ms / 1000)), m = Math.floor(s / 60);
    return m ? m + ' min ' + (s % 60) + ' s' : s + ' s';
  }
  function who(crewId) {
    var m = App.crew.byId(crewId);
    return m ? m.role + ' · ' + m.name : String(crewId || 'Crew');
  }
  function asText(v) { return typeof v === 'string' ? v : JSON.stringify(v, null, 2); }
  function note(text) { return '<p class="cfg-note">' + App.icon('info') + '<span>' + esc(text) + '</span></p>'; }

  /* Select an App.ui.segmented option */
  function markSeg(name, index) {
    App.dom.$$('[data-seg="' + name + '"]').forEach(function (b) {
      b.setAttribute('aria-checked', String(+b.getAttribute('data-value') === index));
    });
  }

  /* Voice shared by drawer and sheet */
  function voiceControls(p) {
    var ok = voiceSupported();
    return '<div class="cfg-row">' +
        '<span class="cfg-row__text"><span class="cfg-row__title" id="' + p + '-voice-label">Voice alerts</span>' +
        '<span class="cfg-row__sub">Announces issues and affected crew</span></span>' +
        '<button type="button" class="switch" role="switch" id="' + p + '-voice-switch" aria-checked="' + voiceOn() + '"' +
        ' aria-labelledby="' + p + '-voice-label" data-act="voice-toggle"' + (ok ? '' : ' disabled') + '></button>' +
      '</div>' +
      '<div class="field"><span class="field__label">Speed</span>' +
        App.ui.segmented({ name: p + '-rate', label: 'Voice speed', options: RATES.map(function (r) { return r.label; }), value: rateIndex() }) +
      '</div>' +
      '<button type="button" class="btn btn--secondary btn--block" id="' + p + '-voice-test" data-act="voice-test"' + (ok ? '' : ' disabled') + '>' +
        App.icon('volume') + 'Test voice</button>' +
      (ok ? '' : note('Speech is unavailable. Try Chrome or Safari.'));
  }

  function syncVoice() {
    App.dom.$$('[id$="-voice-switch"]').forEach(function (s) { s.setAttribute('aria-checked', String(voiceOn())); });
  }
  function toggleVoice() {
    if (App.shell && App.shell.setVoice) App.shell.setVoice(!voiceOn());   // Called during a tap
    syncVoice();
  }
  function testVoice() {
    if (!App.voice || !App.voice.test) {
      App.ui.toast('Voice is unavailable in this browser', { icon: 'info' });
      return;
    }
    App.voice.test();
  }
  function setRate(i) {
    var r = RATES[i];
    if (!r) return;
    App.store.set({ voiceRate: r.value });
    if (App.voice && App.voice.setRate) App.voice.setRate(r.value);
    markSeg('cfg-rate', i);
    markSeg('voice-rate', i);
  }

  /* Drawer */
  function listItem(id, act, icon, title, sub) {
    return '<button type="button" class="list-item cfg-item" id="' + id + '" data-act="' + act + '">' +
      '<span class="list-item__icon">' + App.icon(icon) + '</span>' +
      '<span class="list-item__text"><span class="list-item__title">' + esc(title) + '</span>' +
      '<span class="list-item__sub" data-sub>' + esc(sub) + '</span></span>' + App.icon('chevronRight') + '</button>';
  }

  function themeButton() {
    var dark = themeNow() === 'dark';
    return '<button type="button" class="cfg-theme" id="cfg-theme" data-act="theme" aria-label="' + (dark ? 'Switch to light theme' : 'Switch to dark theme') + '">' +
      '<span class="cfg-theme__icon">' + App.icon(dark ? 'moon' : 'sun') + '</span>' +
      '<span class="cfg-theme__text"><span class="cfg-theme__title">' + (dark ? 'Dark theme' : 'Light theme') + '</span>' +
      '<span class="cfg-theme__sub">Tap to change</span></span></button>';
  }

  function demoStatus() {
    var n = activeTotal();
    return n ? plural(n, 'active simulated fault', 'active simulated faults') : 'No simulated faults';
  }

  function reportsSub() {
    var n = incidents().length;
    return n ? plural(n, 'incident', 'incidents') : 'No incidents';
  }

  function renderDrawer(el) {
    drawerEl = el;
    var iv = App.crew.iv();
    var m = C.mission;
    var delay = (m.earthDelay || {})[m.destination] || '—';

    var operator = iv
      ? '<button type="button" class="cfg-operator" id="cfg-operator" data-act="operator">' + App.ui.avatar(iv, null) +
          '<span class="cfg-operator__text"><span class="eyebrow">Operator inside</span>' +
          '<span class="cfg-operator__name">' + esc(iv.name) + '</span>' +
          '<span class="cfg-operator__sub">' + esc('IV · ' + iv.specialty) + '</span></span>' + App.icon('chevronRight') + '</button>'
      : '<p class="cfg-note">' + App.icon('info') + '<span>No IV assigned. Use Assign roles to choose an operator.</span></p>';

    el.innerHTML = '<div class="cfg" id="cfg">' +
      '<div class="cfg-head"><h2 class="cfg-head__title">Settings</h2>' +
        '<button type="button" class="icon-btn" id="cfg-close" data-close-drawer aria-label="Close settings">' + App.icon('close') + '</button></div>' +
      operator +
      '<div class="cfg-mission">' +
        '<p class="eyebrow">Mission</p>' +
        '<p class="cfg-mission__name">' + esc(m.name + ' · ' + m.destination) + '</p>' +
        '<p class="cfg-mission__site">' + esc(m.site + ' · EVA ' + m.evaNumber) + '</p>' +
        '<p class="cfg-mission__delay">' + App.icon('signal') + '<span>Earth delay: <strong class="mono">' + esc(delay) + '</strong> · Crew decisions are autonomous</span></p>' +
      '</div>' +
      '<nav class="list cfg-list" aria-label="Data and crew">' +
        listItem('cfg-export', 'open-export', 'download', 'Export data', plural(eventCount(), 'event', 'events') + ' in the blackbox') +
        listItem('cfg-reports', 'open-reports', 'fileText', 'Reports', reportsSub()) +
        listItem('cfg-roles', 'open-roles', 'users', 'Assign roles', rolesSummary()) +
        listItem('cfg-link', 'open-link', 'link', 'Station link', App.link ? (function (s) { return s.state + ' · ' + s.queued + ' queued'; })(App.link.status()) : 'Not available') +
      '</nav>' +
      '<section class="cfg-section" aria-label="Voice alerts"><h3 class="eyebrow">Voice alerts</h3>' + voiceControls('cfg') + '</section>' +
      '<section class="cfg-section" aria-label="Demo"><h3 class="eyebrow">Demo</h3>' +
        '<p class="cfg-row__sub" id="cfg-demo-status">' + esc(demoStatus()) + '</p>' +
        '<div class="cfg-actions">' +
          '<button type="button" class="btn btn--secondary" id="cfg-sim" data-act="open-simulation">' + App.icon('play') + 'Simulate a fault</button>' +
          '<button type="button" class="btn btn--ghost" id="cfg-reset" data-act="reset-all">' + App.icon('refresh') + 'Restore normal values</button>' +
        '</div></section>' +
      '<footer class="cfg-foot">' + themeButton() +
        '<p class="cfg-foot__meta">' + esc(C.app.name + ' ' + C.app.version) + '</p>' +
        '<p class="cfg-foot__meta">Data is stored only on this device.</p>' +
      '</footer></div>';

    el.querySelector('#cfg').addEventListener('click', onDrawerClick);
  }

  function onDrawerClick(e) {
    var seg = e.target.closest('[data-seg]');
    if (seg) { setRate(+seg.getAttribute('data-value')); return; }
    var b = e.target.closest('[data-act]');
    if (!b) return;
    var act = b.getAttribute('data-act');
    if (act.indexOf('open-') === 0) { open(act.slice(5)); return; }
    if (act === 'operator') {
      var iv = App.crew.iv();
      App.shell.closeDrawer();
      if (iv) App.shell.selectCrew(iv.id, 'profile');
    } else if (act === 'voice-toggle') {
      toggleVoice();
    } else if (act === 'voice-test') {
      testVoice();
    } else if (act === 'reset-all') {
      resetAll();
    } else if (act === 'theme') {
      App.shell.setTheme(themeNow() === 'dark' ? 'light' : 'dark');
      b.outerHTML = themeButton();
      var nb = drawerEl && drawerEl.querySelector('#cfg-theme');
      if (nb) nb.focus({ preventScroll: true });
    }
  }

  function resetAll() {
    if (!App.telemetry || !App.telemetry.clearScenarios) {
      App.ui.toast('Simulator unavailable', { icon: 'info' });
      return;
    }
    App.telemetry.clearScenarios();
    App.ui.toast('Normal values restored', { icon: 'refresh' });
    var st = document.getElementById('cfg-demo-status');
    if (st) st.textContent = demoStatus();
  }

  /* Export data sheet */
  function exportText() {
    if (!App.blackbox) return '';
    var fn = exportFormat === 'csv' ? App.blackbox.exportCSV : App.blackbox.exportJSON;
    return fn ? asText(fn()) : '';
  }

  function download(text) {
    var stamp = new Date().toISOString().slice(0, 10);
    var type = exportFormat === 'csv' ? 'text/csv' : 'application/json';
    var url = URL.createObjectURL(new Blob([text], { type: type + ';charset=utf-8' }));
    var a = document.createElement('a');
    a.href = url;
    a.download = 'eva-blackbox-' + stamp + '.' + exportFormat;
    document.body.appendChild(a);
    a.click();
    a.remove();
    setTimeout(function () { URL.revokeObjectURL(url); }, 1500);
    App.ui.toast('File downloaded', { icon: 'download' });
  }

  function sheetExport() {
    var n = eventCount();
    var canDownload = window.self === window.top;   // Downloads may be blocked in the published preview
    var html = '<div class="cfg-sheet" id="sheet-export">' +
      '<p class="muted">The blackbox stores ' + esc(plural(n, 'event', 'events')) + ' on this device: suit readings, alerts, and check-ins.</p>' +
      App.ui.segmented({ name: 'exp-format', label: 'Format', options: ['JSON', 'CSV'], value: exportFormat === 'csv' ? 1 : 0 }) +
      '<div class="field"><label class="field__label" for="exp-preview">Preview</label>' +
        '<textarea class="input cfg-preview" id="exp-preview" readonly spellcheck="false"></textarea></div>' +
      '<div class="cfg-actions cfg-actions--split">' +
        '<button type="button" class="btn btn--primary" id="exp-copy" data-act="copy">' + App.icon('copy') + 'Copy</button>' +
        (canDownload ? '<button type="button" class="btn btn--secondary" id="exp-download" data-act="download">' + App.icon('download') + 'Download file</button>' : '') +
      '</div>' +
      (canDownload ? '' : note('Downloads are blocked in this preview. Copy and paste the text instead.')) +
      '<hr class="divider">' +
      '<button type="button" class="btn btn--danger btn--block" id="exp-clear" data-act="clear"' + (App.blackbox ? '' : ' disabled') + '>' + App.icon('trash') + 'Clear blackbox</button>' +
      '</div>';

    App.ui.sheet.open({
      title: 'Export data',
      html: html,
      onMount: function (body) {
        var box = body.querySelector('#sheet-export');
        var area = body.querySelector('#exp-preview');
        area.value = App.blackbox ? exportText() : 'Blackbox unavailable.';
        box.addEventListener('click', function (e) {
          var seg = e.target.closest('[data-seg]');
          if (seg) {
            exportFormat = seg.getAttribute('data-value') === '1' ? 'csv' : 'json';
            markSeg('exp-format', exportFormat === 'csv' ? 1 : 0);
            area.value = exportText();
            return;
          }
          var b = e.target.closest('[data-act]');
          if (!b) return;
          var act = b.getAttribute('data-act');
          if (act === 'copy') {
            App.ui.copyText(area.value, area).then(function (ok) {
              App.ui.toast(ok ? 'Copied' : 'Select and copy the text', { icon: ok ? 'check' : 'info' });
            });
          } else if (act === 'download') {
            download(area.value);
          } else if (act === 'clear') {
            App.ui.confirm({
              title: 'Clear blackbox',
              message: 'Delete ' + plural(eventCount(), 'event', 'events') + ' from this device? This cannot be undone.',
              confirmLabel: 'Delete',
              danger: true,
            }).then(function (yes) {
              if (yes && App.blackbox && App.blackbox.clear) {
                App.blackbox.clear();
                App.ui.toast('Blackbox cleared', { icon: 'trash' });
              }
              sheetExport();
            });
          }
        });
      },
    });
  }

  /* Reports sheet */
  function reportText(list) {
    var counts = [0, 0, 0, 0];
    list.forEach(function (i) { counts[i.level] = (counts[i.level] || 0) + 1; });
    var lines = [
      'INCIDENT REPORT · ' + C.app.name,
      'Mission ' + C.mission.name + ' · EVA ' + C.mission.evaNumber + ' · ' + C.mission.destination,
      'Generated: ' + App.fmt.date(new Date()) + ' ' + App.fmt.time(Date.now()),
      'Incidents: ' + list.length + ' (critical ' + counts[3] + ' · high risk ' + counts[2] + ' · caution ' + counts[1] + ')',
      '',
    ];
    list.forEach(function (i) {
      lines.push('[' + App.levels.meta(i.level).label.toUpperCase() + '] ' + who(i.crewId));
      lines.push('  ' + (i.message || i.key));
      lines.push('  Start ' + App.fmt.time(i.start) + (i.end ? ' · End ' + App.fmt.time(i.end) + ' (' + duration(new Date(i.end) - new Date(i.start)) + ')' : ' · Ongoing'));
      if (i.peakValueText) lines.push('  Peak: ' + i.peakValueText);
      lines.push('');
    });
    return lines.join('\n');
  }

  function sheetReports() {
    var list = incidents().slice().sort(function (a, b) { return new Date(b.start) - new Date(a.start); });
    if (!list.length) {
      App.ui.sheet.open({
        title: 'Reports',
        html: '<div class="cfg-sheet" id="sheet-reports">' +
          App.ui.empty('fileText', 'No incidents logged. Simulate a fault to see its report.') +
          '<button type="button" class="btn btn--secondary btn--block" id="rep-sim" data-act="sim">' + App.icon('play') + 'Simulate a fault</button></div>',
        onMount: function (body) {
          body.querySelector('#sheet-reports').addEventListener('click', function (e) {
            if (e.target.closest('[data-act="sim"]')) sheetSim();
          });
        },
      });
      return;
    }

    var counts = [0, 0, 0, 0], byCrew = {};
    list.forEach(function (i) {
      counts[i.level] = (counts[i.level] || 0) + 1;
      var c = byCrew[i.crewId] || (byCrew[i.crewId] = { n: 0, max: 0 });
      c.n++;
      c.max = Math.max(c.max, i.level);
    });

    var stats = [3, 2, 1].map(function (l) {
      return '<div class="cfg-stat" data-level="' + l + '"><span class="cfg-stat__num">' + counts[l] + '</span>' +
        '<span class="cfg-stat__label">' + esc(App.levels.meta(l).label) + '</span></div>';
    }).join('');

    var crewRows = Object.keys(byCrew).map(function (id) {
      var m = App.crew.byId(id), c = byCrew[id];
      return '<li class="cfg-crewsum">' + (m ? App.ui.avatar(m, c.max) : '') +
        '<span class="cfg-crewsum__text"><span class="cfg-crewsum__name">' + esc(who(id)) + '</span>' +
        '<span class="cfg-crewsum__sub">' + esc(plural(c.n, 'incident', 'incidents')) + '</span></span>' + App.ui.badge(c.max) + '</li>';
    }).join('');

    var items = list.slice(0, 60).map(function (i) {
      var time = App.fmt.time(i.start) + (i.end ? ' → ' + App.fmt.time(i.end) : '');
      var meta = [who(i.crewId)];
      if (i.peakValueText) meta.push('Peak ' + i.peakValueText);
      if (i.end) meta.push(duration(new Date(i.end) - new Date(i.start)));
      return '<li class="cfg-incident" data-level="' + i.level + '">' +
        '<div class="cfg-incident__head">' + App.ui.badge(i.level) +
          '<span class="cfg-incident__time">' + esc(time) + '</span>' +
          (i.end ? '' : '<span class="cfg-live">Ongoing</span>') + '</div>' +
        '<p class="cfg-incident__what">' + esc(i.message || i.key) + '</p>' +
        '<p class="cfg-incident__meta">' + esc(meta.join(' · ')) + '</p></li>';
    }).join('');

    var text = reportText(list);
    App.ui.sheet.open({
      title: 'Reports',
      html: '<div class="cfg-sheet" id="sheet-reports">' +
        '<div class="cfg-stats">' + stats + '</div>' +
        '<section class="cfg-sheet__block" aria-label="By crew member"><h3 class="eyebrow">By crew member</h3><ul class="cfg-crewsums">' + crewRows + '</ul></section>' +
        '<section class="cfg-sheet__block" aria-label="Incidents"><h3 class="eyebrow">Incidents · newest first</h3><ul class="cfg-incidents">' + items + '</ul>' +
        (list.length > 60 ? '<p class="cfg-row__sub">Showing the 60 most recent. The copied report includes all incidents.</p>' : '') + '</section>' +
        '<button type="button" class="btn btn--primary btn--block" id="rep-copy" data-act="copy">' + App.icon('copy') + 'Copy report</button>' +
        '<details class="cfg-details" id="rep-details"><summary>View text report</summary>' +
          '<textarea class="input cfg-preview" id="rep-text" readonly spellcheck="false"></textarea></details>' +
        '</div>',
      onMount: function (body) {
        var area = body.querySelector('#rep-text');
        area.value = text;
        body.querySelector('#sheet-reports').addEventListener('click', function (e) {
          if (!e.target.closest('[data-act="copy"]')) return;
          body.querySelector('#rep-details').open = true;
          App.ui.copyText(text, area).then(function (ok) {
            App.ui.toast(ok ? 'Report copied' : 'Select and copy the text', { icon: ok ? 'check' : 'info' });
          });
        });
      },
    });
  }

  /* Assign roles sheet */
  function rolesError(roles) {
    var ids = Object.keys(roles), count = { EV1: 0, EV2: 0, IV: 0 };
    ids.forEach(function (id) { count[roles[id]] = (count[roles[id]] || 0) + 1; });
    if (count.EV1 > 1) return 'EV1 is assigned twice. Each EVA role needs a unique crew member.';
    if (count.EV2 > 1) return 'EV2 is assigned twice. Each EVA role needs a unique crew member.';
    if (!count.IV) return 'Assign an IV operator to use the app inside the spacecraft.';
    if (!count.EV1 && !count.EV2) return 'Assign at least one EVA crew member (EV1 or EV2).';
    return '';
  }

  function sheetRoles() {
    draftRoles = Object.assign({}, App.store.get('roles'));
    var rows = App.crew.all().map(function (m) {
      return '<div class="cfg-role">' +
        '<div class="cfg-role__who">' + App.ui.avatar(m, null) +
          '<span class="cfg-role__text"><span class="cfg-role__name">' + esc(m.name) + '</span>' +
          '<span class="cfg-role__spec">' + esc(m.specialty) + '</span></span></div>' +
        App.ui.segmented({ name: 'role-' + m.id, label: 'Role for ' + m.name, options: ROLES, value: ROLES.indexOf(draftRoles[m.id]) }) +
      '</div>';
    }).join('');

    App.ui.sheet.open({
      title: 'Assign roles',
      html: '<div class="cfg-sheet" id="sheet-roles">' +
        '<p class="muted">EV1 and EV2 go outside with suit monitoring. IV stays inside and uses this app.</p>' +
        rows +
        '<p class="cfg-error" id="roles-error" role="alert" hidden></p>' +
        '<button type="button" class="btn btn--primary btn--block" id="roles-save" data-act="save">' + App.icon('check') + 'Save roles</button>' +
        '</div>',
      onMount: function (body) {
        var err = body.querySelector('#roles-error');
        body.querySelector('#sheet-roles').addEventListener('click', function (e) {
          var seg = e.target.closest('[data-seg]');
          if (seg) {
            var name = seg.getAttribute('data-seg'), i = +seg.getAttribute('data-value');
            draftRoles[name.replace(/^role-/, '')] = ROLES[i];
            markSeg(name, i);
            var msg = rolesError(draftRoles);
            err.textContent = msg;
            err.hidden = !msg;
            return;
          }
          if (!e.target.closest('[data-act="save"]')) return;
          var problem = rolesError(draftRoles);
          if (problem) {
            err.textContent = problem;
            err.hidden = false;
            return;
          }
          App.store.set({ roles: Object.assign({}, draftRoles) });   // app.js refreshes the crew
          if (App.blackbox && App.blackbox.log) App.blackbox.log({ type: 'system', level: 0, msg: 'Roles: ' + rolesSummary() });
          App.ui.sheet.close();
          App.ui.toast('Roles updated', { icon: 'users' });
        });
      },
    });
  }

  /* Voice alerts sheet */
  function sheetVoice() {
    App.ui.sheet.open({
      title: 'Voice alerts',
      html: '<div class="cfg-sheet" id="sheet-voice">' +
        '<p class="muted">EVA announces issues and affected crew, even while you view another screen.</p>' +
        voiceControls('voice') + '</div>',
      onMount: function (body) {
        body.querySelector('#sheet-voice').addEventListener('click', function (e) {
          var seg = e.target.closest('[data-seg]');
          if (seg) { setRate(+seg.getAttribute('data-value')); return; }
          var b = e.target.closest('[data-act]');
          if (!b) return;
          if (b.getAttribute('data-act') === 'voice-toggle') toggleVoice();
          else if (b.getAttribute('data-act') === 'voice-test') testVoice();
        });
      },
    });
  }

  /* Fault simulation sheet */
  function sheetSim() {
    var eva = App.crew.eva();
    var list = scenarios();
    if (!App.telemetry || !eva.length || !list.length) {
      App.ui.sheet.open({
        title: 'Simulate a fault',
        html: App.ui.empty('play', !eva.length ? 'No EVA crew assigned. Assign EV1 or EV2 first.' : 'Simulator unavailable.'),
      });
      return;
    }
    var sel = App.crew.selected();
    if (!simCrewId || !eva.some(function (m) { return m.id === simCrewId; })) {
      simCrewId = sel && isEvaRole(sel.role) ? sel.id : eva[0].id;
    }
    var active = activeIds(simCrewId);

    var options = list.map(function (sc) {
      var metric = C.metrics[sc.metric];
      return '<button type="button" class="cfg-scenario" role="radio" id="sim-sc-' + esc(sc.id) + '" data-scenario="' + esc(sc.id) + '" aria-checked="' + (sc.id === simScenarioId) + '">' +
        '<span class="cfg-scenario__icon">' + App.icon(metric ? metric.icon : 'triangle') + '</span>' +
        '<span class="cfg-scenario__text"><span class="cfg-scenario__label">' + esc(sc.label) + '</span>' +
        '<span class="cfg-scenario__desc">' + esc(sc.description || '') + '</span></span>' +
        (active.indexOf(sc.id) >= 0 ? '<span class="badge badge--live">Active</span>' : '') + '</button>';
    }).join('');

    App.ui.sheet.open({
      title: 'Simulate a fault',
      html: '<div class="cfg-sheet" id="sheet-simulation">' +
        '<p class="muted">Inject a suit fault to see how EVA classifies and announces it.</p>' +
        '<div class="field"><span class="field__label">Crew member</span>' +
          App.ui.segmented({
            name: 'sim-crew', label: 'Crew member',
            options: eva.map(function (m) { return m.role + ' · ' + firstName(m.name); }),
            value: eva.map(function (m) { return m.id; }).indexOf(simCrewId),
          }) + '</div>' +
        '<div class="field"><span class="field__label">Scenario</span>' +
          '<div class="cfg-scenarios" role="radiogroup" aria-label="Scenario">' + options + '</div></div>' +
        '<div class="cfg-actions cfg-actions--split">' +
          '<button type="button" class="btn btn--primary" id="sim-inject" data-act="inject"' + (simScenarioId ? '' : ' disabled') + '>' + App.icon('play') + 'Inject fault</button>' +
          '<button type="button" class="btn btn--secondary" id="sim-reset" data-act="reset">' + App.icon('refresh') + 'Reset all</button>' +
        '</div>' +
        (voiceOn() ? '' : note('Enable voice using the speaker button at the top left to hear alerts.')) +
        '</div>',
      onMount: function (body) {
        var box = body.querySelector('#sheet-simulation');
        box.addEventListener('click', function (e) {
          var seg = e.target.closest('[data-seg]');
          if (seg) {
            simCrewId = eva[+seg.getAttribute('data-value')].id;
            sheetSim();                       // Refresh active faults for the selected crew member
            return;
          }
          var sc = e.target.closest('[data-scenario]');
          if (sc) {
            simScenarioId = sc.getAttribute('data-scenario');
            App.dom.$$('[data-scenario]', box).forEach(function (b) { b.setAttribute('aria-checked', String(b === sc)); });
            box.querySelector('#sim-inject').disabled = false;
            return;
          }
          var b = e.target.closest('[data-act]');
          if (!b) return;
          if (b.getAttribute('data-act') === 'inject' && simScenarioId) {
            var m = App.crew.byId(simCrewId);
            var chosen = list.filter(function (s) { return s.id === simScenarioId; })[0];
            App.telemetry.inject(simCrewId, simScenarioId);
            App.ui.sheet.close();
            App.ui.toast('Simulated fault for ' + (m ? m.role : '') + ': ' + (chosen ? chosen.label : simScenarioId), { icon: 'play', level: 2 });
            App.shell.selectCrew(simCrewId, 'diagnosis');
          } else if (b.getAttribute('data-act') === 'reset') {
            resetAll();
            sheetSim();
          }
        });
      },
    });
  }

  /* Close the drawer before opening a sheet */
  var SHEETS = { export: sheetExport, reports: sheetReports, roles: sheetRoles, voice: sheetVoice, simulation: sheetSim,
    link: function () { if (App.link && App.link.openSheet) App.link.openSheet(); } };

  function open(name) {
    if (App.shell && App.shell.closeDrawer) App.shell.closeDrawer();
    if (SHEETS[name]) SHEETS[name]();
  }

  /* Refresh counters while the drawer is open */
  App.bus.on('blackbox', function () {
    if (!drawerEl || !drawerEl.classList.contains('is-open')) return;
    var ex = drawerEl.querySelector('#cfg-export [data-sub]');
    var rp = drawerEl.querySelector('#cfg-reports [data-sub]');
    if (ex) ex.textContent = plural(eventCount(), 'event', 'events') + ' in the blackbox';
    if (rp) rp.textContent = reportsSub();
  });
  App.bus.on('state', function (p) {
    if (p && p.patch && 'voiceOn' in p.patch) syncVoice();
  });

  App.views = App.views || {};
  App.views.settings = { renderDrawer: renderDrawer, open: open };
})();
