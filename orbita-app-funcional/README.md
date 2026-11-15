# EVA — Offline-first crew health and mission monitoring

The mobile interface follows the supplied phone mockups (portrait layout, bottom navigation, profile and settings drawer). The application language is English, as required by the project plan.

## Components

- `index.html` + `mobile/eva-mission.js`: phone PWA, encrypted private check-ins, pre-mission readiness verdict, local alert rules, voice prompts, BLE heart-rate and supported pulse-oximeter GATT profiles, telemetry queue and store-and-forward.
- `server/main.go`: Go cabin server, SQLite event store, WebSocket telemetry, personal-baseline calculation, triage ranking, alert acknowledgement, 30-second escalation, post-alert black-box capture and report APIs.
- `dashboard/index.html`: separate cabin / "guy in the chair" dashboard with live triage, demo telemetry simulator and post-mission report.
- `sw.js`: offline app shell cache.
- `manifest.webmanifest`: portrait PWA metadata.

## Run the cabin station

Install Go 1.23 or newer. From this directory:

```sh
go mod tidy
go run ./server
```

Open the cabin dashboard at `http://localhost:8000/`. The phone app is served from `/app/index.html`. Put the cabin PC and phones on the same isolated Wi-Fi network; no Internet connection is needed for the local Go service.

For real phones, configure a TLS certificate trusted by the phone and set `EVA_TLS_CERT` and `EVA_TLS_KEY` before starting the server. HTTPS/WSS is required for installable PWA service workers and Web Bluetooth in most mobile browsers. Do not use a self-signed certificate in a real mission unless it has been explicitly installed and trusted on each device.

## Local operator access

Set `EVA_OPERATOR_TOKEN` to a long random secret on the cabin PC. The monthly clinical report and incident endpoints require an Authorization bearer token. Do not distribute this token to crew phones. Configure firewall rules to permit only the isolated cabin network.

## Privacy boundary

The daily psychological / wellness check-in, screening responses, private notes, and optional menstrual-cycle data are encrypted with WebCrypto AES-GCM in the phone's IndexedDB. They are not included in telemetry or pre-mission requests. The cabin receives only the readiness traffic-light, vulnerability score and a short non-diary summary, plus suit telemetry and operational alerts. The app applies the optional luteal-phase calibration locally before sending telemetry.

## Important integration status and limitations

This is an integration prototype, not certified flight or clinical software.

- Web Bluetooth support depends on Android/browser support and the sensor's exact GATT profile. Standard Heart Rate and Pulse Oximeter profiles are supported where exposed; vendor-specific devices need an adapter.
- The mobile browser needs to remain active for continuous sensor capture, local voice alerts and foreground timers. Background execution and guaranteed alarm delivery require a native Android/iOS application.
- The 30-second server escalation notifies connected cabin clients. Cross-device P2P / Nearby Connections fallback is not yet implemented.
- Local private data uses encrypted IndexedDB, not SQLCipher. The cabin SQLite database is not yet encrypted at rest; use full-disk encryption for demonstrations and SQLCipher/key management before storing operational medical data.
- The Go server includes a rolling baseline and incident capture workflow, but the initial adaptation baseline needs 14 days of valid samples. Clinical thresholds, phase calibration and all protocols must be reviewed and approved by a flight surgeon before operational use.
- Monthly report APIs are operator-token protected; the full monthly lab-entry workflow and PDF/HTML report templates still need completion.
- There is no production user authentication, signed device enrollment, role-based account management, audit log, end-to-end encryption, or certified medical-device integration. Do not use real patient data until those controls and regulatory reviews are complete.
- The dashboard simulator creates synthetic telemetry and must be used only for demonstrations.

## Network / demo sequence

1. Start the Go service on the cabin PC.
2. Connect the phones to the isolated cabin Wi-Fi.
3. Open `https://<cabin-host>:8000/app/index.html` (with trusted TLS) and configure the station URL.
4. Save a private daily check-in; explicitly share only the readiness verdict.
5. Pair supported BLE sensors or enter demo values, then send telemetry.
6. Open the cabin dashboard to observe live triage, acknowledge alerts, and view the mission summary.
7. Trigger a simulator warning/emergency from the dashboard to test voice and escalation flows.
