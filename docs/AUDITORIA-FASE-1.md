# EVA · Auditoría general y plan por fases

**Fase 1 de 7** (Prompt 1 de los «Prompts maestros») · rama `fase-1-auditoria` · base `main` @ `6268a96` · 15 nov 2026
Anexo con evidencia línea por línea de la app móvil: [`docs/audit/mobile-audit.md`](audit/mobile-audit.md) (auditoría de solo lectura hecha con Codex).

Leyenda: ✅ implementado **y con prueba automática** · 🟡 implementado, verificado a mano (sin prueba automática) · 🟠 parcial · ❌ no existe.

---

## 0. Resumen ejecutivo

- **Estado real:** EVA es un **prototipo integrado y demostrable sin internet**: un solo ejecutable (servidor Go + SQLite + consola de PC + app web del teléfono). El flujo teléfono → estación → base de datos → alerta → escalamiento → caja negra → reportes funciona de punta a punta.
- **No cumple todavía los prompts 2–7 completos.** Las brechas grandes están en EVA Mobile (perfil completo, 12 parámetros con historial de 7 días, salud mental de 8 secciones, ciclo menstrual, BLE real), en el backend (migraciones versionadas, identidad de dispositivos, roles, respaldos) y en el perímetro sin GPS (no existe).
- **Antes de esta fase había 0 pruebas automáticas** y la estación no tenía **ninguna autenticación**: cualquier teléfono en la red podía abrir la consola, abortar la EVA o descargar todos los expedientes médicos (`/api/export/…`), y cualquier página web podía enviarle órdenes (`Access-Control-Allow-Origin: *`).
- **Esta fase corrigió 10 problemas críticos** (sección 5), agregó **15 pruebas automáticas** (unidad, persistencia, seguridad y WebSocket) y **CI en GitHub Actions**.

## 1. Ramas del repositorio

| Rama | Estado | Recomendación |
|---|---|---|
| `main` = `integracion-eva` (`6268a96`) | Base canónica: estación + app + docs + manual. | Seguir trabajando desde aquí, por fases, con PR. |
| `app-movil` | **Prototipo paralelo** «orbita-app-funcional» (12 archivos, 474 líneas, servidor Go propio). Esquema **incompatible** con `main` (`astronauta_id`, timestamps como texto, `/api/telemetry`, `/ws/monitor`). | **No fusionar.** Sus ideas valiosas ya se portaron: token de operador → `internal/server/auth.go`; CI → `.github/workflows/ci.yml`; ID de evento para deduplicar → plan Fase 4 (hoy la clave `(crew_id, ts)` + `seq` ya evita duplicados, con prueba). Conservar como referencia histórica. |
| `dev-backend`, `programa-pc` | Vacías: solo el commit inicial (anteriores a `main`). | Borrarlas, o recrearlas desde `main` si se van a usar. |

## 2. Arquitectura requerida vs. actual

| Componente requerido | Dónde está | Estado |
|---|---|---|
| **EVA Mobile** | `web/app/` (HTML/JS sin dependencias, servida por la estación en `/app/`; `dist/eva-app.html` autocontenido) | 🟠 funcional para demo; faltan módulos del Prompt 2 |
| **EVA Station** (consola) | `web/pc/` (servida en `/`) | 🟡 triage, Go/No-Go, protocolos, tripulación, reportes, caja negra, simulador |
| **EVA Backend** | `cmd/eva-station` + `internal/{server,engine,store,ws,model}` + SQLite (`modernc.org/sqlite`, sin cgo) | 🟡 / ✅ según módulo (sección 3.1) |
| **Suit Telemetry** | Simulación: `internal/sim` (trajes virtuales) y `web/app/js/engine/telemetry.js`; BLE: `web/app/js/engine/ble.js` (3 perfiles GATT estándar, sin UI) | 🟠 solo simulado; BLE no alcanzable desde la interfaz |
| **Command Dashboard** | Vistas *Live triage* + *Protocols & control* de la consola | 🟡 |
| Diseño compartido | `web/shared/` (tokens, componentes, íconos, logo) | 🟡 |

## 3. Inventario de funcionalidades

### 3.1 Backend, servidor y base de datos (Prompt 4)

