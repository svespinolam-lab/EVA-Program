# EVA · Fase 4 — Backend, base de datos y seguridad

**Prompt 4** de los «Prompts maestros» · rama `fase-4-backend` (sobre `fase-1-auditoria`) · 15 nov 2026
Contratos: [`docs/API.md`](API.md) · [`docs/WEBSOCKET.md`](WEBSOCKET.md) · Esquema: [`docs/DATABASE.md`](DATABASE.md)

Leyenda: ✅ implementado **y con prueba automática** · 🟢 verificado además en navegador real · 🟠 parcial · ⏭️ siguiente fase.

## 1. Qué se hizo

| Requisito del Prompt 4 | Estado | Cómo se verifica |
|---|---|---|
| **Migraciones versionadas** (`PRAGMA user_version` + `schema_migrations`) | ✅ 🟢 | `TestOldDatabaseUpgradesWithoutLosingData`, `TestFreshDatabaseIsLatestWithRoles`. En vivo: la base de prueba subió de **v0 a v2** sin perder datos. |
| Copia de seguridad **automática antes de migrar** | ✅ 🟢 | Misma prueba + archivo real `eva-pre-migration-v0-….db`. |
| Tablas nuevas: roles, usuarios, auditoría, dispositivos, tripulación por misión, sensores, historial de respuestas, cola de mensajes, protocolos, contactos de emergencia, `event_id` | ✅ | Prueba de migración (existen todas) + pruebas de cada API. |
| **Autenticación y roles** (operator / surgeon) con permisos `view, medical, respond, command, admin` | ✅ | `TestRolePermissions` (el cirujano puede exportar y dar instrucciones, pero no iniciar la misión, inyectar fallas, crear usuarios ni emparejar). |
| **Auditoría** de cambios, lecturas clínicas y accesos rechazados | ✅ | `TestAuditLogRecordsChangesClinicalReadsAndRefusals`. |
| **Identidad del teléfono** (emparejamiento con código de un solo uso, 5 min; token por dispositivo; revocación) | ✅ 🟢 | `TestPhonePairingLifecycle` (sin emparejar → rechazado; código → token; código reutilizado → rechazado; token de otro astronauta → rechazado; revocar → desconecta y deja de servir). En vivo: código «061 167» → teléfono en línea y sincronizó 36 lecturas pendientes. |
| Mensajes que **no se pierden** (instrucciones, alertas al compañero, abortos) | ✅ 🟢 | `TestInstructionWaitsForOfflinePhoneUntilAcknowledged`. En vivo: instrucción enviada con el teléfono apagado → llegó al reconectar, **una sola vez**. |
| **ID único de evento** por lectura (`<dispositivo>:<secuencia>`) | ✅ | `TestPairedReadingsGetUniqueEventIDs` + índice único (prueba de migración). |
| Sin duplicados en reconexión / reenvíos | ✅ | `TestTelemetryReplayIsIdempotentAndAcknowledged`, `TestPhoneLinkTelemetryAckAndDedup`. |
| **Respaldos verificados** (integridad + cadena SHA-256), poda, **restauración** que rechaza copias alteradas | ✅ | 6 pruebas de `backup_test.go` (hechas con Codex) + `TestBackupsStatusSyncSensorsAndBlackboxFilter`. |
| Respaldo automático periódico (`-backup-every`, `-backup-keep`) | 🟠 | Código en `cmd/eva-station`; usa las funciones probadas, sin prueba del temporizador. |
| **Comunicación cifrada**: HTTPS en 8443 con certificado local (o uno propio) | ✅ 🟢 | 4 pruebas de `tlsutil` (hechas con Codex). En vivo: `https://localhost:8443/api/health` responde. |
| API: astronautas (alta/edición, contactos, signos vitales), misiones con tripulación, alertas e historial de respuestas, protocolos, sensores, sincronización, estado del servidor, caja negra con filtros, usuarios, dispositivos, respaldos, auditoría | ✅ | `TestCrewRecordsAndContacts`, `TestMissionWithAssignedCrew`, `TestBackupsStatusSyncSensorsAndBlackboxFilter`. |
| Validación de entradas | ✅ | Edad/estatura/peso/rol/sexo (`ValidateCrew`), misión (id, duración, al menos un EV), roles de asignación, sensores (CHECK), límite de 1 MiB. |
| Contratos API y WebSocket documentados | ✅ | `docs/API.md`, `docs/WEBSOCKET.md` (redactados con Codex a partir del código y revisados). |

**Total de pruebas automáticas: 35 (todas pasan)**, desde 15 al terminar la Fase 1.

## 2. Qué cambia para usar EVA

1. **Emparejar cada teléfono una vez.** La estación ya no acepta lecturas de un teléfono anónimo. En la PC: **Simulator → Pair a phone → Pair EV1 · Ana Ruiz** → aparece un código. En el teléfono: **≡ → Station link → Pairing** → escribir el código → **Pair**. Para demos rápidas existe `-pairing open` (sin código; solo demostración).
2. **HTTPS** disponible en el puerto 8443. Con el certificado local el navegador avisa la primera vez; la huella **TLS SHA-256** de la ventana negra sirve para comprobar que es la estación correcta. HTTP en 8080 sigue igual.
3. **Usuarios personales**: `eva-station.exe -create-user "Dra. Vega" -role surgeon` imprime su enlace (una sola vez).
4. **Respaldos** en `data\backups` y **restauración** con `-restore` (estación cerrada). Ver `INSTRUCCIONES.md`.

## 3. Pendiente (y en qué fase)

| Pendiente | Fase |
|---|---|
| Pantallas en la consola para usuarios, respaldos, auditoría, crear misión/asignar tripulación y editar expedientes (hoy existen la API y las pruebas; en la consola solo el emparejamiento). | ⏭️ 3 |
| Registrar sensores desde la app al emparejar Bluetooth (la API ya existe). | ⏭️ 2 |
| Procedencia **por métrica** (hoy por paquete, con criterio conservador). | ⏭️ 2 / 5 |
| La cola del teléfono descarta lo más viejo pasadas 20 000 lecturas (~5,6 h sin conexión). | ⏭️ 2 |
| Decisión del equipo médico: ¿la preparación diaria debe enviar horas exactas de sueño/hidratación o solo códigos? | Decisión |
| Certificado HTTPS de confianza (sin aviso) requiere instalar una CA propia en los teléfonos. | Limitación externa |
| Cifrado de la base en disco (los datos están en la laptop de la estación; protegerla con el cifrado del sistema operativo). | Limitación / decisión |
