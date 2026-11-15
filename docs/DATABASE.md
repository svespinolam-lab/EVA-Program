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