| Requisito | Estado | Evidencia / brecha |
|---|---|---|
| Funciona sin internet | ✅ | Un ejecutable; las fuentes de Google son opcionales (respaldo del sistema). |
| Tablas: tripulación, línea base, misiones, telemetría, preparación diaria, alertas, incidentes, bitácora del operador, cadena de la caja negra, resumen post-EVA, panel mensual | 🟡 | `internal/store/schema.sql` (11 tablas + 2 vistas), claves foráneas activas, índices y `CHECK`. |
| Tablas faltantes: asignación de tripulación por misión, sensores, dispositivos, protocolos, confirmaciones (historial), sincronización, usuarios, roles, auditoría de accesos, contactos de emergencia | ❌ | Hoy la tripulación es fija (semilla) y los protocolos viven en código (`internal/engine/rules.go`). |
| Migraciones versionadas | ❌ | Solo `CREATE … IF NOT EXISTS`; no hay `PRAGMA user_version`. |
| Persistencia tras reinicio | ✅ | `TestDataSurvivesRestart`. |
| Telemetría idempotente (sin duplicados) | ✅ | Clave única `(crew_id, ts)`; `TestTelemetryReplayIsIdempotentAndAcknowledged`, `TestPhoneLinkTelemetryAckAndDedup`. |
| Caja negra encadenada con SHA-256 | ✅ | `TestBlackboxChainDetectsTampering`. |
| API REST (25 rutas: estado, misión, alertas, instrucciones, incidentes, simulador, caja negra, CSV, reportes) | 🟡 | `internal/server/server.go`. Faltan: alta/edición de astronautas, creación de misión con tripulación, sensores, estado de sincronización, respaldo/restauración. |
| WebSocket bidireccional teléfono ↔ estación | ✅ | `/ws/device`: hello/welcome, telemetry, batch, queue, readiness, ack, incident, monthly-health; ← alert, buddy, instruction, abort, inject, ack-seq. Prueba de integración real. |
| Confirmación de recepción (store-and-forward) | ✅ | `ack-seq` tras el commit en BD; el teléfono borra solo lo confirmado. |
| Paquetes atrasados | 🟡 | `backfill` reconstruye lecturas viejas sin disparar alertas en vivo. |
| Recuperar eventos pendientes hacia el teléfono | 🟠 | Las alertas abiertas se reenvían en `welcome`; **las instrucciones enviadas mientras el teléfono estaba desconectado se pierden**. |
| Identidad del dispositivo | ❌ | `hello` solo pide un `crewId` válido: un teléfono puede hacerse pasar por otro astronauta. |
| Autenticación / roles | ✅ operador · ❌ roles | **Nuevo en F1:** acceso de operador (PC local o token) con pruebas. Faltan roles (cirujano de vuelo vs. operador) y usuarios. |
| Comunicación cifrada | 🟠 | TLS opcional (`-tls-cert/-tls-key`); no es el modo por defecto. |
| Respaldos y restauración | ❌ | — |

### 3.2 EVA Station (Prompt 3)

| Requisito | Estado | Evidencia / brecha |
|---|---|---|
| Dashboard: misión activa, servidor, teléfonos conectados, alertas | 🟡 | Barra superior + contadores. Falta: estado de la BD y de los sensores visibles en el dashboard (existe `/api/health/database`). |
| IPV 0–100, bandas 90/70/40, orden automático | 🟡 | Etiqueta «Preventive watch» = banda «Vulnerable» del documento. Cálculo determinista en `internal/engine/rules.go`. Falta: umbrales **configurables por el equipo médico** (hoy en código). |
| Tarjeta: identidad, estado, signos, tendencia 15 min, alertas, motivo, protocolo, conexión | 🟡 | Completo. **Nuevo en F1:** procedencia «Phone linked · simulated data / · sensors». |
| Alertas: visuales, sonoras, niveles, confirmación, quién confirmó, instrucciones, escalamiento 30 s, historial, resolución | ✅ escalamiento y resolución · 🟡 resto | `TestHypoxiaRaisesAlertEscalatesAt30sAndResolves`. La alarma solo se enciende/apaga (no configurable por nivel). |
| Control de misión: crear, configurar tripulación, asignar, iniciar, supervisar, incidentes, finalizar, informe | 🟠 | Iniciar/terminar/abortar/nueva + incidentes + informe ✅. Configurar/asignar tripulación ❌ (los roles solo se asignan en la app). |
| Caja negra: cronología, lecturas, alertas, desconexiones, confirmaciones, hashes, verificar | ✅ / 🟡 | Congelamiento T−30 … T+15 min con contexto de 7 días. **Faltan búsqueda y filtros por astronauta/misión** y revisar el desborde del JSON en la tabla. |
| Reportes: mensual, post-EVA, incidente, CSV, PDF (imprimir) | 🟡 | Faltan: resumen de alertas, resumen de telemetría, reporte de radiación e historial de intervenciones como reportes propios (hoy solo como CSV). |

### 3.3 EVA Mobile (Prompt 2) — resumen del anexo

