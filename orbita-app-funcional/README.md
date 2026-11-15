# EVA · App móvil

La interfaz está diseñada como una aplicación de teléfono en orientación vertical, con ancho de móvil, navegación inferior fija, panel de configuración lateral y diseño adaptado a safe areas.

## Probar
Abre `index.html` mediante un servidor estático o GitHub Pages. Para instalarla como PWA, abre la URL desde Chrome/Android o Safari/iOS y usa «Añadir a pantalla de inicio».

## Incluye
- Registro de salud mental: ánimo, energía, duración/calidad de sueño y notas.
- Registro manual de frecuencia cardíaca y presión arterial.
- Diagnóstico descriptivo de los datos introducidos.
- Perfil, historial médico y medicación editable.
- Menú de configuración con exportación CSV e impresión.
- Guardado local en el dispositivo y caché PWA básica.

## Limitaciones importantes
Esta entrega es frontend/PWA: no crea cuentas ni sincroniza con EVA Central, no autentica roles, no se conecta realmente a sensores Bluetooth y no envía notificaciones. Los indicadores son descriptivos y no son diagnósticos médicos. Antes de usar datos clínicos reales, se requiere backend seguro, control de acceso, cifrado, auditoría, consentimiento y revisión de cumplimiento normativo.
