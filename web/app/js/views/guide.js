/* EVA · Quick reference for levels, score, screens, and labels. */
(function () {
  'use strict';
  var App = window.App;
  var esc = App.dom.esc;
  var LEVELS = [
    { name: 'Nominal', code: 'OK', icon: 'check', range: '0–24', meaning: 'All readings in range; continue.' },
    { name: 'Caution', code: 'CAUT.', icon: 'info', range: '25–49', meaning: 'A reading left its normal range; watch it and slow down.' },
    { name: 'High risk', code: 'HIGH', icon: 'triangle', range: '50–79', meaning: 'A reading is far out of range; act now and follow the advice shown.' },
    { name: 'Critical', code: 'CRIT.', icon: 'octagon', range: '80–100', meaning: 'Immediate danger; stop, follow the protocol, return to the airlock.' }
  ];

  function section(id, title, html) {
    return '<section class="section" aria-labelledby="guide-' + id + '"><h3 class="section__title" id="guide-' + id + '">' +
      esc(title) + '</h3>' + html + '</section>';
  }

  function definitions(items) {
    return '<dl class="guide-definitions">' + items.map(function (item) {
      return '<div><dt>' + esc(item[0]) + '</dt><dd>' + esc(item[1]) + '</dd></div>';
    }).join('') + '</dl>';
  }

  App.guide = {
    open: function () {
      var levels = '<ul class="guide-levels">' + LEVELS.map(function (level, i) {
        return '<li class="guide-level" data-level="' + i + '"><span class="finding__icon">' + App.icon(level.icon) + '</span>' +
          '<div class="guide-level__copy"><p class="guide-level__name">' + esc(level.name) + ' <span class="badge">' + esc(level.code) + '</span></p>' +
          '<p>' + esc(level.meaning) + '</p></div></li>';
      }).join('') + '</ul>';
      var scores = '<ul class="guide-scores">' + LEVELS.map(function (level, i) {
        return '<li data-level="' + i + '"><span class="badge">' + esc(level.range) + '</span><span>' + esc(level.name) +
          (i === 0 ? ' <span class="muted">(higher still means closer to a limit)</span>' : '') + '</span></li>';
      }).join('') + '</ul>';

      App.ui.sheet.open({
        title: 'How to read EVA',
        html: '<div class="guide">' +
          section('levels', 'Status levels', levels) +
          section('score', 'Score 0–100', scores) +
          section('topbar', 'Top bar', definitions([
            ['Speaker', 'Voice alerts on/off.'],
            ['EVA n · T+hh:mm:ss', 'EVA number and time since the EVA started. Green dot = live data.'],
            ['Crew chips', 'Tap to choose whose data you see. Ring colour = that person’s level.'],
            ['Station chip', 'Link to the EVA Station PC: off / connecting / online. Readings are queued while offline.'],
            ['Coloured banner', 'The most serious active alert. Tap it for details.']
          ])) +
          section('tabs', 'Tabs', definitions([
            ['Log', 'Daily check-in (mood, energy, sleep) plus live body and suit sensors.'],
            ['Diagnosis', 'Score, active findings and what to do.'],
            ['Profile', 'Identity, pre-mission baseline and history.']
          ])) +
          section('labels', 'Labels', definitions([
            ['DEMO · NOT PAIRED', 'Simulated value; no real sensor.'],
            ['Baseline', 'The person’s normal value measured before the mission.'],
            ['Diff.', 'Difference from baseline.'],
            ['Hear diagnosis', 'Reads the result aloud.']
          ])) + '</div>',
        onMount: function (body) { body.scrollTop = 0; }
      });
    }
  };
})();