| Bloque | Estado | Lo principal |
|---|---|---|
| A. Perfil | 🟠 | Identidad, edad y sexo editables; historial, alergias y medicación solo de ejemplo; **faltan suplementos, duración de misión y contactos de emergencia**. |
| B. 12 parámetros | 🟠 | Las 12 tarjetas existen; **ninguna tiene fecha/hora por lectura ni tendencia de 7 días**; semáforo solo en 6. |
| C. Análisis periódicos | ❌ / 🟠 | Hemoglobina, leucocitos y plaquetas definidos pero **nunca se muestran**; ejercicio y variación de peso no existen. |
| D. Salud mental (8 secciones) | 🟠 | Solo ánimo, energía y sueño; **faltan 6 secciones y la pregunta de seguridad personal**. |
| E. Ciclo menstrual | ❌ | No existe en el teléfono (la estación acepta calibraciones anónimas). |
| F. BLE | 🟠 | Perfiles GATT estándar (sin UUID inventados ✅), pero **sin botón para emparejar**, sin lista de dispositivos, sin batería ni reconexión; detección de desconexión defectuosa. |
| G. Offline y sincronización | 🟡 | Cola persistente con confirmación ✅; **las mediciones manuales se perdían al recargar (corregido en F1)**; sin service worker. |
| Datos simulados vs. reales | 🟠 | Varias fugas de «demo» presentado como real (sección 4); 3 corregidas en F1. |

### 3.4 Simulador (Prompt 5)

| Requisito | Estado |
|---|---|
| Capa independiente con modos DEMO / SIMULATED / REAL | 🟠 Hay simulación en la estación y en el teléfono y la columna `source` (`device`/`virtual`/`replay`), pero no un selector de modo explícito ni procedencia por métrica. |
| 12 sensores simulados | 🟠 10 continuos en la estación; glucosa, sueño, peso e hidratación no se simulan. |
| 12 escenarios | 🟠 Existen 9 (taquicardia, hipoxia, temperatura, fuga, CO₂, batería, radiación, sensor, señal). **Faltan:** presión arterial alterada, fatiga acumulada, paquetes duplicados y caída total de la red. |
| 3 astronautas transmitiendo a la vez | 🟠 La semilla tiene 2 exploradores (EV1, EV2) + 1 IV en cabina. |

### 3.5 Biometric Flight Recorder y perímetro sin GPS (Prompt 6)

| Requisito | Estado |
|---|---|
| Recorder: signos, esfuerzo, radiación, traje, conexión, alertas, acciones, cronología, integridad | ✅ / 🟡 (caja negra + incidentes + línea de tiempo) |
| Clasificación: súbito, progresivo, falla de sensor | 🟡 (`internal/engine/blackbox.go`) · **pérdida de comunicación ❌** |
| GPS-Free Hazard Perimeter (ToF) | ❌ No existe nada (ni simulador). |

### 3.6 Integración y documentación (Prompt 7)

Existen: `README.md`, `INSTRUCCIONES.md`, `docs/DESIGN.md`, `docs/DATABASE.md` y el manual en español (`docs/Manual-EVA-ES.docx`). Faltan: contrato API/WebSocket formal, diagramas de componentes y de BD, guía de respaldo, lista de hardware, lista de pruebas superadas/pendientes y pruebas de carga.

## 4. Errores y riesgos encontrados

