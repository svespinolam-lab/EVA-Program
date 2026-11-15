/* EVA Station · System view (phase 3): server and database status, phone
   sync, verified backups, console users and the security audit. Uses the
   phase-4 API; sections the current role may not use are hidden (the
   server enforces the permissions anyway). */
(function () {
  'use strict';
  var S = window.Station;
  var esc = S.esc, icon = S.icon;
  var me = null;

  function kb(n) { return n > 1048576 ? (n / 1048576).toFixed(1) + ' MB' : Math.round(n / 1024) + ' KB'; }
  function when(ms) { return ms ? S.date(ms) + ' ' + S.time(ms) : '—'; }
  function dur(s) { var h = Math.floor(s / 3600), m = Math.floor(s % 3600 / 60); return (h ? h + ' h ' : '') + m + ' min'; }
  function card(title, id, extra) { return '<section class="card stack"><div class="row-between"><h2 class="section__title">' + title + '</h2>' + (extra || '') + '</div><div id="' + id + '"><p class="subtle">Loading…</p></div></section>'; }
  function table(head, rows) { return '<div class="tbl-wrap"><table class="tbl"><thead><tr>' + head.map(function (h) { return '<th>' + h + '</th>'; }).join('') + '</tr></thead><tbody>' + (rows.join('') || '<tr><td colspan="' + head.length + '" class="subtle">None.</td></tr>') + '</tbody></table></div>'; }
  function set(id, html) { var el = document.getElementById(id); if (el) el.innerHTML = html; }
  function can(p) { return !!(me && me.permissions && me.permissions.indexOf(p) >= 0); }

  function loadStatus() {
    Promise.all([S.api('GET', '/api/status'), S.api('GET', '/api/health/database').catch(function () { return {}; })]).then(function (r) {
      var s = r[0], db = r[1];
      set('sys-status', '<dl class="kv">' +
        '<div class="kv__row"><dt>Server</dt><dd>EVA Station ' + esc(s.version) + ' · up ' + dur(s.uptimeS) + '</dd></div>' +
        '<div class="kv__row"><dt>Database</dt><dd>' + (db.ok ? icon('check') + ' integrity ok' : icon('octagon') + ' check failed') + ' · schema v' + s.schema + ' · ' + kb(s.dbBytes) + '</dd></div>' +
        '<div class="kv__row"><dt>Connected</dt><dd>' + s.phones + ' phone(s) · ' + s.consoles + ' console(s)</dd></div>' +
        '<div class="kv__row"><dt>Phone pairing</dt><dd>' + esc(s.pairing) + '</dd></div>' +
        '<div class="kv__row"><dt>Encrypted link</dt><dd>' + (s.https ? 'HTTPS port ' + s.https.port + ' · <span class="hash">' + esc(String(s.https.fingerprint).slice(0, 23)) + '…</span>' : 'HTTPS off') + '</dd></div>' +
        '<div class="kv__row"><dt>Last backup</dt><dd>' + (s.lastBackup ? esc(s.lastBackup.name) + ' · ' + when(s.lastBackup.createdAt) : 'none yet') + '</dd></div>' +
        '<div class="kv__row"><dt>You</dt><dd>' + esc(me ? me.name + ' · ' + me.role : '—') + '</dd></div></dl>');
    });
    S.api('GET', '/api/sync').then(function (rows) {
      set('sys-sync', table(['Crew', 'Link', 'Paired phone', 'Last seen', 'Last stored seq', 'Messages pending'], rows.map(function (x) {
        var d = x.device || {};
        return '<tr><td>' + esc(S.who(x.crewId)) + '</td><td>' + (x.online ? '<span class="chip" data-tone="accent">online</span>' : '<span class="chip" data-level="off">offline</span>') + '</td>' +
          '<td>' + (d.id ? esc(d.name) + ' <span class="subtle mono">' + esc(d.id) + '</span>' : '<span class="subtle">not paired</span>') + '</td><td class="mono">' + when(d.lastSeen) + '</td>' +
          '<td class="mono">' + (d.lastSeq || '—') + '</td><td class="mono">' + (x.outboxPending || 0) + '</td></tr>';
      })));
    });
  }

  function loadAdmin() {
    if (!can('admin')) {
      ['sys-backups', 'sys-users', 'sys-audit'].forEach(function (id) { set(id, '<p class="subtle">Your role (' + esc(me ? me.role : '?') + ') cannot manage this section.</p>'); });
      return;
    }
    S.api('GET', '/api/backups').then(function (bs) {
      set('sys-backups', table(['File', 'Size', 'Created', 'Checked'], bs.slice(0, 12).map(function (b) {
        return '<tr><td class="mono">' + esc(b.name) + '</td><td>' + kb(b.bytes) + '</td><td class="mono">' + when(b.createdAt) + '</td><td>' + (b.verified ? icon('check') + ' verified' : 'listed') + '</td></tr>';
      })) + '<p class="subtle">Restore with the station closed: <span class="mono">eva-station.exe -restore data/backups/&lt;file&gt;</span></p>');
    });
    S.api('GET', '/api/users').then(function (us) {
      set('sys-users', table(['Name', 'Role', 'Created', 'Status', ''], us.map(function (u) {
        return '<tr><td>' + esc(u.name) + '</td><td>' + esc(u.role) + '</td><td class="mono">' + when(u.createdAt) + '</td><td>' + (u.disabled ? 'disabled' : 'active') + '</td>' +
          '<td>' + (u.disabled ? '' : '<button type="button" class="btn btn--ghost btn--sm" data-disable="' + esc(u.id) + '">Disable</button>') + '</td></tr>';
      })) + '<div class="row sys-new-user"><input class="input" id="sys-user-name" maxlength="80" placeholder="Name (e.g. Dr. Vega)">' +
        '<select class="input" id="sys-user-role"><option value="surgeon">Flight surgeon</option><option value="operator">Operator</option></select>' +
        '<button type="button" class="btn btn--primary btn--sm" id="sys-user-add">' + icon('users') + 'Create user</button></div>');
    });
    S.api('GET', '/api/audit?limit=60').then(function (es) {
      set('sys-audit', table(['Time', 'Who', 'Role', 'Action', 'Result'], es.map(function (e) {
        var bad = e.result >= 400 || e.result === 0;
        return '<tr><td class="mono">' + when(e.t) + '</td><td>' + esc(e.actor) + (e.ip ? ' <span class="subtle mono">' + esc(e.ip) + '</span>' : '') + '</td><td>' + esc(e.role) + '</td>' +
          '<td class="mono">' + esc(e.action) + '</td><td' + (bad ? ' class="sys-bad"' : '') + '>' + e.result + '</td></tr>';
      })));
    });
  }

  function reload() { S.api('GET', '/api/auth/me').then(function (m) { me = m; loadStatus(); loadAdmin(); }); }

  S.views.system = {
    render: function (root) {
      root.innerHTML = '<div class="vwrap"><div class="st-head"><div><p class="eyebrow">Records · administration</p><h1>System</h1>' +
        '<p>Server and database health, phone synchronisation, verified backups, console users and the security audit.</p></div>' +
        '<div class="st-actions"><button type="button" class="btn btn--secondary" id="sys-refresh">' + icon('refresh') + 'Refresh</button></div></div><div class="stack">' +
        card('Station status', 'sys-status') + card('Phone synchronisation', 'sys-sync') +
        card('Backups', 'sys-backups', '<button type="button" class="btn btn--primary btn--sm" id="sys-backup">' + icon('shield') + 'Create verified backup</button>') +
        card('Console users', 'sys-users') + card('Security audit (latest 60)', 'sys-audit') + '</div></div>';
      root.firstElementChild.addEventListener('click', function (e) {
        if (e.target.closest('#sys-refresh')) { reload(); return; }
        if (e.target.closest('#sys-backup')) { S.post('/api/backups', {}, 'Backup written and verified').then(reload); return; }
        var dis = e.target.closest('[data-disable]');
        if (dis) {
          S.confirm({ title: 'Disable this user?', message: 'Their access link stops working immediately. The record stays in the audit.', confirmLabel: 'Disable', danger: true })
            .then(function (ok) { if (ok) S.post('/api/users/' + dis.getAttribute('data-disable') + '/disable', {}, 'User disabled').then(reload); });
          return;
        }
        if (e.target.closest('#sys-user-add')) {
          var name = document.getElementById('sys-user-name').value.trim(), role = document.getElementById('sys-user-role').value;
          if (!name) { S.toast('Enter a name', { icon: 'info' }); return; }
          S.post('/api/users', { name: name, role: role }).then(function (j) {
            var link = location.origin + j.link;
            S.modal({ title: 'Access link · ' + j.user.name, html: '<p class="muted">Shown only once. Give it privately to ' + esc(j.user.name) + ' (' + esc(j.user.role) + '). Anyone with this link can use the console with that role.</p>' +
              '<p class="url mono">' + esc(link) + '</p><div class="st-actions" style="justify-content:flex-end"><button type="button" class="btn btn--primary" data-close-modal>Done</button></div>' });
            reload();
          });
        }
      });
      reload();
    },
    update: function (kind) {
      var el = document.getElementById('sys-status');
      if (!el || kind !== 'tick') return;
      if (el._t && Date.now() - el._t < 10000) return;
      el._t = Date.now();
      loadStatus();
    },
  };
})();