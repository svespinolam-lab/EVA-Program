/* EVA · Startup and navigation. Splash, EVA clock, crew selector, risk alert, navigation, drawer, and theme. Each js/views/<name>.js exposes App.views.<name> with render(rootEl), optional onEvent(name, payload) for live updates, and optional destroy() for timer cleanup. */
(function () {
  'use strict';
  var App = window.App;
  var C = App.config;
  var $ = App.dom.$, $$ = App.dom.$$, esc = App.dom.esc;

  App.views = App.views || {};
  var VIEWS = ['log', 'diagnosis', 'profile'];
  var TITLES = { log: 'Log', diagnosis: 'Diagnosis', profile: 'Profile' };
  var current = null;
  var currentView = null;

  function safe(fn) {
    try { return fn(); } catch (err) { console.error('[EVA]', err); }
  }
  function riskOf(id) {
    return App.risk && App.risk.get ? App.risk.get(id) : null;
  }
  function levelOf(id) {
    var a = riskOf(id);
    return a && typeof a.level === 'number' ? a.level : null;
  }

  /* Hydrate HTML icons: <span data-icon="..."> */
  function hydrateIcons(root) {
    $$('[data-icon]', root).forEach(function (el) {
      el.outerHTML = App.icon(el.getAttribute('data-icon'));
    });
  }

  /* Theme */
  function applyTheme(theme) {
    var root = document.documentElement;
    if (theme === 'light' || theme === 'dark') root.setAttribute('data-theme', theme);
    else root.removeAttribute('data-theme');
    var meta = document.querySelector('meta[name="theme-color"]');
    var bg = getComputedStyle(root).getPropertyValue('--bg').trim();
    if (meta && bg) meta.setAttribute('content', bg);
  }
  function setTheme(theme) {
    App.store.set({ theme: theme });
    applyTheme(theme);
  }

  /* Voice */
  function setVoiceUI(on) {
    var b = $('#btn-voice');
    b.setAttribute('aria-pressed', String(on));
    b.setAttribute('aria-label', on ? 'Mute voice alerts' : 'Enable voice alerts');
    b.innerHTML = App.icon(on ? 'volume' : 'volumeOff');
  }
  /* Enable or mute voice from a tap, as required by browsers. */
  function setVoice(on) {
    if (!App.voice) {
      App.ui.toast('Voice is unavailable in this browser', { icon: 'info' });
      return false;
    }
    var ok = App.voice.setEnabled(on);
    if (on && ok === false) {
      App.ui.toast('Speech is unavailable. Try Chrome or Safari.', { icon: 'info' });
      return false;
    }
    App.store.set({ voiceOn: on });
    setVoiceUI(on);
    App.ui.toast(on ? 'Voice alerts enabled' : 'Voice alerts muted', { icon: on ? 'volume' : 'volumeOff' });
    return true;
  }

  /* Splash screen */
  function drawStars(canvas) {
    if (!canvas || !canvas.getContext) return;
    var dpr = Math.min(window.devicePixelRatio || 1, 2);
    var w = canvas.clientWidth || window.innerWidth;
    var h = canvas.clientHeight || window.innerHeight;
    canvas.width = w * dpr;
    canvas.height = h * dpr;
    var ctx = canvas.getContext('2d');
    ctx.scale(dpr, dpr);
    ctx.fillStyle = getComputedStyle(document.documentElement).getPropertyValue('--star').trim() || '#dce8ff';
    var seed = 11;
    function rnd() { seed = (seed * 9301 + 49297) % 233280; return seed / 233280; }
    var n = Math.round((w * h) / 5200);
    for (var i = 0; i < n; i++) {
      ctx.globalAlpha = 0.2 + rnd() * 0.6;
      ctx.beginPath();
      ctx.arc(rnd() * w, rnd() * h, rnd() < 0.08 ? 1.3 : 0.5 + rnd() * 0.5, 0, Math.PI * 2);
      ctx.fill();
    }
  }

  function runSplash() {
    var splash = $('#splash');
    if (!splash) return;
    $('#splash-name').textContent = C.app.name;
    $('#splash-tagline').textContent = C.app.tagline;
    var logo = $('#splash-logo');
    logo.src = C.app.logo;
    logo.onerror = function () { logo.style.visibility = 'hidden'; };
    drawStars($('#splash-stars'));

    var steps = App.crew.eva().map(function (m) { return 'Suit link ' + m.role + ' · ' + m.name; });
    steps.push('Risk classifier active', 'Blackbox recording');
    var list = $('#splash-steps');
    list.innerHTML = steps.map(function (s) {
      return '<li class="splash__step">' + App.icon('check') + '<span>' + esc(s) + '</span></li>';
    }).join('');
    var items = $$('.splash__step', list);
    var per = Math.max(180, (C.app.splashMs - 700) / Math.max(1, items.length));
    items.forEach(function (li, i) {
      setTimeout(function () { li.classList.add('is-on'); }, 250 + i * per);
    });

    var finished = false;
    function finish() {
      if (finished) return;
      finished = true;
      splash.classList.add('is-done');
      setTimeout(function () { splash.hidden = true; }, 650);
    }
    setTimeout(finish, C.app.splashMs);
    splash.addEventListener('click', finish);
  }

  /* EVA clock */
  function tickClock() {
    var el = $('#eva-clock');
    if (el) el.textContent = 'EVA ' + C.mission.evaNumber + ' · T+' + App.fmt.clock(Date.now() - App.store.get('evaStart'));
    var dot = $('#live-dot');
    var live = App.telemetry && App.telemetry.isRunning ? App.telemetry.isRunning() : false;
    if (dot) dot.setAttribute('data-state', live ? 'on' : 'off');
  }

  /* Crew selector */
  function renderCrew() {
    var sel = App.crew.selected();
    var eva = App.crew.eva();
    var html = eva.map(function (m) {
      var lvl = levelOf(m.id);
      var meta = App.levels.meta(lvl);
      return '<button type="button" class="crew-chip" data-crew="' + esc(m.id) + '" data-level="' + App.ui.lvlAttr(lvl) + '"' +
        ' aria-pressed="' + (!!sel && sel.id === m.id) + '" aria-label="' + esc(m.role + ', ' + m.name + ', ' + meta.label) + '">' +
        App.ui.avatar(m, lvl) +
        '<span class="crew-chip__text"><span class="crew-chip__name">' + esc(m.name) + '</span>' +
        '<span class="crew-chip__meta"><span class="crew-chip__role">' + esc(m.role) + '</span>' +
        '<span class="crew-chip__state">' + esc(meta.short) + '</span></span></span></button>';
    }).join('');
    $('#crew').innerHTML = html || '<p class="subtle">No EVA crew assigned. Open Settings to assign roles.</p>';
  }

  function selectCrew(id, view) {
    App.store.set({ selectedCrewId: id });
    renderCrew();
    if (view && view !== current) go(view);
    else render(current);
  }

  /* Risk alert: most severe finding across the crew */
  function topAlert() {
    var best = null;
    App.crew.eva().forEach(function (m) {
      var a = riskOf(m.id);
      var f = a && a.findings && a.findings[0];
      if (f && (!best || f.level > best.finding.level)) best = { member: m, finding: f };
    });
    return best;
  }

  function renderAlert() {
    var slot = $('#alert-slot');
    var t = topAlert();
    if (!t || t.finding.level < 1) {
      slot.innerHTML = '';
      slot.removeAttribute('data-sig');
      return;
    }
    var f = t.finding;
    var meta = App.levels.meta(f.level);
    var what = f.message + (f.valueText ? ' · ' + f.valueText : '');
    var sig = f.level + '|' + t.member.id + '|' + f.key;
    if (slot.getAttribute('data-sig') === sig) {
      var w = slot.querySelector('.alert-banner__what');   // Same alert: update only the value
      if (w) w.textContent = what;
      return;
    }
    slot.setAttribute('data-sig', sig);
    slot.innerHTML = '<button type="button" class="alert-banner" data-level="' + f.level + '" data-crew="' + esc(t.member.id) + '">' +
      App.icon(meta.icon) +
      '<span class="alert-banner__text"><span class="eyebrow alert-banner__who">' + esc(meta.label + ' · ' + t.member.role + ' ' + t.member.name) + '</span>' +
      '<span class="alert-banner__what">' + esc(what) + '</span></span>' + App.icon('chevronRight') + '</button>';
  }

  /* Diagnosis badge for high or critical risk */
  function renderTabBadge() {
    var max = App.levels.max(App.crew.eva().map(function (m) { return levelOf(m.id); }));
    var tab = $('.tab[data-view="diagnosis"]');
    var b = tab.querySelector('.tab__badge');
    if (max >= 2) {
      if (!b) {
        b = document.createElement('span');
        b.className = 'tab__badge';
        tab.appendChild(b);
      }
      b.setAttribute('data-level', String(max));
    } else if (b) {
      b.remove();
    }
  }

  /* View navigation */
  function viewFromHash() {
    var h = (location.hash || '').replace('#', '');
    return VIEWS.indexOf(h) >= 0 ? h : 'log';
  }

  function go(name) {
    if (VIEWS.indexOf(name) < 0) name = 'log';
    if (location.hash !== '#' + name) location.hash = name;   // Triggers hashchange, then render
    else render(name);
  }

  function render(name, keepScroll) {
    if (currentView && currentView.destroy) safe(function () { currentView.destroy(); });
    current = name;
    currentView = App.views[name] || null;
    App.store.set({ view: name });
    $('#view-title').textContent = TITLES[name];
    $$('.tab').forEach(function (t) {
      if (t.getAttribute('data-view') === name) t.setAttribute('aria-current', 'page');
      else t.removeAttribute('aria-current');
    });

    var main = $('#main');
    main.style.animation = 'none';
    void main.offsetWidth;            // Restart the entry animation
    main.style.animation = '';
    if (!currentView || !currentView.render) {
      main.innerHTML = App.ui.empty('info', 'This screen is not ready yet.');
    } else {
      var ok = safe(function () { currentView.render(main); return true; });
      if (!ok) main.innerHTML = App.ui.empty('triangle', 'Unable to display this screen. Check the console.');
    }
    if (!keepScroll) window.scrollTo(0, 0);
  }

  /* Settings drawer */
  function openDrawer() {
    var d = $('#drawer');
    if (App.views.settings && App.views.settings.renderDrawer) safe(function () { App.views.settings.renderDrawer(d); });
    d.inert = false;
    d.classList.add('is-open');
    $('#drawer-scrim').classList.add('is-open');
    $('#btn-menu').setAttribute('aria-expanded', 'true');
    setTimeout(function () {
      var f = d.querySelector('button, a[href], input');
      if (f) f.focus({ preventScroll: true });
    }, 90);
  }
  function closeDrawer() {
    var d = $('#drawer');
    if (!d.classList.contains('is-open')) return;
    d.classList.remove('is-open');
    $('#drawer-scrim').classList.remove('is-open');
    d.inert = true;
    $('#btn-menu').setAttribute('aria-expanded', 'false');
    $('#btn-menu').focus({ preventScroll: true });
  }

  /* View API */
  App.shell = {
    go: go,
    render: function () { render(current, true); },
    selectCrew: selectCrew,
    openDrawer: openDrawer,
    closeDrawer: closeDrawer,
    setTheme: setTheme,
    setVoice: setVoice,
    refresh: function () { renderCrew(); renderAlert(); renderTabBadge(); },
    current: function () { return current; },
  };

  /* Startup */
  function boot() {
    document.documentElement.lang = 'en';
    applyTheme(App.store.get('theme'));
    hydrateIcons(document);
    setVoiceUI(false);
    runSplash();

    // Engine: keep the UI working if a file is missing
    ['blackbox', 'risk', 'voice', 'telemetry'].forEach(function (mod) {
      var m = App[mod];
      if (m && m.init) safe(function () { m.init(); });
    });
    if (App.telemetry && App.telemetry.start) safe(function () { App.telemetry.start(); });
    // Link with EVA Station (WebSocket): telemetry up, alerts / instructions / abort down
    if (App.link && App.link.init) safe(function () { App.link.init(); });

    App.shell.refresh();
    tickClock();
    setInterval(tickClock, 1000);
    render(viewFromHash());

    /* System events update the header and current view */
    App.bus.on('risk', function (p) {
      if (p && p.changed) { renderCrew(); renderTabBadge(); }
      renderAlert();
    });
    App.bus.on('state', function (p) {
      var k = p.patch;
      if ('roles' in k || 'profiles' in k) { App.shell.refresh(); render(current, true); }
    });
    ['telemetry', 'sensor', 'scenario', 'risk', 'alert', 'checkin', 'blackbox'].forEach(function (evt) {
      App.bus.on(evt, function (p) {
        if (currentView && currentView.onEvent) safe(function () { currentView.onEvent(evt, p); });
      });
    });

    /* Interaction */
    window.addEventListener('hashchange', function () { render(viewFromHash()); });
    $('#crew').addEventListener('click', function (e) {
      var chip = e.target.closest('[data-crew]');
      if (chip) selectCrew(chip.getAttribute('data-crew'));
    });
    $('#alert-slot').addEventListener('click', function (e) {
      var b = e.target.closest('[data-crew]');
      if (b) selectCrew(b.getAttribute('data-crew'), 'diagnosis');
    });
    $('#btn-voice').addEventListener('click', function () { setVoice(!App.store.get('voiceOn')); });
    $('#btn-menu').addEventListener('click', openDrawer);
    $('#drawer-scrim').addEventListener('click', closeDrawer);
    $('#drawer').addEventListener('click', function (e) {
      if (e.target.closest('[data-close-drawer]')) closeDrawer();
    });
    $('#sheet-close').addEventListener('click', function () { App.ui.sheet.close(); });
    $('#sheet-scrim').addEventListener('click', function () { App.ui.sheet.close(); });
    document.addEventListener('keydown', function (e) {
      if (e.key === 'Escape' && !App.ui.sheet.isOpen()) closeDrawer();
    });
  }

  boot();
})();