| ID | Sev. | Hallazgo | Estado |
|---|---|---|---|
| S1 | **Crítica** | Sin autenticación: cualquier dispositivo de la red controla la misión y descarga expedientes médicos. | ✅ Corregido F1 |
| S2 | **Crítica** | `Access-Control-Allow-Origin: *` y sin defensa CSRF: una página web cualquiera puede dar órdenes a la estación (incluso desde el navegador del operador). | ✅ Corregido F1 |
| S3 | **Crítica** | Cada teléfono recibía el **expediente médico de toda la tripulación** (alergias, padecimientos, medicación, tipo de sangre…) y el IPV/diagnóstico de los demás cada segundo. | ✅ Corregido F1 |
| S4 | **Crítica** | Un teléfono podía **confirmar (silenciar) la alerta de otro astronauta**. | ✅ Corregido F1 |
| S5 | Alta | Sin límite de tamaño en el cuerpo de las peticiones (riesgo de saturación). | ✅ Corregido F1 (1 MiB) |
| S6 | **Crítica** | Identidad del teléfono no verificada (`hello` con cualquier `crewId`). | Pendiente → Fase 4 (emparejamiento por dispositivo) |
| S7 | Alta | TLS no es el modo por defecto. | Pendiente → Fase 4 |
| D1 | **Crítica** | La consola mostraba «Phone linked» aunque el teléfono enviara **datos simulados**. | ✅ Corregido F1 |
| D2 | **Crítica** | Un paquete con un solo canal real se marcaba entero como «device» (lo simulado viajaba como real). | ✅ Corregido F1 (criterio conservador) |
| D3 | Alta | La etiqueta «Demo» de los mosaicos del traje desaparecía al primer segundo. | ✅ Corregido F1 |
| D4 | Alta | Gráficas de hardware dibujadas con valores simulados; presión arterial inventada «hace 20 min»; ejemplos de perfil sin etiqueta. | Pendiente → Fase 2/5 |
| P1 | **Crítica** | Las mediciones manuales del teléfono **se perdían al recargar** aunque la pantalla dice que se guardan. | ✅ Corregido F1 |
| P2 | Alta | Sincronización mensual rota: la expresión regular rechazaba meses válidos («2026-11»). | ✅ Corregido F1 (la UI de envío sigue pendiente → Fase 2) |
| P3 | Media | Instrucciones enviadas con el teléfono desconectado se pierden; la cola del teléfono descarta lo más viejo pasadas 20 000 lecturas (~5,6 h). | Pendiente → Fase 4 |
| P4 | Media | Sin migraciones versionadas ni respaldos. | Pendiente → Fase 4 |
| Q1 | Alta | **0 pruebas automáticas** y sin CI. | ✅ 15 pruebas + CI |
| Q2 | Media | Lógica duplicada: 4 clasificadores de riesgo (config, risk.js, log.js, Go) con umbrales distintos; 3 presentaciones de salud física. | Pendiente → Fase 2/4 |
| R1 | Media | Ramas duplicadas o vacías (`app-movil`, `dev-backend`, `programa-pc`). | Documentado (sección 1) |
| PV1 | Media (decisión) | La preparación diaria envía horas exactas de sueño/hidratación/fatiga; la pantalla de la PC dice que solo llega «veredicto, índice y códigos». | **Decisión del equipo médico** → Fase 4 |

## 5. Correcciones de la Fase 1 (todas con prueba)

1. **Acceso de operador** (`internal/server/auth.go`): la consola, la API, los reportes y las exportaciones solo desde la PC de la estación o con el token de operador (`/?operator=…` una vez → cookie HttpOnly/SameSite=Strict, o `Authorization: Bearer`). Los teléfonos conservan `/app/`, `/shared/`, `/ws/device` y `GET /api/health`. — `TestPhonesCannotUseOperatorRoutes`, `TestPhonesKeepTheirPublicRoutes`, `TestOperatorAccess`.
2. **Anti-CSRF y anti secuestro de WebSocket**: órdenes y consola en vivo solo del mismo origen; sin CORS comodín; cabeceras `nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy: no-referrer`. — `TestCrossSiteRequestsAreBlocked`, `TestSecurityHeadersAndNoWildcardCORS`.
3. **Límite de 1 MiB** por petición. — `TestOversizedBodiesAreRejected`.
4. **Privacidad hacia los teléfonos**: `welcome` solo lleva el directorio público; la difusión periódica ya no incluye IPV ni diagnóstico. — `TestPhonesNeverReceiveOtherCrewMedicalRecords` + prueba WebSocket.
5. **Confirmaciones solo del propio astronauta** (`Engine.AckByCrew`). — `TestPhoneCannotAcknowledgeAnotherCrewAlert`.
6. **Procedencia en la consola**: «Phone linked · simulated data» vs. «· sensors». — `TestConsoleSeesReadingProvenance`.
7. **Etiquetado conservador en el teléfono**: un paquete es «device» solo si todos los canales vienen de hardware. — verificado en navegador.
8. **Etiqueta «Demo ·» permanente** en los mosaicos del traje. — verificado en navegador.
9. **Mediciones manuales persistentes** (`eva.physicalManual`). — verificado en navegador (guardar → recargar → se conserva).
10. **Expresión regular del panel mensual** corregida. — verificado (acepta 2026-11, rechaza 2026-13).

Además: reloj inyectable (`store.Now`) para probar el escalamiento de 30 s sin esperar, y **CI** (`.github/workflows/ci.yml`): `go vet`, pruebas, compilación sin cgo y revisión de sintaxis de todo el JavaScript; el detector de carreras corre como informativo.

## 6. Dependencias

