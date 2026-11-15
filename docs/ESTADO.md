# EVA · Estado del proyecto (para retomar)

Actualizado: 15 nov 2026. Repositorio: https://github.com/svespinolam-lab/EVA-Program

## Ramas y PR (fusionar en este orden)

| # | Rama | Contenido | Estado |
|---|---|---|---|
| 1 | `fase-1-auditoria` → `main` | Auditoría, seguridad crítica, privacidad, 15 pruebas, CI | Subida · CI verde · **PR por crear** |
| 2 | `fase-4-backend` → `fase-1-auditoria` | Migraciones, roles, auditoría, emparejamiento, mensajes durables, respaldos, HTTPS, API | Subida · **PR por crear** |
| 3 | `fase-2-mobile` → `fase-4-backend` | App: salud mental 8 secciones, 12 parámetros con historial, perfil, ciclo menstrual, análisis mensuales | Subida · **PR por crear** |

Detalle: `docs/AUDITORIA-FASE-1.md`, `docs/FASE-4.md`, `docs/FASE-2.md`, contratos `docs/API.md` y `docs/WEBSOCKET.md`.

## Pruebas automáticas

- Go: `go test ./...` → **35 pruebas** (motor, BD, migraciones, respaldos, TLS, seguridad, emparejamiento, WebSocket).
- App: `for t in tools/tests/*.test.mjs; do node "$t"; done` → **36 pruebas** (salud mental, mediciones, ciclo).
- CI (`.github/workflows/eva-go.yml`) corre ambas en cada push.

## Cómo usarlo

1. `EVA-Station.bat` (o `dist/eva-station.exe`). Consola: http://localhost:8080 (en la propia PC).
2. Teléfono en la misma red: `http://<IP de la laptop>:8080/app/` (la IP aparece en la ventana negra).
3. Emparejar una vez: consola **Simulator → Pair a phone** → código de 6 dígitos → en el teléfono **≡ → Station link → Pairing**.
4. Consola desde otro dispositivo: usar el **Operator link** de la ventana negra.

## Qué falta (por fase del plan)

- **Fase 2 (resto):** Bluetooth completo (lista de dispositivos, batería, reconexión), procedencia por métrica en lo enviado, variación de peso y ejercicio, abrir sin red (service worker), unificar clasificadores.
- **Fase 3 · EVA Station:** pantallas para usuarios, respaldos, auditoría, crear misión/asignar tripulación y expedientes (la API ya existe); estado de BD y sensores en el dashboard; IPV configurable; alarma por nivel; reportes de alertas, telemetría, radiación e intervenciones.
- **Fase 5 · Simulador:** selector DEMO/SIMULATED/REAL, 12 sensores, escenarios faltantes (presión arterial, fatiga, duplicados, caída de red), 3 astronautas a la vez.
- **Fase 6:** clase «pérdida de comunicación» en incidentes; perímetro sin GPS (ToF) con simulador claramente ficticio y documento de hardware.
- **Fase 7:** demo de 22 pasos automatizada, pruebas de carga, documentación final, informe en 3 categorías.
- **Decisiones del equipo:** ¿la preparación diaria envía horas exactas de sueño/hidratación o solo códigos? Certificado HTTPS de confianza (CA propia). Cifrado del disco de la laptop.

## Notas para quien continúe

- Codex: un solo trabajo a la vez (los trabajos en paralelo se cortan); revisar `git status` después de cada trabajo.
- Windows + Git Bash: evitar barras invertidas en comandos de una línea (usar `/`), y crear archivos largos con PowerShell o el editor.
