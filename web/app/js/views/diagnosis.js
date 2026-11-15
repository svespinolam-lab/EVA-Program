/* EVA · Diagnosis for the selected crew member: summary and findings; mental check-in; physical health versus pre-mission baseline; suit readings versus nominal ranges; recent blackbox incidents. IV crew see mental health only. Live updates preserve focus and scroll. Styles: css/views/diagnosis.css (.dx-). */
(function () {
  'use strict';
  var App = window.App = window.App || {};
  App.views = App.views || {};
  var C = App.config;

  /* Human-readable level summaries (0..3) */
  var POSTURE = ['No risk detected', 'Monitor closely', 'Act soon', 'Act now'];
  var GROUP_LABEL = { bio: 'Physical health', suit: 'Suit', mental: 'Mental health', link: 'Link' };
  var GROUP_OK = { bio: 'Vital signs in range.', suit: 'Life support in range.' };
  var MENTAL_OK = 'No signs of fatigue or stress.';

  var st = null;   // State while the view is open

  /* Utilities */
  function esc(s) { return App.dom.esc(s); }
  function isNum(v) { return typeof v === 'number' && !isNaN(v); }
  function lvlAttr(l) { return App.ui.lvlAttr(l); }
  function toMs(t) { return typeof t === 'number' ? t : (t ? Date.parse(t) : NaN); }
  function agoText(t) { var ms = toMs(t); return isNum(ms) ? App.fmt.ago(ms) : '—'; }
  function timeText(t) { var ms = toMs(t); return isNum(ms) ? App.fmt.time(ms) : '—'; }
  function q(sel) { return st ? st.root.querySelector(sel) : null; }
  function setText(el, text) { if (el && el.textContent !== text) el.textContent = text; }
  function setHTML(el, html) {
    if (el && el._dxHTML !== html) { el.innerHTML = html; el._dxHTML = html; }
  }
  function withPeriod(s) { s = String(s || ''); return /[.!?…]$/.test(s) ? s : s + '.'; }
  function capitalize(s) { return s.charAt(0).toUpperCase() + s.slice(1); }
  /* Signed difference: +18, −0.4, ±0 */
  function signed(n, d) {
    if (!isNum(n)) return '—';
    var r = Number(n.toFixed(d || 0));
    if (r === 0) return '±0';
    return (r > 0 ? '+' : '−') + App.fmt.num(Math.abs(r), d || 0);
  }

  /* Engine data with fallbacks for missing modules */
  function getRisk(id) {
    try { return App.risk && App.risk.get ? App.risk.get(id) : null; } catch (e) { return null; }
  }
  function getData(id) {
    try { return App.telemetry && App.telemetry.latest ? App.telemetry.latest(id) : null; } catch (e) { return null; }
  }
  function isConnected(id, key) {
    if (!App.telemetry || typeof App.telemetry.isConnected !== 'function') return true;
    try { return App.telemetry.isConnected(id, key) !== false; } catch (e) { return true; }
  }
  function metricValue(key, data, id) {
    if (!data) return null;
    if (key === 'bp') { var manualBP=manualValues(id); return isNum(manualBP.bpSys) ? manualBP.bpSys : null; } // Manual cuff or external BLE cuff entry
    if (!isConnected(id, key)) return null;
    return isNum(data[key]) ? data[key] : null;
  }
  function activeFindings(a) {
    return (a && a.findings ? a.findings : []).filter(function (f) { return f && isNum(f.level) && f.level >= 1; });
  }
  function findingFor(a, key) {
    return activeFindings(a).filter(function (f) { return f.key === key; })[0] || null;
  }
  /* Metric level from configured thresholds and engine findings */
  function rowLevel(key, value, a) {
    if (value === null) return null;
    var l = App.levels.classify(key, value);
    var f = findingFor(a, key);
    if (f && (l === null || f.level > l)) l = f.level;
    return l;
  }

  /* 1 · Summary */
  function heroTitle(a) {
    if (!a || !isNum(a.level)) return 'No suit data';
    return POSTURE[a.level] || App.levels.meta(a.level).label;
  }
  function summaryText(a) {
    if (!a) return 'Waiting for the first suit reading.';
    return a.summary || (isNum(a.level) ? App.levels.meta(a.level).label : 'No suit signal.');
  }
  function updatedText(a) {
    if (!a) return 'Waiting for data';
    return a.updatedAt ? 'Updated ' + agoText(a.updatedAt) : 'Live';
  }
  function countText(a) {
    var n = activeFindings(a).length;
    return n === 0 ? 'None' : n === 1 ? '1 active' : n + ' active';
  }

  function findingHTML(f) {
    var meta = App.levels.meta(f.level);
    var group = GROUP_LABEL[f.group];
    var since = toMs(f.since);
    return '<div class="finding" data-level="' + lvlAttr(f.level) + '" data-key="' + esc(f.key) + '">' +
      '<span class="finding__icon">' + App.icon(meta.icon) + '</span>' +
      '<div class="finding__text">' +
        '<p class="finding__title">' + esc(f.message || f.label || 'Finding') + '</p>' +
        (f.advice ? '<p class="finding__detail">' + esc(f.advice) + '</p>' : '') +
        '<p class="dx-finding__meta"><b>' + esc(meta.short) + '</b>' +
          (group ? ' · ' + esc(group) : '') +
          (isNum(since) ? ' · since ' + esc(App.fmt.time(since)) : '') + '</p>' +
      '</div>' +
      (f.valueText ? '<span class="finding__value" data-dx-val>' + esc(f.valueText) + '</span>' : '') +
      '</div>';
  }
  function noDataHTML() {
    return '<div class="dx-allclear" data-level="off">' +
      '<span class="finding__icon">' + App.icon('unlink') + '</span>' +
      '<div class="finding__text">' +
        '<p class="finding__title">No suit data</p>' +
        '<p class="finding__detail">Connect sensors in Log to assess risk.</p>' +
        '<button type="button" class="btn btn--ghost" id="dx-go-log" data-dx-action="log">Go to Log' + App.icon('chevronRight') + '</button>' +
      '</div></div>';
  }
  function allClearHTML() {
    return '<div class="dx-allclear" data-level="0">' +
      '<span class="finding__icon">' + App.icon('check') + '</span>' +
      '<div class="finding__text">' +
        '<p class="finding__title">All in range</p>' +
        '<p class="finding__detail">All readings are within nominal limits.</p>' +
      '</div></div>';
  }
  function findingsHTML(a) {
    var list = activeFindings(a);
    if (list.length) return '<div class="dx-findings">' + list.map(findingHTML).join('') + '</div>';
    if (!a || !isNum(a.level)) return noDataHTML();
    return allClearHTML();
  }
  function findingsSig(a) {
    return (a && isNum(a.level) ? 'd' : 'n') + '#' + activeFindings(a).map(function (f) {
      return [f.key, f.level, f.message, f.advice, !!f.valueText, f.since].join(':');
    }).join('|');
  }

  function heroHTML(member, a) {
    var lvl = a && isNum(a.level) ? a.level : null;
    var score = a && isNum(a.score) ? a.score : 0;
    return '<section class="card dx-hero" id="dx-hero" data-level="' + lvlAttr(lvl) + '" aria-labelledby="dx-hero-title">' +
      '<div class="dx-hero__top">' +
        '<p class="eyebrow dx-hero__who">' + esc(member.role + ' · ' + member.name) + '</p>' +
        '<p class="dx-updated" id="dx-updated">' + esc(updatedText(a)) + '</p>' +
      '</div>' +
      '<div class="dx-hero__main">' +
        '<div id="dx-ring">' + App.ui.ring(score, lvl, 'score') + '</div>' +
        '<div class="dx-hero__text">' +
          '<span id="dx-badge">' + App.ui.badge(lvl) + '</span>' +
          '<h2 class="dx-hero__label" id="dx-hero-title">' + esc(heroTitle(a)) + '</h2>' +
          '<p class="dx-hero__summary" id="dx-summary">' + esc(summaryText(a)) + '</p>' +
        '</div>' +
      '</div>' +
      '<button type="button" class="btn btn--secondary btn--block" id="dx-listen" data-dx-action="listen">' +
        App.icon('volume') + 'Hear diagnosis</button>' +
      '<hr class="divider">' +
      '<div class="dx-sub"><p class="eyebrow">Active findings</p><p class="dx-count" id="dx-count">' + esc(countText(a)) + '</p></div>' +
      '<div id="dx-findings">' + findingsHTML(a) + '</div>' +
      '</section>';
  }

  function updateRing(a) {
    var host = q('#dx-ring');
    if (!host) return;
    var lvl = a && isNum(a.level) ? a.level : null;
    var score = a && isNum(a.score) ? Math.max(0, Math.min(100, a.score)) : 0;
    var ring = host.querySelector('.ring');
    var arc = ring && ring.querySelector('.ring__value');
    if (!arc) { host.innerHTML = App.ui.ring(score, lvl, 'score'); return; }
    ring.setAttribute('data-level', lvlAttr(lvl));
    var c = parseFloat(arc.getAttribute('stroke-dasharray')) || 2 * Math.PI * 56;
    arc.style.strokeDashoffset = (c * (1 - score / 100)).toFixed(1);   // Inline style animates the transition
    setText(ring.querySelector('.ring__num'), String(Math.round(score)));
  }

  function updateHero(a) {
    var hero = q('#dx-hero');
    if (!hero) return;
    var lvl = a && isNum(a.level) ? a.level : null;
    var la = lvlAttr(lvl);
    if (hero.getAttribute('data-level') !== la) hero.setAttribute('data-level', la);
    updateRing(a);
    setHTML(q('#dx-badge'), App.ui.badge(lvl));
    setText(q('#dx-hero-title'), heroTitle(a));
    setText(q('#dx-summary'), summaryText(a));
    setText(q('#dx-updated'), updatedText(a));
    setText(q('#dx-count'), countText(a));

    var sig = findingsSig(a);
    if (sig !== st.sig.findings) {
      st.sig.findings = sig;
      setHTML(q('#dx-findings'), findingsHTML(a));
      return;
    }
    // Same findings: update live values only
    var byKey = {};
    activeFindings(a).forEach(function (f) { byKey[f.key] = f; });
    Array.prototype.forEach.call(st.root.querySelectorAll('#dx-findings .finding'), function (row) {
      var f = byKey[row.getAttribute('data-key')];
      if (f) setText(row.querySelector('[data-dx-val]'), f.valueText || '');
    });
  }

  /* 2 · Mental health */
  /* Fallback check-in rules when the risk engine is unavailable */
  function localMental(c) {
    var notes = [];
    if (c.mood === 0) notes.push('low mood');
    if (c.energy === 0) notes.push('low energy');
    if (isNum(c.sleepMin) && c.sleepMin < C.mental.sleep.recommendedMin) notes.push('less sleep than recommended');
    return {
      level: notes.length >= 3 ? 2 : notes.length ? 1 : 0,
      text: notes.length ? capitalize(notes.join(', ')) + '.' : MENTAL_OK,
    };
  }

  function mentalInfo(member, a) {
    var all = App.store.get('checkins') || {};
    var grp = a && a.groups && a.groups.mental;
    var c = all[member.id] || (grp && grp.checkin) || null;
    if (!c) return { checkin: null, level: null, findings: [], text: '', advice: '' };
    var local = localMental(c);
    var findings = ((grp && grp.findings) || []).filter(function (f) { return f && isNum(f.level) && f.level >= 1; });
    var level = grp && isNum(grp.level) ? grp.level : local.level;
    var text = findings.length ? withPeriod(findings[0].message) : (level === 0 ? MENTAL_OK : local.text);
    return {
      checkin: c,
      level: level,
      findings: findings,
      text: text,
      advice: findings.length && findings[0].advice ? findings[0].advice : '',
    };
  }

  function mentalCell(label, icon, value, flagged) {
    return '<div' + (flagged ? ' data-level="1"' : '') + '><dt>' + esc(label) + '</dt>' +
      '<dd>' + App.icon(icon) + '<span>' + esc(value) + '</span></dd></div>';
  }

  function mentalBodyHTML(member, info) {
    if (!info.checkin) {
      var btn = '<button type="button" class="btn btn--primary" id="dx-go-log-mental" data-dx-action="log">' +
        App.icon('clipboard') + 'Add check-in</button>';
      return App.ui.empty('smile', 'No mental health check-in yet for ' + member.name + '.')
        .replace(/<\/div>$/, btn + '</div>');
    }
    var c = info.checkin;
    var M = C.mental;
    var meta = App.levels.meta(info.level);
    var rec = M.sleep.recommendedMin;
    var lowSleep = isNum(c.sleepMin) && c.sleepMin < rec;
    var at = toMs(c.at);
    return '<div class="dx-result">' +
      '<div class="dx-result__head" data-level="' + lvlAttr(info.level) + '">' +
        '<span class="dx-result__icon">' + App.icon(meta.icon) + '</span>' +
        '<div class="dx-result__copy">' +
          '<p class="dx-result__label">Result: ' + esc(meta.label) + '</p>' +
          '<p class="dx-result__text">' + esc(info.text) + '</p>' +
          (info.advice ? '<p class="dx-result__advice">' + esc(info.advice) + '</p>' : '') +
        '</div>' +
      '</div>' +
      '<dl class="dx-mental-grid">' +
        mentalCell(M.mood.label, M.mood.icons[c.mood] || 'smile', M.mood.options[c.mood] || '—', c.mood === 0) +
        mentalCell(M.energy.label, M.energy.icons[c.energy] || 'energyMid', M.energy.options[c.energy] || '—', c.energy === 0) +
        mentalCell(M.sleep.label, 'bed', isNum(c.sleepMin) ? App.fmt.minutes(c.sleepMin) : '—', lowSleep) +
      '</dl>' +
      '<p class="dx-foot">' +
        (isNum(at)
          ? 'Logged <span data-dx-ago="' + at + '">' + esc(agoText(at)) + '</span> · ' + esc(App.fmt.time(at))
          : 'Check-in time unavailable') +
        (lowSleep ? ' · Recommended: ' + esc(App.fmt.minutes(rec)) : '') +
      '</p>' +
      (info.findings.length > 1
        ? '<div class="dx-findings">' + info.findings.slice(1).map(findingHTML).join('') + '</div>'
        : '') +
      '</div>';
  }

  function mentalSectionHTML(member, a) {
    var info = mentalInfo(member, a);
    return '<section class="section" id="dx-mental" aria-label="Mental health">' +
      App.ui.sectionHead({
        eyebrow: 'Check-in',
        title: 'Mental health',
        action: '<span id="dx-mental-badge">' + (info.checkin ? App.ui.badge(info.level) : '') + '</span>',
      }) +
      '<div class="card" id="dx-mental-body">' + mentalBodyHTML(member, info) + '</div>' +
      '</section>';
  }

  function mentalSig(member, a) {
    var info = mentalInfo(member, a);
    var c = info.checkin;
    if (!c) return 'none';
    return [c.at, c.mood, c.energy, c.sleepMin, info.level, info.text, info.advice,
      info.findings.map(function (f) { return f.key + ':' + f.level; }).join(',')].join('|');
  }

  function updateMental(member, a) {
    var sig = mentalSig(member, a);
    if (sig === st.sig.mental) return;
    st.sig.mental = sig;
    var info = mentalInfo(member, a);
    setHTML(q('#dx-mental-body'), mentalBodyHTML(member, info));
    setHTML(q('#dx-mental-badge'), info.checkin ? App.ui.badge(info.level) : '');
  }

  /* 3 and 4 · Physical health, suit, and life support */
  function rangeText(key) {
    var m = C.metrics[key];
    var r = m.nominal || [null, null];
    var d = m.decimals || 0;
    if (r[0] !== null && r[1] !== null) return App.fmt.num(r[0], d) + '–' + App.fmt.num(r[1], d);
    if (r[0] !== null) return '≥ ' + App.fmt.num(r[0], d);
    if (r[1] !== null) return '≤ ' + App.fmt.num(r[1], d);
    return '—';
  }

  /* All data displayed in a table row */
  function rowModel(key, member, a, data) {
    var m = C.metrics[key];
    var value = metricValue(key, data, member.id);
    var base = member.baseline || {};
    var r = {
      key: key, m: m, value: value,
      level: rowLevel(key, value, a),
      actual: '', base: '—', diff: '—',
      missing: m.onDemand ? 'Not measured' : 'No signal',
    };
    if (key === 'bp') {
      var manualBP=manualValues(member.id);
      if (isNum(base.bpSys)) r.base = Math.round(base.bpSys) + '/' + Math.round(base.bpDia);
      if (value !== null) {
        r.actual = String(manualBP.bpSys) + '/' + (isNum(manualBP.bpDia) ? String(manualBP.bpDia) : '—');
        if (isNum(base.bpSys) && isNum(manualBP.bpDia)) r.diff = signed(manualBP.bpSys - base.bpSys, 0) + '/' + signed(manualBP.bpDia - base.bpDia, 0);
      }
    } else {
      if (isNum(base[key])) r.base = App.fmt.num(base[key], m.decimals);
      if (value !== null) {
        r.actual = App.fmt.num(value, m.decimals);
        if (isNum(base[key])) r.diff = signed(value - base[key], m.decimals);
      }
    }
    return r;
  }

  function valHTML(r) {
    return r.value === null
      ? '<span class="dx-missing">' + esc(r.missing) + '</span>'
      : esc(r.actual) + '<small>' + esc(r.m.unit) + '</small>';
  }

  function rowHTML(g, r) {
    var meta = r.value === null ? App.levels.OFFLINE : App.levels.meta(r.level);
    var html = '<tr id="dx-row-' + r.key + '" data-level="' + lvlAttr(r.value === null ? null : r.level) + '">' +
      '<th scope="row"><span class="dx-metric">' +
        '<span class="dx-metric__icon">' + App.icon(meta.icon, null, meta.label) + '</span>' +
        '<abbr title="' + esc(r.m.label) + '">' + esc(r.m.short) + '</abbr></span></th>' +
      '<td class="dx-val">' + valHTML(r) + '</td>';
    if (g === 'bio') html += '<td class="dx-base">' + esc(r.base) + '</td><td class="dx-diff">' + esc(r.diff) + '</td>';
    else html += '<td class="dx-range">' + esc(rangeText(r.key)) + '</td>';
    return html + '</tr>';
  }

  function tableHTML(g, member, a, data) {
    var bio = g === 'bio';
    var head = bio ? ['Metric', 'Current', 'Baseline', 'Diff.'] : ['Metric', 'Current', 'Nominal'];
    return '<table class="dx-table" id="dx-table-' + g + '">' +
      '<caption class="visually-hidden">' + esc(C.groups[g].label) +
        (bio ? ': current value, pre-mission baseline, and difference' : ': current value and nominal range') + '</caption>' +
      '<thead><tr>' + head.map(function (h) { return '<th scope="col">' + h + '</th>'; }).join('') + '</tr></thead>' +
      '<tbody>' + C.groups[g].metrics.map(function (key) {
        return rowHTML(g, rowModel(key, member, a, data));
      }).join('') + '</tbody></table>';
  }

  /* Group result: level and summary */
  function groupInfo(g, member, a, data) {
    var grp = a && a.groups && a.groups[g];
    var findings = ((grp && grp.findings) || activeFindings(a).filter(function (f) { return f.group === g; }))
      .filter(function (f) { return f && isNum(f.level) && f.level >= 1; });
    var levels = C.groups[g].metrics.map(function (k) { return rowLevel(k, metricValue(k, data, member.id), a); });
    var anyData = levels.some(isNum);
    var level = !anyData ? null : (grp && isNum(grp.level) ? grp.level : App.levels.max(levels));
    var text;
    if (findings.length) {
      var more = findings.length - 1;
      text = String(findings[0].message || findings[0].label).replace(/[.!?…]$/, '') +
        (more ? ' and ' + more + (more === 1 ? ' more finding.' : ' more findings.') : '.');
    } else if (!anyData) {
      text = 'No sensor signal.';
    } else {
      text = GROUP_OK[g];
    }
    return { level: level, text: text };
  }

  function groupSectionHTML(g, member, a, data) {
    var info = groupInfo(g, member, a, data);
    return '<section class="section" id="dx-' + g + '" aria-label="' + esc(C.groups[g].label) + '">' +
      App.ui.sectionHead({
        eyebrow: g === 'bio' ? 'Biomedical sensors' : 'Suit telemetry',
        title: C.groups[g].label,
        action: '<span id="dx-' + g + '-badge">' + App.ui.badge(info.level) + '</span>',
      }) +
      '<div class="card">' +
        '<div class="dx-group-result" id="dx-' + g + '-result">' + App.ui.statusLine(info.level, info.text) + '</div>' +
        '<div class="dx-table-wrap">' + tableHTML(g, member, a, data) + '</div>' +
      '</div></section>';
  }

  function updateGroups(member, a, data) {
    ['bio', 'suit'].forEach(function (g) {
      C.groups[g].metrics.forEach(function (key) {
        var tr = q('#dx-row-' + key);
        if (!tr) return;
        var r = rowModel(key, member, a, data);
        var lvl = lvlAttr(r.value === null ? null : r.level);
        if (tr.getAttribute('data-level') !== lvl) {
          tr.outerHTML = rowHTML(g, r);       // Level changed: redraw the row and icon
          return;
        }
        setHTML(tr.querySelector('.dx-val'), valHTML(r));
        if (g === 'bio') {
          setText(tr.querySelector('.dx-base'), r.base);
          setText(tr.querySelector('.dx-diff'), r.diff);
        }
      });
      var info = groupInfo(g, member, a, data);
      setHTML(q('#dx-' + g + '-result'), App.ui.statusLine(info.level, info.text));
      setHTML(q('#dx-' + g + '-badge'), App.ui.badge(info.level));
    });
  }

  /* 4b · Complete daily physical-health panel (manual + sensor sources) */
  var PHYSICAL = [
    {key:'hr',label:'Resting heart rate',unit:'bpm',method:'Wearable HR band · 5 min seated',source:'sensor',why:'Early signal of cardiovascular strain, infection or dehydration.'},
    {key:'hrv',label:'Heart-rate variability (RMSSD)',unit:'ms',method:'Wearable band · overnight',source:'sensor',why:'Tracks recovery and physiological stress against personal baseline.'},
    {key:'bp',label:'Blood pressure',unit:'mmHg',method:'Bluetooth cuff or manual cuff · seated after 5 min',source:'manual',why:'Screens cardiovascular load and fluid-shift effects.'},
    {key:'orthostatic',label:'Orthostatic blood-pressure drop',unit:'mmHg',method:'Cuff: lying, then standing at 1 and 3 min',source:'manual',why:'Checks dizziness and intolerance when returning to gravity.'},
    {key:'spo2',label:'Blood oxygen (SpO₂)',unit:'%',method:'Bluetooth fingertip oximeter',source:'sensor',why:'Monitors oxygenation in a closed habitat or suit.'},
    {key:'temp',label:'Body temperature',unit:'°C',method:'Bluetooth forehead or ear thermometer',source:'sensor',why:'Flags possible fever or cooling-system problems.'},
    {key:'rr',label:'Respiratory rate',unit:'breaths/min',method:'Count breaths for 60 seconds or respiratory sensor',source:'manual',why:'Screens respiratory changes and possible CO₂-related problems.'},
    {key:'glucose',label:'Fasting blood glucose',unit:'mg/dL',method:'Bluetooth glucometer · finger-prick',source:'manual',why:'Tracks metabolic changes; record fasting status.'},
    {key:'sleep',label:'Sleep duration',unit:'hours',method:'Actigraphy wearable or manual entry',source:'manual',why:'Tracks circadian disruption and accumulated fatigue.'},
    {key:'weight',label:'Body mass',unit:'kg',method:'Adapted spacecraft scale · manual entry',source:'manual',why:'Tracks nutritional status and muscle-mass changes.'},
    {key:'hydration',label:'Hydration (urine colour scale)',unit:'1–8',method:'Compare with the hydration colour card',source:'manual',why:'Helps identify dehydration and renal-stone risk.'},
    {key:'radAccum',label:'Accumulated radiation dose',unit:'mSv',method:'Personal dosimeter · cumulative mission dose',source:'manual',why:'Tracks mission and career radiation exposure.'}
  ];
  var SENSOR_KEYS = {hr:'hr',hrv:'hrv',spo2:'spo2',temp:'temp',rad:'rad'};
  var MONTHLY_LABS = [
    {key:'hemoglobin',label:'Hemoglobin',unit:'g/dL',method:'Monthly blood test',source:'manual',why:'Periodic screen for anemia; interpret using the flight surgeon’s reference range.'},
    {key:'wbc',label:'White blood cells',unit:'cells/µL',method:'Monthly complete blood count',source:'manual',why:'Trend can help identify infection or immune changes.'},
    {key:'platelets',label:'Platelets',unit:'cells/µL',method:'Monthly complete blood count',source:'manual',why:'Tracks clotting-related blood count changes.'}
  ];
  var PHYSICAL_GUIDANCE = {
    hr:'Green: 50–90 bpm or within ±10 bpm of baseline. Observe: 91–110 or +10–20 over baseline. Red: >110 at rest, <40 with dizziness, or +20 over baseline for 3 days.',
    hrv:'Use personal baseline only. Observe: 15–30% below baseline. Review: >30% below baseline or falling for 3 days.',
    bp:'Green: below 120/80 mmHg. Observe: 120–139 or 80–89. Red: ≥140/90; urgent review at ≥180/120.',
    orthostatic:'Review: systolic drop ≥20 mmHg, diastolic drop ≥10 mmHg, fainting or marked dizziness.',
    spo2:'Green: ≥95%. Observe: 92–94%. Red: <92% or <95% with shortness of breath.',
    temp:'Green: 36.1–37.2 °C. Observe: 37.3–38.0 °C. Red: >38.0 °C or <35.5 °C.',
    rr:'Green: 12–20 breaths/min. Observe: 21–24 or 10–11. Red: >24 or <10.',
    glucose:'Green fasting: 70–99 mg/dL. Observe: 100–125 or 60–69. Red: ≥126 or <54; follow flight-surgeon protocol.',
    sleep:'Green: 7–9 h. Observe: 6–7 h. Review: <6 h or 3 nights in a row below 6 h.',
    weight:'Observe: 2–5% change in 2 weeks. Red: >5% loss in 30 days.',
    hydration:'Green: 1–3. Observe: 4–5. Red: ≥6 or no urination for 8 h.',
    rad:'Green: <50% of mission budget. Observe: 50–80%. Red: >80% or solar-particle-event warning.'
  };
  function manualValues(id) {
    var all = App.store.get('physicalManual') || {};
    return all[id] || {};
  }
  function physicalValue(key, data, saved) {
    if (SENSOR_KEYS[key] && data && isNum(data[SENSOR_KEYS[key]]) && isConnected(App.crew.selected().id, SENSOR_KEYS[key])) return data[SENSOR_KEYS[key]];
    if (key === 'bp' && saved && saved.bpSys != null && saved.bpDia != null) return saved.bpSys + '/' + saved.bpDia;
    return saved && saved[key] != null && saved[key] !== '' ? saved[key] : null;
  }
  function physicalCard(p, member, data, saved) {
    var v = physicalValue(p.key, data, saved);
    var sensorKey = SENSOR_KEYS[p.key];
    var status = sensorKey && App.telemetry && App.telemetry.status ? App.telemetry.status(member.id, sensorKey) : '';
    var simulated = sensorKey && !(App.telemetry && App.telemetry.isHardwareConnected && App.telemetry.isHardwareConnected(member.id,sensorKey));
    var sourceLabel = sensorKey ? (status === 'connected' ? (simulated ? 'Not paired · demo data only' : 'Sensor connected') : 'Sensor not connected') : 'Manual entry / external device';
    if(p.key==='bp' && saved.bpSys!=null && saved.bpDia!=null) v=Number(saved.bpSys)+'/'+Number(saved.bpDia);
    var value = v == null ? '<span class="dx-missing">No measurement yet</span>' : '<b>' + esc(String(v)) + '</b> <small>' + esc(p.unit) + '</small>';
    var input = p.key === 'bp' ? '<div class="dx-bp-inputs"><label class="field"><span class="field__label">Systolic</span><input class="input" type="number" inputmode="decimal" data-physical-input="bpSys" value="' + esc(saved.bpSys == null ? '' : saved.bpSys) + '" placeholder="mmHg"></label><label class="field"><span class="field__label">Diastolic</span><input class="input" type="number" inputmode="decimal" data-physical-input="bpDia" value="' + esc(saved.bpDia == null ? '' : saved.bpDia) + '" placeholder="mmHg"></label></div>' : (p.source === 'manual' ? '<label class="field"><span class="field__label">Enter value</span><input class="input" type="number" inputmode="decimal" step="any" data-physical-input="' + p.key + '" value="' + esc(saved[p.key] == null ? '' : saved[p.key]) + '" placeholder="Not recorded"></label>' : '');
    return '<article class="card" data-physical-card="' + p.key + '"><div class="row-between"><b>' + esc(p.label) + '</b><span class="chip" data-tone="' + (sensorKey && status !== 'connected' ? 'warning' : 'accent') + '">' + esc(sourceLabel) + '</span></div><p class="dx-physical-value">' + value + '</p><p class="subtle">' + esc(p.method) + '</p>' + input + '<details><summary>Why it matters · ranges · action</summary><p>' + esc(p.why) + '</p><p>' + esc(PHYSICAL_GUIDANCE[p.key]) + '</p><p class="subtle">Prototype guidance only. Contact the flight surgeon for interpretation.</p></details></article>';
  }
  function physicalSectionHTML(member, data) {
    var saved = manualValues(member.id);
    return '<section class="section" id="dx-physical-complete" aria-label="Complete physical health"><div class="section__head"><div><p class="eyebrow">Daily measurements · 12 indicators</p><h2 class="section__title">Physical health</h2></div></div><div class="card dx-physical-notice"><b>Sensor connection status</b><p>All 12 parameters remain visible. If a sensor is not paired, its card says so; no missing value is treated as normal. Browser demo streams are simulated, not readings from suit hardware.</p></div><div class="dx-physical-grid">' + PHYSICAL.map(function(p){return physicalCard(p,member,data,saved);}).join('') + '</div><p class="subtle">Manual entries are stored on this phone only. They are not sent to EVA Station.</p></section>';
  }
  function updatePhysicalValues(member, data) {
    if (!st || !q('#dx-physical-complete')) return;
    PHYSICAL.concat(MONTHLY_LABS).forEach(function(p) {
      var card=q('[data-physical-card="'+p.key+'"]');
      if (!card) return;
      var v=physicalValue(p.key,data,manualValues(member.id));
      var host=card.querySelector('.dx-physical-value');
      if (host) setHTML(host,v==null?'<span class="dx-missing">No measurement yet</span>':'<b>'+esc(String(v))+'</b> <small>'+esc(p.unit)+'</small>');
      var chip=card.querySelector('.chip');
      if (chip) {
        var key=SENSOR_KEYS[p.key], status=key&&App.telemetry&&App.telemetry.status?App.telemetry.status(member.id,key):'';
        var demo=key&&!(App.telemetry&&App.telemetry.isHardwareConnected&&App.telemetry.isHardwareConnected(member.id,key));
        chip.textContent=key?(status==='connected'?(demo?'Not paired · demo data only':'Sensor connected'):'Sensor not connected'):'Manual entry / external device';
        chip.setAttribute('data-tone',key&&status!=='connected'?'warning':'accent');
      }
    });
  }

  function savePhysicalInput(e) {
    var input = e.target.closest('[data-physical-input]');
    if (!input || !st || !st.root.contains(input)) return;
    var key = input.getAttribute('data-physical-input');
    var all = App.store.get('physicalManual') || {};
    var member = st.memberId;
    var row = Object.assign({}, all[member] || {});
    row[key] = input.value === '' ? null : Number(input.value);
    if ((key==='bpSys'||key==='bpDia') && row.bpSys!=null && row.bpDia!=null) row.bpAt=Date.now();
    all[member] = row;
    App.store.set({physicalManual:all});
    updatePhysicalValues(App.crew.byId(member),getData(member));
  }

  /* 5 · Blackbox */
  function blackboxItems(member) {
    if (!App.blackbox || typeof App.blackbox.list !== 'function') return null;
    try { return App.blackbox.list({ crewId: member.id, minLevel: 1, limit: 6 }) || []; } catch (e) { return []; }
  }

  function entryText(e) {
    var m = e.key && C.metrics[e.key];
    var txt = e.msg || (m ? m.label : 'Logged event');
    if (m && isNum(e.value) && !/\d/.test(txt)) txt += ' · ' + App.fmt.num(e.value, m.decimals) + ' ' + m.unit;
    return txt;
  }

  function timelineHTML(member) {
    var items = blackboxItems(member);
    if (items === null) return App.ui.empty('blackbox', 'Blackbox unavailable.');
    if (!items.length) return App.ui.empty('blackbox', 'No incidents logged for ' + member.name + '.');
    return '<div class="timeline">' + items.map(function (e) {
      var lvl = isNum(e.level) ? e.level : null;
      var meta = App.levels.meta(lvl);
      return '<div class="timeline__item" data-level="' + lvlAttr(lvl) + '">' +
        '<span class="timeline__time">' + esc(timeText(e.t)) + '</span>' +
        '<span class="timeline__dot" aria-hidden="true"></span>' +
        '<p class="timeline__text"><span class="dx-tl-level">' + esc(meta.short) + '</span>' + esc(entryText(e)) + '</p>' +
        '</div>';
    }).join('') + '</div>';
  }

  function timelineSig(member) {
    var items = blackboxItems(member);
    if (items === null) return 'na';
    return items.map(function (e) { return [e.t, e.level, e.key, e.msg].join(':'); }).join('|');
  }

  function logSectionHTML(member) {
    return '<section class="section" id="dx-log" aria-label="Blackbox">' +
      App.ui.sectionHead({
        eyebrow: 'Blackbox',
        title: 'Recent incidents',
        action: '<button type="button" class="btn btn--ghost" id="dx-reports" data-dx-action="reports">' +
          App.icon('fileText') + 'View reports</button>',
      }) +
      '<div class="card" id="dx-timeline">' + timelineHTML(member) + '</div>' +
      '</section>';
  }

  function updateTimeline() {
    if (!st) return;
    st.tlTimer = null;
    var member = App.crew.byId(st.memberId);
    if (!member || st.iv) return;
    var sig = timelineSig(member);
    if (sig === st.sig.timeline) return;
    st.sig.timeline = sig;
    setHTML(q('#dx-timeline'), timelineHTML(member));
  }
  function scheduleTimeline() {
    if (st && !st.tlTimer) st.tlTimer = setTimeout(updateTimeline, 400);
  }

  /* IV crew inside the spacecraft */
  function ivHTML(member) {
    return '<section class="card dx-note" aria-label="No suit telemetry">' + App.icon('info') +
      '<div class="dx-note__copy">' +
        '<p class="dx-note__title">' + esc(member.name) + ' is inside the spacecraft</p>' +
        '<p class="dx-note__text">Assigned role: ' + esc(member.role) + '. No suit is in use, so there is no telemetry to assess. ' +
          'Only mental health is shown. Select an EV crew member above to view suit telemetry.</p>' +
      '</div></section>';
  }

  /* Actions */
  function speechText(member, a) {
    var who = member.role + ': ';
    if (!a || !isNum(a.level)) return who + 'no suit data.';
    var meta = App.levels.meta(a.level);
    var f = activeFindings(a)[0];
    var value = f ? String(f.valueText || '')
      .replace(/bpm/g, 'beats per minute').replace(/%/g, 'percent')
      .replace(/°C/g, 'degrees').replace(/mmHg/g, 'millimeters of mercury')
      .replace(/\bmin\b/g, 'minutes').replace(/\bh\b/g, 'hours').replace(/\//g, ' over ') : '';
    return capitalize(meta.voice || meta.label) + '. ' + who +
      (f ? f.message + (value ? ', ' + value : '') : 'all readings in range') + '.';
  }

  /* Called from a tap because speech requires a user gesture */
  function listen() {
    var member = st && App.crew.byId(st.memberId);
    if (!member) return;
    if (!App.voice) {
      App.ui.toast('Voice is unavailable in this browser', { icon: 'info' });
      return;
    }
    if (!App.store.get('voiceOn') && !App.shell.setVoice(true)) return;
    if (typeof App.voice.speakAssessment === 'function') App.voice.speakAssessment(member.id);
    else if (typeof App.voice.say === 'function') App.voice.say(speechText(member, getRisk(member.id)), { priority: 'high' });
  }

  function openReports() {
    var aj = App.views.settings;
    if (aj && typeof aj.open === 'function') aj.open('reports');
    else if (App.shell && App.shell.openDrawer) App.shell.openDrawer();
  }

  function onClick(e) {
    if (!st) return;
    var pair = e.target.closest('[data-dx-pair-ble]');
    if(pair){ if(!App.ble){App.ui.toast('Bluetooth adapter unavailable',{icon:'unlink',level:2});return;} App.ble.pair(st.memberId).catch(function(err){App.ui.toast(err.message,{icon:'unlink',level:2});}); return; }
    var b = e.target.closest('[data-dx-action]');
    if (!b || !st.root.contains(b)) return;
    var act = b.getAttribute('data-dx-action');
    if (act === 'listen') listen();
    else if (act === 'log') App.shell.go('log');
    else if (act === 'reports') openReports();
  }

  /* Refresh update and check-in timestamps every second */
  function tick() {
    if (!st) return;
    if (!st.iv) setText(q('#dx-updated'), updatedText(getRisk(st.memberId)));
    Array.prototype.forEach.call(st.root.querySelectorAll('[data-dx-ago]'), function (el) {
      setText(el, agoText(Number(el.getAttribute('data-dx-ago'))));
    });
  }

  /* View interface used by js/app.js */
  function render(root) {
    destroy();
    var member = App.crew.selected();
    st = { root: root, memberId: member ? member.id : null, iv: false, sig: {}, timer: null, tlTimer: null };
    root.addEventListener('click', onClick);
    root.addEventListener('change', savePhysicalInput);
    if (!member) {
      root.innerHTML = App.ui.empty('users', 'No crew configured.');
      return;
    }
    var a = getRisk(member.id);
    st.iv = !/^EV/.test(member.role);
    if (st.iv) {
      root.innerHTML = ivHTML(member) + mentalSectionHTML(member, a);
    } else {
      var data = getData(member.id);
      root.innerHTML =
        heroHTML(member, a) +
        mentalSectionHTML(member, a) +
        groupSectionHTML('bio', member, a, data) +
        physicalSectionHTML(member, data) +
        groupSectionHTML('suit', member, a, data) +
        logSectionHTML(member);
      st.sig.findings = findingsSig(a);
      st.sig.timeline = timelineSig(member);
    }
    st.sig.mental = mentalSig(member, a);
    st.timer = setInterval(tick, 1000);
  }

  function onEvent(evt, p) {
    if (!st) return;
    var member = App.crew.byId(st.memberId);
    if (!member) return;
    var mine = !!p && p.crewId === member.id;

    if (evt === 'risk' && mine) {
      var a = p.assessment || getRisk(member.id);
      if (!st.iv) {
        updateHero(a);
        updateGroups(member, a, getData(member.id));
        updatePhysicalValues(member, getData(member.id));
      }
      updateMental(member, a);
    } else if ((evt === 'telemetry' || evt === 'sensor') && mine && !st.iv) {
      updateGroups(member, getRisk(member.id), getData(member.id));
      updatePhysicalValues(member, getData(member.id));
    } else if (evt === 'checkin' && mine) {
      updateMental(member, getRisk(member.id));
    } else if (evt === 'blackbox' && !st.iv) {
      scheduleTimeline();
    }
  }

  function destroy() {
    if (!st) return;
    st.root.removeEventListener('click', onClick);
    st.root.removeEventListener('change', savePhysicalInput);
    clearInterval(st.timer);
    clearTimeout(st.tlTimer);
    st = null;
  }

  App.views.diagnosis = { render: render, onEvent: onEvent, destroy: destroy };
})();
