# EVA · Fase 3 — EVA Station (consola)

**Prompt 3** · rama `fase-3-station` (sobre `fase-2-mobile`) · 15 nov 2026

| Hecho | Verificación |
|---|---|
| **Vista System** (menú Records → System): estado del servidor y de la base (integridad, esquema, tamaño), teléfonos y consolas conectados, modo de emparejamiento, HTTPS y huella, último respaldo. | Navegador: todos los datos visibles. |
| **Sincronización por astronauta**: enlace en línea, teléfono emparejado, última vez visto, última secuencia guardada y mensajes pendientes. | Navegador: Ana en línea, secuencia 11608, 0 pendientes. |
| **Respaldos** desde la consola (crear copia verificada y listar). | Navegador: copia creada. |
| **Usuarios de consola**: crear (operator / flight surgeon) con enlace de acceso mostrado una sola vez; deshabilitar. | Navegador: usuario creado y enlace mostrado. |
| **Auditoría de seguridad** (últimas 60 acciones: quién, desde dónde, qué, resultado; rechazos en rojo). | Navegador. |
| **Caja negra con filtros** por astronauta y por tipo de evento; el filtro se conserva aunque la vista se redibuje. | Navegador: solo «User created» tras 6 s de redibujados. |
| Las secciones administrativas se ocultan si el rol no tiene permiso (el servidor lo impone igualmente, probado en Fase 4). | Código + pruebas de permisos. |

## Pendiente de la Fase 3

- Crear misión y asignar tripulación desde la consola (la API `POST /api/missions` ya existe y está probada).
- Editar expedientes y contactos desde la consola (API `PUT /api/crew/{id}` y `/contacts` probadas).
- Umbrales del IPV configurables por el equipo médico; alarma sonora configurable por nivel.
- Reportes de alertas, telemetría, radiación e intervenciones (hoy disponibles como CSV).