- **Go** ≥ 1.26 (local: 1.27.4) · **modernc.org/sqlite** v1.62.0 (Go puro, sin cgo). Sin otras dependencias de servidor.
- **Front-ends:** HTML/CSS/JS sin librerías. Google Fonts opcional (hay respaldo del sistema). Node solo para `tools/bundle.mjs` y revisiones.
- **Web Bluetooth:** requiere HTTPS y Chrome en Android; **no existe en iOS** (Safari/Chrome de iPhone) → un iPhone no podrá leer sensores BLE desde la web sin una app nativa.
- Sin nube ni servicios externos.

## 7. Plan por fases

Orden recomendado: **4 → 2 → 3 → 5 → 6 → 7**. La Fase 4 define los contratos de datos, las migraciones y la identidad de dispositivos que necesitan la app y la consola; hacerlas antes obligaría a rehacer trabajo.

### Fase 4 — Backend, BD y seguridad (Prompt 4)
- P0: migraciones versionadas (`PRAGMA user_version`) desde el esquema actual, con prueba de actualización de una BD vieja.
- P0: **emparejamiento de dispositivos** (código de un solo uso mostrado en la consola → token por teléfono) y ACK/telemetría ligados a ese token.
- P0: tablas `mission_crew`, `devices`, `sensors`, `alert_responses` (historial de confirmaciones), `audit_log`, `users/roles` (operador, cirujano de vuelo, astronauta).
- P1: respaldo y restauración (`VACUUM INTO` + verificación de la cadena) con prueba de restauración; cola de mensajes salientes al teléfono (que las instrucciones no se pierdan); ID de evento único.
- P1: TLS por defecto con certificado local; contrato API/WebSocket documentado (`docs/API.md`, `docs/WEBSOCKET.md`).
- Criterio de terminado: pruebas de migración, emparejamiento, permisos por rol, respaldo/restauración y reconexión.

### Fase 2 — EVA Mobile (Prompt 2)
- P0: procedencia por métrica (DEMO / SIMULATED / REAL) en UI, historial, riesgo, envío y reportes; quitar la presión arterial inventada; gráficas solo con lecturas reales cuando hay hardware.
- P0: modelo único de medición `{valor, unidad, fecha/hora, fuente, estado del sensor}` para los 12 parámetros, con historial y **tendencia de 7 días**, semáforo, línea base, instrucciones y qué hacer.
- P1: perfil completo (suplementos, contactos de emergencia, duración de misión, estatura/peso editables); análisis periódicos visibles y sincronizables; 8 secciones de salud mental + pregunta de seguridad; BLE con lista de dispositivos, batería, desconexión y reconexión.
- P2: módulo menstrual opcional y privado; service worker para abrir sin red; unificar los 4 clasificadores.
- Criterio de terminado: pruebas de cada módulo en navegador de teléfono real (Android + iPhone).

### Fase 3 — EVA Station (Prompt 3)
- P1: estado de BD, sensores y última sincronización en el dashboard; umbrales del IPV configurables y auditados; alarma configurable por nivel.
- P1: crear misión y asignar tripulación desde la consola; búsqueda y filtros en la caja negra; JSON sin desbordes.
- P1: reportes de alertas, telemetría, radiación e intervenciones (PDF + CSV).

### Fase 5 — Simulador (Prompt 5)
- Selector de modo DEMO/SIMULATED/REAL; los 12 sensores; los escenarios faltantes (presión arterial, fatiga acumulada, paquetes duplicados, caída total de red); 3 astronautas transmitiendo a la vez; interfaz de adaptador para hardware real.

### Fase 6 — Flight Recorder y perímetro sin GPS (Prompt 6)
- Clase «pérdida de comunicación» en incidentes; reconstrucción cronológica completa.
- Perímetro ToF: modelo (anclas, timestamps, tiempo de vuelo, distancia ± incertidumbre, trilateración) con **simulador claramente marcado como ficticio**; vista en la consola; documento del hardware necesario (radios UWB, anclas, sincronización). Sin afirmar precisión de 1 m.

### Fase 7 — Integración, pruebas y entrega (Prompt 7)
- Los 22 pasos de la demo como prueba automatizada de extremo a extremo; pruebas de carga y de desconexión; documentación final completa; informe en 3 categorías.

## 8. Limitaciones externas (no se pueden validar sin hardware)

Sensores reales del traje y sus protocolos (no se inventan), Web Bluetooth en iPhone (no soportado), radios de tiempo de vuelo y anclas para el perímetro, y validación clínica de umbrales por un médico. Todo lo simulado se etiqueta como tal.

## 9. Cómo verificar esta fase

```bash
go vet ./... && go test -count=1 ./...
```

En GitHub, la pestaña **Actions** ejecuta lo mismo en cada push y PR.
