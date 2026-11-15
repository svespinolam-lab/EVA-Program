/* EVA · Optional, private menstrual-cycle module (off by default).
   Button-based logging of period start, flow and symptoms; phase estimate;
   interpretation adjustments. Everything stays on this phone (App.storage
   "cycle"). The station only ever receives two anonymous offsets in the
   telemetry field "cal" (never the phase, dates or symptoms):
     luteal phase  ->  cal.temp = +0.3 (deg C), cal.hr = +3 (bpm)
   (expected physiological rise; the station widens the alert thresholds by
   that amount so a normal luteal rise is not flagged as fever or stress).
   Phase from the last logged start and the mean cycle length (last 6
   cycles between 21 and 40 days, default 28): day 1-5 menstrual, then
   follicular, ovulation window = (length - 14) +/- 1, then luteal until the
   next expected start + 7 days; later than that = unknown (no adjustment). */
(function () {
  'use strict';
  var App = window.App = window.App || {};
  var KEY = 'cycle', DAY = 864e5;
  var SYMPTOMS = ['Cramps', 'Headache', 'Fatigue', 'Bloating', 'Mood changes', 'Breast tenderness'];
  var FLOW = ['Light', 'Medium', 'Heavy'];

  function empty() { return { enabled: false, starts: [], days: {} }; }
  function load() { var d = App.storage && App.storage.get(KEY, null); return d && typeof d === 'object' && Array.isArray(d.starts) ? d : empty(); }
  function save(d) { if (App.storage) App.storage.set(KEY, d); }
  function dayKey(t) { var d = new Date(t); return d.getFullYear() + '-' + ('0' + (d.getMonth() + 1)).slice(-2) + '-' + ('0' + d.getDate()).slice(-2); }
  function midnight(t) { var d = new Date(t); d.setHours(0, 0, 0, 0); return d.getTime(); }

  function cycleLength(d) {
    d = d || load();
    var s = d.starts.slice().sort(function (a, b) { return a - b; }), gaps = [];
    for (var i = 1; i < s.length; i++) {
      var g = Math.round((s[i] - s[i - 1]) / DAY);
      if (g >= 21 && g <= 40) gaps.push(g);
    }
    gaps = gaps.slice(-6);
    return gaps.length ? Math.round(gaps.reduce(function (a, b) { return a + b; }, 0) / gaps.length) : 28;
  }

  function phase(t, d) {
    d = d || load();
    t = t || Date.now();
    if (!d.enabled || !d.starts.length) return null;
    var last = null;
    d.starts.forEach(function (s) { if (s <= t && (last === null || s > last)) last = s; });
    if (last === null) return null;
    var day = Math.floor((midnight(t) - midnight(last)) / DAY) + 1, len = cycleLength(d), ov = len - 14;
    if (day <= 5) return 'menstrual';
    if (day < ov - 1) return 'follicular';
    if (day <= ov + 1) return 'ovulatory';
    if (day <= len + 7) return 'luteal';
    return 'unknown';
  }

  function adjustments(t) {
    return phase(t) === 'luteal' ? { temp: 0.3, hr: 3 } : { temp: 0, hr: 0 };
  }

  var api = {
    SYMPTOMS: SYMPTOMS, FLOW: FLOW,
    isEnabled: function () { return !!load().enabled; },
    setEnabled: function (on) { var d = load(); d.enabled = !!on; save(d); },
    erase: function () { save(empty()); },
    logStart: function (t) {
      var d = load(), m = midnight(t || Date.now());
      if (d.starts.indexOf(m) < 0) d.starts.push(m);
      d.starts.sort(function (a, b) { return a - b; });
      d.starts = d.starts.slice(-24);
      save(d);
    },
    logDay: function (t, entry) {
      var d = load(), k = dayKey(t || Date.now()), cur = d.days[k] || {};
      if (entry && FLOW.indexOf(entry.flow) >= 0) cur.flow = entry.flow;
      if (entry && Array.isArray(entry.symptoms)) cur.symptoms = entry.symptoms.filter(function (s) { return SYMPTOMS.indexOf(s) >= 0; });
      d.days[k] = cur;
      var keys = Object.keys(d.days).sort();
      while (keys.length > 120) delete d.days[keys.shift()];
      save(d);
    },
    today: function () { return load().days[dayKey(Date.now())] || {}; },
    phase: phase, cycleLength: cycleLength, adjustments: adjustments,
  };

  function chip(kind, v, pressed) {
    return '<button type="button" class="chip" data-cyc="' + kind + '" data-v="' + v + '" aria-pressed="' + pressed + '">' + v + '</button>';
  }
  function sheetHTML() {
    var d = load(), today = api.today(), p = phase();
    var html = '<div class="cfg-sheet cycle"><div class="cfg-row"><span id="cyc-label">Track my cycle (private)</span>' +
      '<button type="button" class="switch" role="switch" aria-labelledby="cyc-label" aria-checked="' + d.enabled + '" data-cyc="toggle"></button></div>' +
      '<p class="muted">Off by default. Everything stays on this phone. The station only receives an anonymous adjustment of the temperature and heart-rate alert limits during the luteal phase.</p>';
    if (d.enabled) {
      html += '<p><b>Phase:</b> ' + (p ? p.charAt(0).toUpperCase() + p.slice(1) : 'log the start of your period') +
        (p === 'luteal' ? ' · alert limits adjusted +0.3 °C and +3 bpm' : '') + ' · cycle ' + cycleLength(d) + ' days</p>' +
        '<button type="button" class="btn btn--primary" data-cyc="start">Period started today</button>' +
        '<p class="field__label">Flow today</p><div class="cyc-row">' +
        FLOW.map(function (f) { return chip('flow', f, today.flow === f); }).join('') +
        '</div><p class="field__label">Symptoms today</p><div class="cyc-row">' +
        SYMPTOMS.map(function (s) { return chip('sym', s, (today.symptoms || []).indexOf(s) >= 0); }).join('') +
        '</div><button type="button" class="btn btn--ghost" data-cyc="erase">Delete all cycle data</button>';
    }
    return html + '</div>';
  }

  api.openSheet = function () {
    App.ui.sheet.open({
      title: 'Cycle tracking', html: sheetHTML(),
      onMount: function (b) {
        b.addEventListener('click', function (e) {
          var x = e.target.closest('[data-cyc]');
          if (!x) return;
          var a = x.getAttribute('data-cyc'), v = x.getAttribute('data-v'), t = api.today();
          if (a === 'toggle') api.setEnabled(!api.isEnabled());
          else if (a === 'start') { api.logStart(); App.ui.toast('Period start logged on this phone'); }
          else if (a === 'flow') api.logDay(null, { flow: v });
          else if (a === 'sym') {
            var s = (t.symptoms || []).slice(), i = s.indexOf(v);
            if (i >= 0) s.splice(i, 1); else s.push(v);
            api.logDay(null, { symptoms: s });
          } else if (a === 'erase') { api.erase(); App.ui.toast('Cycle data deleted from this phone'); }
          var host = b.querySelector('.cycle');
          if (host) host.outerHTML = sheetHTML();
        });
      },
    });
  };

  App.cycle = api;
})();