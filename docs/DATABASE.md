# EVA · Databases

Two SQLite instances, as in the planning document: the station keeps only what the privacy filter lets out of the phones; the phone keeps everything about its wearer.

| Instance | Where | Schema |
|---|---|---|
| `data/eva-station.db` | Cabin PC (EVA Station) | [`internal/store/schema.sql`](../internal/store/schema.sql) — created automatically |
| `app_astronaut.db` | Each explorer's phone (future native app) | [`db/device_schema.sql`](../db/device_schema.sql) — the current web app keeps the same fields in browser storage |

## Planning document → station tables

| Document | Table | Notes |
|---|---|---|
| astronautas | `crew` | + `radiation_budget_msv` (600 mSv career limit), `radiation_accum_msv` |
| linea_base_personal | `baseline` | phase `ADAPTATION_14D` → `ROLLING_7D`, mean ± SD per vital |
| misiones | `missions` | PLANNED · ACTIVE · COMPLETED · ABORTED |
| telemetria_mision | `telemetry` | 1 Hz; `UNIQUE(crew_id, ts)` makes offline re-sends idempotent; `ipv` and `status` per reading |
| registro_diario (verdict only) | `readiness` | GREEN / YELLOW / RED + vulnerability + factor codes; the diary stays on the phone |
| alertas | `alerts` | `voice` ≤ 8 words, `protocol` steps, response, `escalated` (30 s) |
| incidentes_caja_negra | `incidents` | frozen window T−30 … T+15 min, classification, timeline, `sha256` seal |
| — | `oplog` | operator/system actions (instructions, acks, abort) |
| — | `blackbox_chain` | tamper-evident SHA-256 chain of every black-box event |
| — | `eva_summary` | post-EVA load (TRIMP, kcal, dose, recovery hours) |
| reportes_mensuales | `monthly_health` | blood panel, weight, exercise, radiation, mental index |

Views: `v_live_triage` and `v_monthly_clinical`. **Timestamps** are INTEGER Unix milliseconds (UTC): `datetime(ts/1000, 'unixepoch', 'localtime')`. Open the file with *DB Browser for SQLite* while the station is stopped, or export CSV from **Reports → Export data**.

## Schema versions and migrations

`PRAGMA user_version` holds the schema version; `schema_migrations (version, name, applied_at)` records every migration applied (code: `internal/store/migrate.go`).

| Version | Content |
|---|---|
| 1 | Baseline schema (`internal/store/schema.sql`): crew, baseline, missions, telemetry, readiness, alerts, incidents, oplog, blackbox_chain, eva_summary, monthly_health + views. Databases created before migrations existed (version 0) upgrade in place. |
| 2 | Access control, identity and sync: `roles`, `users`, `audit_log`, `devices`, `mission_crew`, `sensors`, `alert_responses`, `device_outbox`, `protocols`, `emergency_contacts`, and `telemetry.event_id` (unique `"<device id>:<sequence>"`). |

- Each migration runs once, inside a transaction together with its version bump.
- Before migrating an existing database the station writes a safety copy `data/backups/eva-pre-migration-v<N>-<UTC>.db`.
- Secrets are never stored: users and paired devices keep only the SHA-256 of their tokens.

### Version 2 tables

| Table | Purpose | Key constraints |
|---|---|---|
| `roles` | Role → permissions (`view, medical, respond, command, admin`). Seeded: `operator` (all), `surgeon` (view, medical, respond). | PK name |
| `users` | Console users (people). | `token_hash` UNIQUE; FK role → roles; `disabled_at` keeps the record for the audit |
| `audit_log` | Who did what, from where, with which result (HTTP status). | index on ts |
| `devices` | Paired phones bound to one crew member; `last_seen`, `last_seq` = sync state. | `token_hash` UNIQUE; FK crew; `revoked_at` |
| `mission_crew` | Crew assigned to a mission and its role for that mission. | PK (mission, crew); role CHECK |
| `sensors` | Registered measuring devices (standard GATT profile name, manual, suit bus or simulated). | CHECK on kind, interface, status, battery 0–100 |
| `alert_responses` | Every answer to an alert (the alert row keeps the latest). | FK alert; CHECK response, by_kind |
| `device_outbox` | Instructions, buddy relays and aborts kept until the phone confirms them (`msg-ack`). | index (crew, delivered_at); `expires_at` |
| `protocols` | Catalogue mirrored from the deterministic rules (`source = builtin`); rows edited by the medical team (`medical`) are never overwritten. | PK (code, level) |
| `emergency_contacts` | Crew profile contacts. | FK crew ON DELETE CASCADE |

### Backups and restore

`internal/store/backup.go`: `VACUUM INTO` snapshot → verified read-only (`PRAGMA integrity_check` + full black-box chain) → kept only if both pass. `Restore` refuses unverified files, moves the current database (and `-wal`/`-shm`) aside as `*.pre-restore-<UTC>`, then copies the backup in place. CLI: `eva-station -restore <file>` (station stopped).
