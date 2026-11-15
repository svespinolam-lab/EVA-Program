/* =====================================================================
   EVA · ICONS (shared by the phone app and EVA Station)
   Íconos de línea propios (cuadrícula de 24 px, trazo 1.75).
   Uso:  App.icon('heart')  →  '<svg class="icon">…</svg>'
   Para agregar uno: añade una entrada con el contenido del <svg>.
   ===================================================================== */
(function () {
  'use strict';
  var App = window.App = window.App || {};

  var I = {
    /* Navegación y acciones */
    menu:      '<path d="M4 7h16M4 12h16M4 17h16"/>',
    close:     '<path d="M6 6l12 12M18 6 6 18"/>',
    clipboard: '<path d="M9 4H7a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V6a2 2 0 0 0-2-2h-2"/><rect x="9" y="2.5" width="6" height="3" rx="1"/><path d="M8 14h2l1.5-3 2 6 1.5-3h1"/>',
    activity:  '<path d="M3 12h4l3-8 4 16 3-8h4"/>',
    user:      '<circle cx="12" cy="8" r="4"/><path d="M4 21a8 8 0 0 1 16 0"/>',
    users:     '<circle cx="9" cy="8" r="3.5"/><path d="M2.5 20a6.5 6.5 0 0 1 13 0"/><path d="M16 4.6a3.5 3.5 0 0 1 0 6.8M18 14a6.5 6.5 0 0 1 3.5 6"/>',
    chevronRight: '<path d="M9 5l7 7-7 7"/>',
    plus:      '<path d="M12 5v14M5 12h14"/>',
    minus:     '<path d="M5 12h14"/>',
    check:     '<path d="M5 12.5l4.5 4.5L19 7.5"/>',
    edit:      '<path d="M4 20h4L19 9l-4-4L4 16z"/><path d="M13.5 6.5l4 4"/>',
    copy:      '<rect x="8" y="8" width="12" height="12" rx="2"/><path d="M16 8V6a2 2 0 0 0-2-2H6a2 2 0 0 0-2 2v8a2 2 0 0 0 2 2h2"/>',
    download:  '<path d="M12 4v11M7 10.5l5 5 5-5"/><path d="M4 19.5h16"/>',
    fileText:  '<path d="M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8z"/><path d="M14 3v5h5M9 13h6M9 17h6"/>',
    trash:     '<path d="M4 7h16M10 11v6M14 11v6M6 7l1 13h10l1-13M9 7V4h6v3"/>',
    refresh:   '<path d="M20 12a8 8 0 1 1-2.34-5.66"/><path d="M20 4v4.5h-4.5"/>',
    play:      '<path d="M7 4.5v15l12-7.5z"/>',
    clock:     '<circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/>',
    bell:      '<path d="M6 16v-5a6 6 0 0 1 12 0v5l1.5 2h-15z"/><path d="M10 20.5a2 2 0 0 0 4 0"/>',
    moon:      '<path d="M20 14.5A8.5 8.5 0 1 1 9.5 4a7.5 7.5 0 0 0 10.5 10.5z"/>',
    sun:       '<circle cx="12" cy="12" r="4"/><path d="M12 2.5v2M12 19.5v2M2.5 12h2M19.5 12h2M5.3 5.3l1.4 1.4M17.3 17.3l1.4 1.4M5.3 18.7l1.4-1.4M17.3 6.7l1.4-1.4"/>',
    arrowUp:   '<path d="M12 19V5M6 11l6-6 6 6"/>',
    arrowDown: '<path d="M12 5v14M6 13l6 6 6-6"/>',

    /* Voz */
    volume:    '<path d="M4 9v6h4l5 4V5L8 9z"/><path d="M16.5 8.5a5 5 0 0 1 0 7M19 6a8.5 8.5 0 0 1 0 12"/>',
    volumeOff: '<path d="M4 9v6h4l5 4V5L8 9z"/><path d="M17 9.5l5 5M22 9.5l-5 5"/>',

    /* Sensores y traje */
    heart:     '<path d="M12 20.5 4.6 13.2a4.6 4.6 0 0 1 6.5-6.5l.9.9.9-.9a4.6 4.6 0 0 1 6.5 6.5z"/>',
    gauge:     '<path d="M4.5 17a8.5 8.5 0 1 1 15 0"/><path d="M12 13l3.5-3.5"/><circle cx="12" cy="13" r="1"/>',
    droplet:   '<path d="M12 3.5s6 6.6 6 10.5a6 6 0 0 1-12 0c0-3.9 6-10.5 6-10.5z"/>',
    thermometer: '<path d="M10 14.5V5a2 2 0 0 1 4 0v9.5a4 4 0 1 1-4 0z"/><path d="M12 9v7"/>',
    suit:      '<path d="M5 19v-7a7 7 0 0 1 14 0v7z"/><path d="M8.5 11.5h7a1.5 1.5 0 0 1 1.5 1.5v1a2 2 0 0 1-2 2H9a2 2 0 0 1-2-2v-1a1.5 1.5 0 0 1 1.5-1.5z"/>',
    tank:      '<rect x="8" y="6" width="8" height="15" rx="3"/><path d="M10 6V3.5h4V6M12 3.5V2"/>',
    wind:      '<path d="M3 9h11a3 3 0 1 0-3-3"/><path d="M3 15h15a3 3 0 1 1-3 3"/><path d="M3 12h6"/>',
    battery:   '<rect x="3" y="7.5" width="16" height="9" rx="2"/><path d="M21 10.5v3M6.5 10.5v3M9.5 10.5v3"/>',
    signal:    '<circle cx="12" cy="12" r="1.5"/><path d="M8.5 15.5a5 5 0 0 1 0-7M15.5 8.5a5 5 0 0 1 0 7M5.6 18.4a9 9 0 0 1 0-12.8M18.4 5.6a9 9 0 0 1 0 12.8"/>',
    link:      '<path d="M10 14a4 4 0 0 0 5.66 0l3-3a4 4 0 0 0-5.66-5.66l-1 1"/><path d="M14 10a4 4 0 0 0-5.66 0l-3 3a4 4 0 0 0 5.66 5.66l1-1"/>',
    unlink:    '<path d="M10 14a4 4 0 0 0 5.66 0l3-3a4 4 0 0 0-5.66-5.66l-1 1"/><path d="M14 10a4 4 0 0 0-5.66 0l-3 3a4 4 0 0 0 5.66 5.66l1-1"/><path d="M4 4l16 16"/>',
    blackbox:  '<rect x="3" y="5" width="18" height="14" rx="2"/><path d="M6.5 12h2.5l1.5-3 2.5 6 1.5-3h3"/>',
    planet:    '<circle cx="12" cy="12" r="5.5"/><path d="M5.2 15.6C3.4 17.4 2.7 19 3.4 19.7c1.3 1.3 6.2-1.4 10.9-6.1s7.4-9.6 6.1-10.9c-.7-.7-2.3 0-4.1 1.8"/>',
    shield:    '<path d="M12 3l7.5 3v5.5c0 4.5-3.2 8.3-7.5 9.5-4.3-1.2-7.5-5-7.5-9.5V6z"/>',
    bed:       '<path d="M3 18V7M3 14h18v4M21 14v-2a3 3 0 0 0-3-3h-7v5"/><circle cx="7" cy="11" r="1.6"/>',

    /* Niveles de riesgo (la forma también comunica, no solo el color) */
    info:      '<circle cx="12" cy="12" r="9"/><path d="M12 11v5.5M12 7.8v.2"/>',
    triangle:  '<path d="M12 3.5 21.5 20h-19z"/><path d="M12 10v4.5M12 17.2v.2"/>',
    octagon:   '<path d="M8.3 3h7.4L21 8.3v7.4L15.7 21H8.3L3 15.7V8.3z"/><path d="M12 7.5V13M12 16.2v.2"/>',

    /* Salud mental */
    smile:     '<circle cx="12" cy="12" r="9"/><path d="M8.5 14.5a4.5 4.5 0 0 0 7 0"/><path d="M9 9.5v.5M15 9.5v.5"/>',
    moodLow:   '<circle cx="12" cy="12" r="9"/><path d="M8.5 16a4.5 4.5 0 0 1 7 0"/><path d="M9 9.5v.5M15 9.5v.5"/>',
    moodMid:   '<circle cx="12" cy="12" r="9"/><path d="M8.5 15h7"/><path d="M9 9.5v.5M15 9.5v.5"/>',
    moodHigh:  '<circle cx="12" cy="12" r="9"/><path d="M8 13.5a4.5 4.5 0 0 0 8 0z"/><path d="M9 9.5v.5M15 9.5v.5"/>',
    energyLow: '<rect x="4" y="14" width="4" height="6" rx="1" fill="currentColor"/><rect x="10" y="10" width="4" height="10" rx="1"/><rect x="16" y="5" width="4" height="15" rx="1"/>',
    energyMid: '<rect x="4" y="14" width="4" height="6" rx="1" fill="currentColor"/><rect x="10" y="10" width="4" height="10" rx="1" fill="currentColor"/><rect x="16" y="5" width="4" height="15" rx="1"/>',
    energyHigh:'<rect x="4" y="14" width="4" height="6" rx="1" fill="currentColor"/><rect x="10" y="10" width="4" height="10" rx="1" fill="currentColor"/><rect x="16" y="5" width="4" height="15" rx="1" fill="currentColor"/>',
  };

  App.icons = I;

  /* Devuelve el <svg>. label = texto para lectores de pantalla (opcional). */
  App.icon = function (name, extraClass, label) {
    var body = I[name] || I.info;
    var a11y = label
      ? ' role="img" aria-label="' + String(label).replace(/"/g, '&quot;') + '"'
      : ' aria-hidden="true" focusable="false"';
    return '<svg class="icon' + (extraClass ? ' ' + extraClass : '') + '" viewBox="0 0 24 24"' + a11y + '>' + body + '</svg>';
  };
})();
