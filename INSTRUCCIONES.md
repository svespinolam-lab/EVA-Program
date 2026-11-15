# EVA · Guía de configuración y simulación (español)

EVA es un sistema de monitoreo biomédico para caminatas espaciales (EVA) que funciona **sin internet**, en una red local en estrella centrada en la PC de cabina:

```
 Teléfono EV1 (app EVA) ─┐                       ┌─ Navegador de la PC: EVA Station (triage, Go/No-Go,
 Teléfono EV2 (app EVA) ─┼── Wi-Fi local ──► eva-station.exe ─┤   protocolos, reportes, caja negra)
 (sensores BLE / simulados)   (hotspot de la PC)   (Go + SQLite) └─ data/eva-station.db
```

- **App móvil (inglés)**: cada explorador. Lee sus sensores (simulados), evalúa el riesgo en el propio teléfono (*edge*), habla las alertas, guarda su caja negra local y envía la telemetría a la PC. Si se corta el Wi-Fi, guarda las lecturas en una cola y las sube al reconectar (*store-and-forward*).
- **EVA Station (programa de PC, inglés)**: el “guy in the chair”. Triage en vivo ordenado por el Índice de Prioridad de Vigilancia (IPV), tablero Go/No-Go, consola de protocolos, reportes y caja negra.
- **Servidor (Go) + base de datos (SQLite)**: un solo ejecutable, `eva-station.exe`, que sirve las dos interfaces, recibe los teléfonos por WebSocket y guarda todo.

> Concepto demostrativo con datos simulados: **no es un dispositivo médico**.

## ⚡ Forma más rápida (sin instalar nada)

1. En GitHub: **Code → Download ZIP** y descomprímelo (o `git clone https://github.com/svespinolam-lab/EVA-Program.git`).
2. Doble clic en **`EVA-Station.bat`**. Se abre el programa de PC en <http://localhost:8080>.
3. En el celular (misma Wi-Fi) abre la dirección que muestra la ventana negra + `/app/`.

Para modificar: edita los archivos de `web/` (interfaces) y recarga el navegador; si cambias código Go (`internal/`, `cmd/`), vuelve a compilar con `tools\build.ps1`.

---

## 1. Requisitos

