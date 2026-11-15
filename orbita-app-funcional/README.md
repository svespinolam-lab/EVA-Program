# ÓRBITA — aplicación funcional (prototipo frontend)

## Ejecutar
1. Descomprime `orbita-app.zip`.
2. Abre `index.html` en un navegador moderno (Chrome, Edge, Firefox o Safari).
3. No requiere instalación de paquetes ni servidor para probarse.

## Funciones incluidas
- Navegación entre Registro, Diagnóstico, Perfil y Configuración.
- Registro de ánimo, energía, sueño, frecuencia cardíaca, presión arterial y notas.
- Guardado local automático al enviar cada formulario (localStorage del navegador).
- Resumen descriptivo de registros recientes y últimos siete registros.
- Edición de perfil, información médica y medicación.
- Exportación CSV e impresión desde el navegador.
- Diseño adaptable a escritorio y móvil.

## Importante: alcance de esta versión
Es un prototipo funcional de frontend. Los datos permanecen en el navegador y no se sincronizan entre dispositivos ni se envían a un servidor. No incluye cuentas, autenticación, base de datos remota, roles reales, cifrado de extremo a extremo, integraciones con sensores ni notificaciones. El módulo llamado “Diagnóstico” solo resume datos introducidos por el usuario; no realiza diagnósticos médicos ni sustituye atención profesional.

Para producción, especialmente por tratar datos de salud, hace falta definir backend, autenticación, permisos, cifrado, copias de seguridad, política de privacidad y cumplimiento legal aplicable. No usar datos reales sensibles en esta demo.
