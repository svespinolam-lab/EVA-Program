/* EVA · Shared UI helpers return HTML or control common elements for consistent views. */
(function () {
  'use strict';
  var App = window.App = window.App || {};
  var esc = function (s) { return App.dom.esc(s); };

  function lvlAttr(level) { return typeof level === 'number' ? String(level) : 'off'; }

  App.ui = {
    lvlAttr: lvlAttr,

    /* Risk badge */
    badge: function (level, text) {
      var m = App.levels.meta(level);
      return '<span class="badge" data-level="' + lvlAttr(level) + '">' +
        App.icon(m.icon) + esc(text || m.label) + '</span>';
    },

    /* Status line with icon */
    statusLine: function (level, text) {
      var m = App.levels.meta(level);
      return '<p class="status-line" data-level="' + lvlAttr(level) + '">' +
        App.icon(m.icon) + '<span>' + esc(text || m.label) + '</span></p>';
    },

    /* Avatar with risk-colored ring */
    avatar: function (member, level, large) {
      return '<span class="avatar' + (large ? ' avatar--lg' : '') + '" data-level="' + lvlAttr(level) + '" aria-hidden="true">' +
        esc(member.initials || '?') + '</span>';
    },

    /* Section header: eyebrow, title, hint, and optional HTML action */
    sectionHead: function (o) {
      return '<div class="section__head"><div class="section__titles">' +
        (o.eyebrow ? '<p class="eyebrow">' + esc(o.eyebrow) + '</p>' : '') +
        '<h2 class="section__title">' + esc(o.title) + '</h2>' +
        (o.hint ? '<p class="section__hint">' + esc(o.hint) + '</p>' : '') +
        '</div>' + (o.action || '') + '</div>';
    },

    /* Mood/energy options as radio buttons. Views listen for clicks on data-seg and read data-value. */
    segmented: function (o) {
      var html = '<div class="segmented" role="radiogroup" aria-label="' + esc(o.label) + '" style="--n:' + o.options.length + '">';
      o.options.forEach(function (text, i) {
        var on = o.value === i;
        html += '<button type="button" class="segmented__opt" role="radio" id="' + esc(o.name + '-' + i) + '"' +
          ' data-seg="' + esc(o.name) + '" data-value="' + i + '" aria-checked="' + on + '">' +
          (o.icons ? App.icon(o.icons[i]) : '') + '<span>' + esc(text) + '</span></button>';
      });
      return html + '</div>';
    },

    /* SVG sparkline; numeric values and options: min, max, minSpan, capacity */
    sparkline: function (values, opts) {
      opts = opts || {};
      var w = 128, h = 44, pad = 4;
      var vals = (values || []).filter(function (v) { return typeof v === 'number' && !isNaN(v); });
      var open = '<svg class="spark" viewBox="0 0 ' + w + ' ' + h + '" preserveAspectRatio="none" aria-hidden="true">' +
        '<line class="spark__grid" x1="0" y1="' + h / 2 + '" x2="' + w + '" y2="' + h / 2 + '"/>';
      if (vals.length < 2) return open + '</svg>';

      var min = opts.min !== undefined ? opts.min : Math.min.apply(null, vals);
      var max = opts.max !== undefined ? opts.max : Math.max.apply(null, vals);
      var mean = vals.reduce(function (a, b) { return a + b; }, 0) / vals.length;
      var span = Math.max(max - min, opts.minSpan || Math.abs(mean) * 0.04 || 1);
      var mid = (max + min) / 2;
      min = mid - span / 2; max = mid + span / 2;

      var cap = Math.max(opts.capacity || vals.length, vals.length);
      var step = (w - pad * 2) / (cap - 1);
      var pts = vals.map(function (v, i) {
        var x = w - pad - (vals.length - 1 - i) * step;
        var y = h - pad - ((v - min) / (max - min)) * (h - pad * 2);
        return [x.toFixed(1), y.toFixed(1)];
      });
      var line = 'M' + pts.map(function (p) { return p.join(' '); }).join(' L');
      var first = pts[0], last = pts[pts.length - 1];
      var area = line + ' L' + last[0] + ' ' + (h - pad) + ' L' + first[0] + ' ' + (h - pad) + ' Z';
      return open +
        '<path class="spark__area" d="' + area + '"/>' +
        '<path class="spark__line" d="' + line + '" vector-effect="non-scaling-stroke"/>' +
        '<circle class="spark__end" cx="' + last[0] + '" cy="' + last[1] + '" r="3"/>' +
        '</svg>';
    },

    /* Circular score gauge from 0 (no risk) to 100 (maximum) */
    ring: function (score, level, caption) {
      var r = 56, c = 2 * Math.PI * r;
      var pct = Math.max(0, Math.min(100, score || 0)) / 100;
      return '<div class="ring" data-level="' + lvlAttr(level) + '">' +
        '<svg viewBox="0 0 132 132" aria-hidden="true">' +
        '<circle class="ring__track" cx="66" cy="66" r="' + r + '" stroke-width="10"/>' +
        '<circle class="ring__value" cx="66" cy="66" r="' + r + '" stroke-width="10" stroke-dasharray="' + c.toFixed(1) + '" stroke-dashoffset="' + (c * (1 - pct)).toFixed(1) + '"/>' +
        '</svg><div class="ring__label"><span class="ring__num">' + Math.round(score || 0) + '</span>' +
        '<span class="eyebrow">' + esc(caption || 'score') + '</span></div></div>';
    },

    /* Empty state */
    empty: function (icon, text) {
      return '<div class="empty">' + App.icon(icon || 'info') + '<p>' + esc(text) + '</p></div>';
    },

    /* Toast */
    toast: (function () {
      var timer = null;
      return function (text, opts) {
        opts = opts || {};
        var el = document.getElementById('toast');
        if (!el) return;
        el.setAttribute('data-level', lvlAttr(opts.level));
        if (opts.level === undefined) el.removeAttribute('data-level');
        el.innerHTML = App.icon(opts.icon || 'check') + '<span>' + esc(text) + '</span>';
        el.classList.add('is-open');
        clearTimeout(timer);
        timer = setTimeout(function () { el.classList.remove('is-open'); }, opts.ms || 2600);
      };
    })(),

    /* Bottom sheet: App.ui.sheet.open({title, html, onMount(bodyEl), onClose()}) */
    sheet: (function () {
      var current = null, lastFocus = null;
      function els() {
        return {
          sheet: document.getElementById('sheet'),
          scrim: document.getElementById('sheet-scrim'),
          title: document.getElementById('sheet-title'),
          body: document.getElementById('sheet-body'),
        };
      }
      function onKey(e) { if (e.key === 'Escape') api.close(); }
      var api = {
        open: function (o) {
          if (current) api.close(true);
          var e = els();
          if (!e.sheet) return;
          lastFocus = document.activeElement;
          current = o || {};
          // Replace the body on each opening to avoid accumulating listeners
          var fresh = e.body.cloneNode(false);
          e.body.parentNode.replaceChild(fresh, e.body);
          e.body = fresh;
          e.title.textContent = current.title || '';
          e.body.innerHTML = current.html || '';
          e.body.scrollTop = 0;
          e.sheet.inert = false;
          e.sheet.classList.add('is-open');
          e.scrim.classList.add('is-open');
          document.addEventListener('keydown', onKey);
          if (current.onMount) current.onMount(e.body);
          setTimeout(function () {
            var f = e.body.querySelector('[autofocus], button, input, select, textarea') || e.sheet.querySelector('.icon-btn');
            if (f) f.focus({ preventScroll: true });
          }, 60);
        },
        close: function (silent) {
          var e = els();
          if (!e.sheet || !current) return;
          var done = current;
          current = null;
          e.sheet.classList.remove('is-open');
          e.scrim.classList.remove('is-open');
          e.sheet.inert = true;
          document.removeEventListener('keydown', onKey);
          if (!silent && done.onClose) done.onClose();
          if (lastFocus && lastFocus.focus) lastFocus.focus({ preventScroll: true });
        },
        isOpen: function () { return !!current; },
      };
      return api;
    })(),

    /* In-app confirmation for environments that block native dialogs */
    confirm: function (o) {
      return new Promise(function (resolve) {
        var settled = false;
        function done(v) { if (settled) return; settled = true; App.ui.sheet.close(true); resolve(v); }
        App.ui.sheet.open({
          title: o.title,
          html: '<p class="muted">' + esc(o.message) + '</p>' +
            '<div class="row"><button type="button" class="btn btn--secondary grow" data-act="no">Cancel</button>' +
            '<button type="button" class="btn ' + (o.danger ? 'btn--danger' : 'btn--primary') + ' grow" data-act="yes">' + esc(o.confirmLabel || 'Confirm') + '</button></div>',
          onMount: function (body) {
            body.addEventListener('click', function (ev) {
              var b = ev.target.closest('[data-act]');
              if (b) done(b.getAttribute('data-act') === 'yes');
            });
          },
          onClose: function () { done(false); },
        });
      });
    },

    /* Copy text; if clipboard access fails, select the supplied textarea for manual copying. Call from a click. */
    copyText: function (text, fallbackEl) {
      function selectFallback() {
        if (fallbackEl) { fallbackEl.focus(); fallbackEl.select(); }
        return false;
      }
      try {
        if (navigator.clipboard && navigator.clipboard.writeText) {
          return navigator.clipboard.writeText(text).then(function () { return true; }, selectFallback);
        }
      } catch (e) { /* Permission unavailable */ }
      return Promise.resolve(selectFallback());
    },
  };
})();