| Para | Necesitas |
|---|---|
| Ejecutar | Windows 10/11. Nada más: `dist\eva-station.exe` ya trae el servidor, la base de datos y las dos interfaces. |
| Compilar | **Go 1.26 o superior** (<https://go.dev/dl/>). Opcional: Node.js para generar `dist\eva-app.html`. |
| Teléfono | Chrome (Android) o Safari (iPhone), conectado a la misma red Wi-Fi que la PC. |

## 2. Compilar (solo si cambiaste el código Go)

```powershell
powershell -ExecutionPolicy Bypass -File tools\build.ps1
```

Genera `dist\eva-station.exe` (y `dist\eva-app.html`). Si `go` no está en el PATH, el script usa el Go que quedó en `%LOCALAPPDATA%\eva-tools\go`.

## 3. Preparar la red local (sin internet)

1. En la PC: **Configuración → Red e Internet → Zona con cobertura inalámbrica móvil** → actívala (anota nombre y contraseña). También sirve cualquier router Wi-Fi compartido.
2. Conecta el teléfono a esa red.

## 4. Arrancar EVA Station

```powershell
dist\eva-station.exe
```

La consola muestra algo así:

```
  PC program      http://localhost:8080
  Phones (Wi-Fi)  http://192.168.137.1:8080/app/
```

- La primera vez Windows pedirá permiso de **firewall**: permítelo en **redes privadas**.
- Abre <http://localhost:8080> en la PC: es EVA Station.
- La base de datos se crea sola en `data\eva-station.db` con la tripulación de demostración (Ana Ruiz EV1, Luis Ortega EV2, Marta Vidal IV). Para empezar de cero, cierra el programa y borra la carpeta `data`.

Opciones útiles:

| Opción | Qué hace |
|---|---|
| `-port 9090` | Otro puerto |
| `-virtual all` / `-virtual none` | Trajes virtuales para todos los exploradores sin teléfono / para ninguno (por defecto: EV2) |
| `-web web` | Sirve las interfaces desde la carpeta `web` (para editarlas en vivo; se activa solo si ejecutas desde la raíz del proyecto) |
| `-data otra\carpeta` | Dónde guardar la base de datos |

## 5. Enlazar el teléfono

1. En el teléfono abre la dirección **Phones (Wi-Fi)** que mostró la consola (por ejemplo `http://192.168.137.1:8080/app/`). También aparece en la PC con el botón de señal (arriba a la derecha) o en **Simulator**.
2. En la app: **≡ Settings → Station link** → elige quién lleva el teléfono (**Ana Ruiz · EV1**) → activa **Link**.
3. En la PC, el triage mostrará **Phone linked** para EV1. El traje virtual de esa persona se apaga solo: desde ese momento los datos vienen del teléfono.
4. Toca el ícono de bocina en la app para activar la voz (el navegador lo exige).

## 6. Guion de la simulación (demo)

| Paso | En el teléfono (EV1) | En EVA Station (PC) |
|---|---|---|
| 1 · Pre-misión | **Log → Mental health**: ánimo, energía, sueño e hidratación → *Save*. | **Pre-mission · Go/No-Go**: aparece el semáforo (GO / GO con reservas / NO-GO) con un resumen **sin datos privados**. |
| 2 · Inicio | — | **Start EVA** (arranca el reloj T+). EV2 aparece con su traje virtual. |
| 3 · Telemetría | Lecturas cada segundo (FC, SpO₂, temperatura, respiración, HRV, esfuerzo, traje, radiación). | **Live triage**: tarjetas ordenadas por IPV, tendencias de 15 min, diagnóstico contextual (“Heart rate +38 % vs baseline at rest for 6 min”). |
| 4 · Falla | — | **Simulator → EV1 → Hypoxia**. La falla se inyecta **en el teléfono**, que la procesa y la reenvía. |
| 5 · Alerta | Voz de 8 palabras o menos (“Oxygen at 88. Return to airlock now.”) y botones **Copy / I'm OK / Need help**. | Alarma sonora de cabina, banner de emergencia, protocolo sugerido y cuenta regresiva de 30 s. |
| 6 · Escalamiento | Si nadie responde en 30 s… | …la alerta pasa a EV2 (“EV1 not responding. Go assist now.”) y el IPV de EV1 sube a emergencia. |
| 7 · Respuesta | Toca **I'm OK** o **Need help**. | **Protocols & control**: *Acknowledge*, *Send instruction* (frases rápidas), *Report incident* o *Abort EVA*. |
| 8 · Caja negra | Su caja negra local guarda todo. | **Reports**: el incidente congela de T−30 min a T+15 min. Usa *Seal now* para no esperar 15 min y ábrelo: clasificación (súbito / progresivo / falla de sensor), línea de tiempo, gráficas y sello SHA-256. **Black box → Verify integrity**. |
| 9 · Sin red | Apaga el Wi-Fi del teléfono 30 s y vuelve a encenderlo. | Al reconectar llega la cola pendiente (*store-and-forward*) y queda en el registro del operador. |
| 10 · Cierre | — | **End EVA** → **Post-EVA report** (carga fisiológica TRIMP, energía, dosis de radiación, recuperación). **Reports → Monthly health** y **Export data (CSV)**. Imprime con *Print → Save as PDF*. |

## 7. Editar el código

Abre la carpeta del proyecto en VS Code. Lo que más vas a tocar:

| Quiero cambiar | Archivo |
|---|---|
| Colores, tipografía, radios (PC y app a la vez) | `web/shared/css/tokens.css` |
| Logo | `web/shared/logo.png` |
| Pantallas de la PC | `web/pc/js/views.js`, `web/pc/css/station.css` |
| Pantallas del teléfono | `web/app/js/views/*.js`, `web/app/css/` |
| Enlace teléfono ↔ PC | `web/app/js/engine/link.js` (teléfono) · `internal/server/server.go` (PC) |
| Reglas clínicas, IPV, alertas, escalamiento | `internal/engine/rules.go`, `internal/engine/engine.go` |
| Caja negra y clasificación de incidentes | `internal/engine/blackbox.go` |
| Reportes | `internal/engine/reports.go`, `internal/server/reports.tmpl.html` |
| Tablas de la base de datos | `internal/store/schema.sql` (PC) · `db/device_schema.sql` (teléfono nativo) |
| Trajes virtuales y escenarios de falla | `internal/sim/sim.go` (PC) · `web/app/js/engine/telemetry.js` (teléfono) |

Con el programa corriendo desde la raíz del proyecto, los cambios en `web/` se ven al recargar el navegador. Los cambios en Go requieren volver a compilar.

## 8. Qué se optimizó respecto al documento

| Documento | Implementado | Por qué |
|---|---|---|
| Servidor en Python (FastAPI) | **Go** compilado, un solo `.exe` de unos 20 MB con todo embebido | Sin intérprete ni dependencias; menor latencia y consumo (comentario de Sofía Espínola). |
| SQLite | SQLite en modo **WAL**, escritor dedicado, **lotes cada 250 ms** con sentencia preparada, marcas de tiempo enteras (ms) e índices | Miles de lecturas por segundo sin bloquear las consultas del tablero. |
| Umbrales fijos | Línea base personal (media ± DE, Welford; 14 días de adaptación → promedio móvil de 7), **FC ajustada al esfuerzo** (máx. por edad, Tanaka), **mediana de 5 s** para SpO₂, **histéresis** (sube con 3 lecturas y baja con 5) | Menos falsas alarmas, más precisión. |
| — | Detección de **falla de sensor** (valores imposibles, línea plana, saltos) | No se dispara una emergencia falsa por un sensor flojo. |
| Caja negra | Grabación continua + **cadena de hashes SHA-256** + sello de cada incidente (como el registrador de vuelo de un avión) | Cualquier alteración posterior se detecta (*Verify integrity*). |
| Cola de respaldo | Secuencia por lectura, **confirmación tras guardar en disco** e inserción idempotente | No se pierden ni se duplican datos al reconectar. |
| Reportes | Reglas deterministas: **TRIMP de Banister**, gasto energético de **Keytel**, dosis integrada, recuperación estimada | Precisos y explicables, sin aprendizaje automático. |

## 9. Límites conocidos (honestos)

- **Sensores BLE reales**: la app ahora incluye un adaptador inicial para perfiles GATT estándar de frecuencia cardíaca (Heart Rate 0x180D), temperatura (Health Thermometer 0x1809) y SpO₂ (Pulse Oximeter 0x1822). En Diagnosis → Physical health → Pair Bluetooth sensor se puede emparejar un dispositivo compatible. Requiere Android Chrome en HTTPS y permiso Bluetooth. Si no hay hardware emparejado, la interfaz lo identifica como `Not paired · demo data only`; las lecturas simuladas nunca se presentan como hardware real. Presión arterial, respiración, glucosa, sueño, peso, hidratación, dosis acumulada y biometría mensual se introducen manualmente. HRV y perfiles propietarios del traje requieren conocer el UUID/servicio GATT del fabricante; iOS necesita un puente nativo CoreBluetooth.
- **Sincronización clínica mensual**: en Diagnosis → Physical health, el botón `Sync monthly clinical panel to EVA Station` envía únicamente hemoglobina, leucocitos, plaquetas, glucosa en ayunas, caída ortostática, peso y dosis mensual de radiación. La cola se conserva si la estación está desconectada y se reintenta al reconectar. El diario, ánimo, notas y ciclo menstrual no se transmiten.
- **Voz sin internet y reconocimiento de voz (Vosk)**: la síntesis de voz usa la del teléfono. El reconocimiento offline requiere app nativa; por ahora los botones *Copy / I'm OK / Need help* cumplen esa función.
- **P2P si cae la PC** (Nearby Connections o broadcast UDP): no es posible desde un navegador. Hoy el escalamiento al compañero pasa por la PC.
- **SQLCipher**: el driver de SQLite en Go puro no lo incluye. Por diseño, la base de la PC **no guarda el diario privado** (solo el veredicto filtrado). Para cifrar en disco usa BitLocker en la PC o una app nativa con SQLCipher.
- **Tipografías**: Helvetica Neue / Inter / DM Mono se cargan de Google Fonts si hay internet; sin internet se usan las del sistema.
