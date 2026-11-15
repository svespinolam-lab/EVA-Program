# EVA · Biomedical EVA monitoring (phone app + EVA Station)

EVA supports a crew during extravehicular activities on the Moon or Mars **without internet**: each explorer's phone reads the suit sensors, classifies risk at the edge, speaks short alerts and keeps a local black box; the cabin PC runs **EVA Station** — Go server + SQLite + the operator program ("guy in the chair") — over a local Wi-Fi star network.

> Concept demonstrator with simulated sensors. **Not a medical device.** Setup guide in Spanish: [`INSTRUCCIONES.md`](INSTRUCCIONES.md).

```
 phone EV1 ─┐  WebSocket /ws/device          ┌─ /            EVA Station (PC program)
 phone EV2 ─┼────── local Wi-Fi ──► eva-station.exe ─┼─ /app/        EVA phone app (served to the phones)
            │   telemetry 1 Hz, readiness,   │  (Go, SQLite)  └─ /reports/…  printable reports
            └── acks ⇄ alerts, instructions  └─ data/eva-station.db
```

## Quick start

**Easiest (no install, Windows):** Code → Download ZIP → unzip → double-click **`EVA-Station.bat`** (uses the ready-made `dist/eva-station.exe`).

To rebuild after changing Go code:

```powershell
powershell -ExecutionPolicy Bypass -File tools\build.ps1   # needs Go ≥ 1.26 → dist\eva-station.exe
dist\eva-station.exe                                        # PC: http://localhost:8080 · phones: http://<PC-IP>:8080/app/
```

Run it from the project root during development: the web front-ends are then served live from `web/` (edit and reload). Flags: `-port`, `-data`, `-web`, `-virtual AST-002|all|none`.

## Repository map

```
cmd/eva-station/main.go     entry point (flags, start-up banner, 1 Hz ticker)
internal/model              shared data types (JSON names match the app)
internal/store              SQLite: schema.sql, seed (demo crew), batching writer, hash chain, queries
internal/engine             triage engine: rules.go (clinical + suit rules, IPV, protocols, voice scripts),
                            engine.go (state, alerts, 30 s escalation, missions, vulnerability, baseline),
                            blackbox.go (incident freeze, classification, timeline, SHA-256 seal), reports.go
internal/sim                virtual suits + failure scenarios for crew members without a phone
internal/server             HTTP routes, WebSocket hub, printable report templates
internal/ws                 minimal dependency-free WebSocket (RFC 6455)
web/shared                  ONE design system for both programs: tokens.css, base.css, controls.css, icons.js, logo.png
web/pc                      EVA Station front-end (vanilla JS): station.js (core, live link) + views.js
web/app                     EVA phone app (vanilla JS, offline-capable), engine/link.js = station link
db/device_schema.sql        SQLite schema for a future native phone app (private diary stays on the device)
docs/                       DESIGN.md (shared design system) · DATABASE.md (tables vs planning document)
tools/build.ps1             build script · tools/bundle.mjs → dist/eva-app.html (app in one file)
```

## Triage: Surveillance Priority Index (IPV, 0–100)

`IPV = acute risk (live telemetry) ⊕ accumulated vulnerability (recent history)`

| Band | Status | Typical triggers |
|---|---|---|
| 90–100 | EMERGENCY | SpO₂ < 90 %, HR > baseline + 60 at rest or ≥ 95 % of age max, core temp ≥ 39 °C, suit pressure < 3.6 psi, CO₂ > 10 mmHg, radiation > 2 mSv/h, no answer to an alert after 30 s, "Need help" |
| 70–89 | RISK | SpO₂ 90–91 %, HR +35 % over baseline at rest (or +30 over effort-expected), temp ≥ 38 °C, telemetry lost > 10 s |
| 40–69 | VULNERABLE | advisory findings, or green vitals with factors: sleep < 6 h × 3 days, fatigue, dehydration, HRV −20 %, baseline deviation (z-score), radiation > 50 % of career, incomplete recovery |
| 0–39 | STABLE | within personal baseline |

Precision measures: personal baseline (Welford mean ± SD, 14-day adaptation then rolling 7 days), effort-aware heart rate (Tanaka HRmax), 5-s SpO₂ median, hysteresis (raise after 3 readings, clear after 5), sensor-fault detection, silent calibration offsets (e.g. luteal phase) applied without revealing the reason.

## Phone ↔ station protocol (`/ws/device`, JSON)

| Phone → station | Station → phone |
|---|---|
| `hello {crewId, device}` | `welcome {mission, crew, alerts}` |
| `telemetry {seq, t, hr, spo2, temp, rr, hrv, rad, effort, suitP, o2, co2, batt, bpSys, bpDia}` | `ack-seq {seq}` (stored in SQLite → drop from the outbox) |
| `batch {items}` (offline queue) · `queue {n}` | `batch-ok {n}` |
| `readiness {day, status, vulnerability, summary, factors, sleepH, hydration, fatigueHigh}` | `alert {alert}` · `alert-update {alert}` · `buddy {…}` (escalation) |
| `ack {alertId, response: COPIED / IM_OK / NEED_HELP}` · `incident` | `instruction {text}` · `abort {reason}` · `inject {scenario}` · `clear` · `mission` · `crew` (1 Hz) |

The private diary (mood, notes, cycle) never leaves the phone: only the readiness verdict, a vulnerability index and factor codes.

## HTTP API (station)

`GET /api/state` · `GET /api/info` · `GET /api/health/database` (SQLite integrity + row counts) · `POST /api/mission/{start|end|abort|new}` · `POST /api/alerts/{id}/ack` · `POST /api/instruction` · `POST /api/crew/{id}/incident` · `POST /api/incidents/{id}/seal` · `GET|POST /api/sim…` · `GET /api/blackbox[/verify]` · `GET /api/export/{table}` (CSV) · `GET /reports/{mission|incident|monthly}/{id}` (print → PDF).
