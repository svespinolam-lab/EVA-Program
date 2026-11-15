/* EVA · Spoken alerts identify the affected crew role, finding, and value using speechSynthesis. Severe alerts also beep via Web Audio and vibrate if supported. Browsers require a user gesture: App.shell.setVoice calls App.voice.setEnabled. Listens to risk.js alert events; configured through config.voice. */
(function () {
  'use strict';
  var App = window.App = window.App || {};
  var C = App.config;

  var synth = null;
  try { synth = window.speechSynthesis || null; } catch (e) { synth = null; }

  var enabled = false;
  var chosenVoice = null;
  var queue = [];            // Pending alerts: {text, priority}
  var speaking = null;       // Currently spoken alert
  var watchdog = null;
  var audioCtx = null;
  var repeatTimer = null;
  var lastCritical = {};     // Crew ID and metric key to last spoken time

  /* Spoken units */
  var SPOKEN_UNITS = {
    'bpm': 'beats per minute',
    '%': 'percent',
    '°C': 'degrees',
    'mmHg': 'millimeters of mercury',
    'min': 'minutes',
    'psi': 'psi',
  };

  /* Voice selection: config.voice.lang, then any English voice */
  function pickVoice() {
    if (!synth || !synth.getVoices) return;
    var voices = [];
    try { voices = synth.getVoices() || []; } catch (e) { voices = []; }
    if (!voices.length) return;
    var want = String(C.voice.lang || 'en-US').toLowerCase().replace('_', '-');
    function norm(v) { return String(v.lang || '').toLowerCase().replace('_', '-'); }
    chosenVoice =
      voices.filter(function (v) { return norm(v) === want && v.localService; })[0] ||
      voices.filter(function (v) { return norm(v) === want; })[0] ||
      voices.filter(function (v) { return norm(v).indexOf('en') === 0 && v.localService; })[0] ||
      voices.filter(function (v) { return norm(v).indexOf('en') === 0; })[0] ||
      null;
  }

  function makeUtterance(text) {
    var u = new window.SpeechSynthesisUtterance(text);
    u.lang = chosenVoice ? chosenVoice.lang : (C.voice.lang || 'en-US');
    if (chosenVoice) u.voice = chosenVoice;
    u.rate = Number(App.store.get('voiceRate')) || C.voice.rate || 1;
    u.pitch = C.voice.pitch || 1;
    return u;
  }

  /* Alert queue */
  function finish(item) {
    if (speaking !== item) return;
    speaking = null;
    clearTimeout(watchdog);
    setTimeout(pump, 120);
  }

  function pump() {
    if (!enabled || !synth || speaking || !queue.length) return;
    var item = queue.shift();
    speaking = item;
    try {
      var u = makeUtterance(item.text);
      u.onend = function () { finish(item); };
      u.onerror = function () { finish(item); };
      synth.speak(u);
      // Release the queue even when browsers omit completion events
      clearTimeout(watchdog);
      watchdog = setTimeout(function () { finish(item); }, 6000 + item.text.length * 90);
    } catch (e) {
      finish(item);
    }
  }

  /* Speak once from a tap even when alerts are disabled */
  function speakOnce(text) {
    if (!api.isSupported()) return false;
    try {
      synth.cancel();
      speaking = null;
      queue = [];
      synth.speak(makeUtterance(text));
      return true;
    } catch (e) {
      return false;
    }
  }

  /* Beep and vibration */
  function ensureAudio() {
    if (audioCtx) return audioCtx;
    try {
      var Ctx = window.AudioContext || window.webkitAudioContext;
      if (Ctx) audioCtx = new Ctx();
    } catch (e) { audioCtx = null; }
    return audioCtx;
  }

  function tone(freq, startAt, dur) {
    var osc = audioCtx.createOscillator();
    var gain = audioCtx.createGain();
    osc.type = 'sine';
    osc.frequency.value = freq;
    gain.gain.setValueAtTime(0.0001, startAt);
    gain.gain.exponentialRampToValueAtTime(0.14, startAt + 0.02);
    gain.gain.exponentialRampToValueAtTime(0.0001, startAt + dur);
    osc.connect(gain);
    gain.connect(audioCtx.destination);
    osc.start(startAt);
    osc.stop(startAt + dur + 0.02);
  }

  /* Level 2: one tone; level 3: two descending tones */
  function beep(level) {
    try {
      if (!audioCtx) return;
      if (audioCtx.state === 'suspended' && audioCtx.resume) audioCtx.resume();
      var t = audioCtx.currentTime + 0.01;
      if (level >= 3) { tone(988, t, 0.18); tone(740, t + 0.24, 0.22); }
      else { tone(880, t, 0.16); }
    } catch (e) { /* Audio unavailable */ }
  }

  function vibrate(level) {
    try {
      if (navigator.vibrate) navigator.vibrate(level >= 3 ? [180, 90, 180] : 120);
    } catch (e) { /* Vibration unavailable */ }
  }

  /* Alert text */
  function spokenRole(role) {
    // Keep the role compact so alerts remain brief.
    return String(role || '');
  }

  function spokenValue(f) {
    if (!f || f.value === null || f.value === undefined) return '';
    var m = C.metrics[f.key];
    if (!m) return '';
    if (f.key === 'bp') {
      var parts = String(f.valueText || '').split(' ')[0].split('/');
      return parts.length === 2 ? parts[0] + ' over ' + parts[1] + ' ' + SPOKEN_UNITS.mmHg : '';
    }
    var num = App.fmt.num(f.value, Math.min(m.decimals || 0, 1));
    return num + ' ' + (SPOKEN_UNITS[m.unit] || m.unit);
  }

  function capitalize(s) { return s ? s.charAt(0).toUpperCase() + s.slice(1) : s; }

  function alertText(member, f) {
    var who = member ? spokenRole(member.role) + ': ' : '';
    var val = spokenValue(f);
    var levelWord = capitalize(App.levels.meta(f.level).voice);
    var what = String(f.message || f.label || '').toLowerCase().replace(/co₂/g, 'CO2');
    return levelWord + '. ' + who + what + (val ? ', ' + val : '') + '.';
  }

  /* Repeat critical alerts while active */
  function repeatCriticals() {
    if (!enabled || !App.risk) return;
    var every = (C.voice.repeatCriticalEverySec || 30) * 1000;
    var now = Date.now();
    App.crew.eva().forEach(function (m) {
      var a = App.risk.get(m.id);
      if (!a) return;
      a.findings.forEach(function (f) {
        if (f.level < 3) return;
        var k = m.id + '|' + f.key;
        if (now - (lastCritical[k] || 0) < every) return;
        lastCritical[k] = now;
        beep(3);
        api.say(alertText(m, f), { priority: 3 });
      });
    });
  }

  /* Public API */
  var api = {
    init: function () {
      pickVoice();
      try {
        if (synth && synth.addEventListener) synth.addEventListener('voiceschanged', pickVoice);
        else if (synth) synth.onvoiceschanged = pickVoice;
      } catch (e) { /* No action needed */ }

      App.bus.on('alert', function (p) {
        if (!enabled || !p || !p.finding) return;
        var f = p.finding;
        if (f.level < (C.voice.minLevelToSpeak || 1)) return;
        var member = p.member || App.crew.byId(p.crewId);
        if (f.level >= 2) { beep(f.level); vibrate(f.level); }
        if (f.level >= 3) lastCritical[p.crewId + '|' + f.key] = Date.now();
        api.say(alertText(member, f), { priority: f.level });
      });
    },

    isSupported: function () {
      return !!(synth && window.SpeechSynthesisUtterance);
    },

    /* Call from a tap; returns false when speech is unsupported. */
    setEnabled: function (on) {
      if (!on) {
        enabled = false;
        queue = [];
        speaking = null;
        clearTimeout(watchdog);
        clearInterval(repeatTimer);
        repeatTimer = null;
        try { if (synth) synth.cancel(); } catch (e) { /* No action needed */ }
        return true;
      }
      if (!api.isSupported()) return false;
      ensureAudio();
      try { if (audioCtx && audioCtx.state === 'suspended') audioCtx.resume(); } catch (e) { /* No action needed */ }
      pickVoice();
      enabled = true;
      // Speaking during the tap unlocks speech on iPhone
      try { synth.cancel(); } catch (e) { /* No action needed */ }
      queue = [];
      speaking = null;
      api.say('Voice alerts enabled.', { priority: 1 });
      clearInterval(repeatTimer);
      repeatTimer = setInterval(repeatCriticals, 5000);
      return true;
    },

    isEnabled: function () { return enabled; },

    /* Queue text with priority 0..3; critical alerts interrupt lower-priority speech. */
    say: function (text, opts) {
      if (!enabled || !synth || !text) return false;
      var priority = (opts && opts.priority) || 0;
      if (queue.some(function (q) { return q.text === text; })) return true;
      if (speaking && speaking.text === text) return true;
      var item = { text: text, priority: priority };
      var i = 0;
      while (i < queue.length && queue[i].priority >= priority) i++;
      queue.splice(i, 0, item);
      if (speaking && priority >= 3 && priority > speaking.priority) {
        speaking = null;
        clearTimeout(watchdog);
        try { synth.cancel(); } catch (e) { /* No action needed */ }
        setTimeout(pump, 80);
        return true;
      }
      pump();
      return true;
    },

    /* Test speech from a tap, even when alerts are disabled */
    test: function () {
      ensureAudio();
      beep(2);
      var text = 'Voice test. EVA alerts sound like this.';
      return enabled ? api.say(text, { priority: 2 }) : speakOnce(text);
    },

    /* Speak the current assessment using the Repeat alert button */
    speakAssessment: function (crewId) {
      var member = App.crew.byId(crewId);
      var a = App.risk ? App.risk.get(crewId) : null;
      var who = member ? spokenRole(member.role) + ': ' : '';
      var text;
      if (!a || a.level === null) {
        text = who + 'No suit data yet.';
      } else if (!a.findings.length) {
        text = 'Nominal. ' + who + 'all readings in range.';
      } else {
        text = alertText(member, a.findings[0]);
      }
      return enabled ? api.say(text, { priority: 2 }) : speakOnce(text);
    },

    setRate: function (rate) {
      var r = Math.max(0.5, Math.min(2, Number(rate) || 1));
      App.store.set({ voiceRate: r });
      return r;
    },

    beep: beep,
  };

  App.voice = api;
})();
