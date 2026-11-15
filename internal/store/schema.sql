-- =====================================================================
-- EVA Station · database schema (SQLite 3, WAL mode)
-- ---------------------------------------------------------------------
-- Runs on the cabin PC, with no internet. It only receives what the
-- privacy filter lets out of each phone: live suit telemetry, alerts,
-- responses and the daily readiness verdict. The private diary
-- (mood, notes, menstrual cycle…) stays on the phone: see
-- db/device_schema.sql.
--
-- Conventions
--   * Every timestamp is INTEGER Unix epoch milliseconds (UTC): faster to
--     compare and index than ISO text, and timezone-safe.
--     Readable form:  datetime(ts / 1000, 'unixepoch', 'localtime')
--   * Telemetry is append-only and idempotent: UNIQUE(crew_id, ts) lets a
--     phone re-send its offline queue without creating duplicates.
--   * Field names map 1:1 to the planning document (see docs/DATABASE.md).
-- =====================================================================

-- 1. Crew profile ------------------------------------------------------
CREATE TABLE IF NOT EXISTS crew (
  id                   TEXT PRIMARY KEY,                -- 'AST-001'
  name                 TEXT NOT NULL,
  initials             TEXT NOT NULL DEFAULT '',
  role                 TEXT NOT NULL CHECK (role IN ('EV1','EV2','EV3','IV')),
  specialty            TEXT NOT NULL DEFAULT '',
  sex                  TEXT NOT NULL CHECK (sex IN ('F','M')),
  age                  INTEGER NOT NULL,
  height_cm            REAL,
  weight_kg            REAL,
  blood_type           TEXT,
  conditions           TEXT,
  allergies            TEXT,
  medication           TEXT,
  radiation_budget_msv REAL NOT NULL DEFAULT 600.0,     -- career limit
  radiation_accum_msv  REAL NOT NULL DEFAULT 0.0,
  created_at           INTEGER NOT NULL
);

-- 2. Personal baseline (days 1–14 adaptation, then rolling 7 days) ----
CREATE TABLE IF NOT EXISTS baseline (
  crew_id      TEXT PRIMARY KEY REFERENCES crew(id) ON DELETE CASCADE,
  phase        TEXT NOT NULL DEFAULT 'ADAPTATION_14D' CHECK (phase IN ('ADAPTATION_14D','ROLLING_7D')),
  days         INTEGER NOT NULL DEFAULT 0,
  hr_rest_mean REAL NOT NULL, hr_rest_sd REAL NOT NULL DEFAULT 5,
  hrv_mean     REAL NOT NULL, hrv_sd     REAL NOT NULL DEFAULT 8,
  spo2_mean    REAL NOT NULL, spo2_sd    REAL NOT NULL DEFAULT 1,
  temp_mean    REAL NOT NULL, temp_sd    REAL NOT NULL DEFAULT 0.2,
  rr_mean      REAL NOT NULL DEFAULT 14, rr_sd REAL NOT NULL DEFAULT 2,
  bp_sys       REAL NOT NULL DEFAULT 118,
  bp_dia       REAL NOT NULL DEFAULT 76,
  sleep_h      REAL NOT NULL DEFAULT 7.5,
  updated_at   INTEGER NOT NULL
);

-- 3. Missions / extravehicular activities ------------------------------
CREATE TABLE IF NOT EXISTS missions (
  id          TEXT PRIMARY KEY,                       -- 'SELENE3-EVA02'
  name        TEXT NOT NULL,
  environment TEXT NOT NULL,                          -- Moon | Mars | Orbit
  site        TEXT NOT NULL DEFAULT '',
  planned_h   REAL NOT NULL DEFAULT 6,
  status      TEXT NOT NULL DEFAULT 'PLANNED' CHECK (status IN ('PLANNED','ACTIVE','COMPLETED','ABORTED')),
  started_at  INTEGER,
  ended_at    INTEGER,
  created_at  INTEGER NOT NULL
);

-- 4. Live suit telemetry (1 Hz per explorer) ---------------------------
CREATE TABLE IF NOT EXISTS telemetry (
  id         INTEGER PRIMARY KEY,
  mission_id TEXT REFERENCES missions(id),            -- NULL = outside an EVA (checks)
  crew_id    TEXT NOT NULL REFERENCES crew(id),
  ts         INTEGER NOT NULL,
  hr REAL, spo2 REAL, temp REAL, rr REAL, hrv REAL,
  rad_rate   REAL,                                    -- mSv/h
  effort     INTEGER CHECK (effort IN (0,1,2)),       -- rest | moderate | high
  suit_p REAL, o2_min REAL, co2 REAL, batt REAL,
  bp_sys REAL, bp_dia REAL,
  ipv        INTEGER NOT NULL DEFAULT 0,              -- surveillance priority 0–100
  status     TEXT NOT NULL DEFAULT 'STABLE' CHECK (status IN ('STABLE','VULNERABLE','RISK','EMERGENCY')),
  source     TEXT NOT NULL DEFAULT 'device' CHECK (source IN ('device','virtual','replay')),
  seq        INTEGER                                  -- device sequence (store-and-forward)
);
CREATE UNIQUE INDEX IF NOT EXISTS ux_telemetry_crew_ts ON telemetry (crew_id, ts);
CREATE INDEX IF NOT EXISTS ix_telemetry_mission ON telemetry (mission_id, crew_id, ts);

-- 5. Daily readiness (privacy-filtered output of the phone diary) ------
CREATE TABLE IF NOT EXISTS readiness (
  crew_id       TEXT NOT NULL REFERENCES crew(id) ON DELETE CASCADE,
  day           TEXT NOT NULL,                        -- 'YYYY-MM-DD'
  status        TEXT NOT NULL CHECK (status IN ('GREEN','YELLOW','RED')),
  vulnerability INTEGER NOT NULL DEFAULT 0,           -- 0–100
  summary       TEXT NOT NULL DEFAULT '',             -- e.g. 'With reservations: 3 nights < 6 h'
  factors       TEXT NOT NULL DEFAULT '[]',           -- JSON array of factor codes only
  sleep_h       REAL,
  hydration     INTEGER,                              -- urine colour 1–8
  fatigue_high  INTEGER NOT NULL DEFAULT 0,
  hr_rest       REAL,
  hrv_rest      REAL,
  received_at   INTEGER NOT NULL,
  PRIMARY KEY (crew_id, day)
) WITHOUT ROWID;

-- 6. Alerts, responses and 30-second escalation ------------------------
CREATE TABLE IF NOT EXISTS alerts (
  id           INTEGER PRIMARY KEY,
  mission_id   TEXT REFERENCES missions(id),
  crew_id      TEXT NOT NULL REFERENCES crew(id),
  ts           INTEGER NOT NULL,
  level        TEXT NOT NULL CHECK (level IN ('ADVISORY','RISK','EMERGENCY')),
  code         TEXT NOT NULL,                         -- e.g. 'SPO2_CRIT'
  metric       TEXT,
  voice        TEXT NOT NULL,                         -- ≤ 8 words
  reason       TEXT NOT NULL,                         -- contextual diagnosis
  protocol     TEXT NOT NULL DEFAULT '[]',            -- JSON array of steps
  response     TEXT NOT NULL DEFAULT 'PENDING' CHECK (response IN ('PENDING','COPIED','IM_OK','NEED_HELP','NO_RESPONSE')),
  responded_by TEXT,                                  -- crew | operator
  responded_at INTEGER,
  response_s   REAL,
  escalated    INTEGER NOT NULL DEFAULT 0,
  escalated_at INTEGER,
  resolved_at  INTEGER
);
CREATE INDEX IF NOT EXISTS ix_alerts_mission ON alerts (mission_id, ts);
CREATE INDEX IF NOT EXISTS ix_alerts_crew_open ON alerts (crew_id, resolved_at);

-- 7. Black box incidents (frozen window T−30 min … T+15 min) ------------
CREATE TABLE IF NOT EXISTS incidents (
  id                 INTEGER PRIMARY KEY,
  mission_id         TEXT REFERENCES missions(id),
  crew_id            TEXT NOT NULL REFERENCES crew(id),
  alert_id           INTEGER REFERENCES alerts(id),
  trigger            TEXT NOT NULL CHECK (trigger IN ('EMERGENCY','ABORT','MANUAL')),
  event_ts           INTEGER NOT NULL,
  window_start       INTEGER NOT NULL,
  window_end         INTEGER NOT NULL,
  status             TEXT NOT NULL DEFAULT 'RECORDING' CHECK (status IN ('RECORDING','SEALED')),
  classification     TEXT CHECK (classification IN ('SUDDEN','PROGRESSIVE','SENSOR_FAULT')),
  first_deviation_ts INTEGER,
  detection_s        REAL,
  response_s         REAL,
  context_7d         TEXT,                            -- JSON (readiness of the 7 previous days)
  samples            TEXT,                            -- JSON (frozen 1 Hz series, column arrays)
  timeline           TEXT,                            -- JSON (first deviation, alert, response, actions)
  samples_count      INTEGER NOT NULL DEFAULT 0,
  sha256             TEXT,
  sealed_at          INTEGER
);
CREATE INDEX IF NOT EXISTS ix_incidents_crew ON incidents (crew_id, event_ts);

-- 8. Operator and system log (the "cockpit voice recorder" side) ------
CREATE TABLE IF NOT EXISTS oplog (
  id         INTEGER PRIMARY KEY,
  mission_id TEXT,
  ts         INTEGER NOT NULL,
  actor      TEXT NOT NULL,                           -- operator | crew:AST-001 | system
  crew_id    TEXT,
  action     TEXT NOT NULL,
  detail     TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS ix_oplog_ts ON oplog (ts);

-- 9. Tamper-evident hash chain of every black-box event ----------------
--    hash = SHA-256(prev_hash | ts | kind | crew_id | ref | payload)
CREATE TABLE IF NOT EXISTS blackbox_chain (
  id        INTEGER PRIMARY KEY,
  ts        INTEGER NOT NULL,
  kind      TEXT NOT NULL,
  crew_id   TEXT,
  ref       TEXT,
  payload   TEXT NOT NULL,
  prev_hash TEXT NOT NULL,
  hash      TEXT NOT NULL
);

-- 10. Post-EVA summary per astronaut (feeds next-day recovery) ---------
CREATE TABLE IF NOT EXISTS eva_summary (
  mission_id TEXT NOT NULL REFERENCES missions(id),
  crew_id    TEXT NOT NULL REFERENCES crew(id),
  duration_s INTEGER, samples INTEGER,
  hr_mean REAL, hr_max REAL, spo2_min REAL, temp_max REAL,
  trimp      REAL,                                    -- Banister training impulse
  kcal       REAL,                                    -- Keytel HR-based estimate
  dose_msv   REAL,
  peak_ipv   INTEGER, alerts INTEGER, risk_s INTEGER,
  recovery_h REAL,
  ended_at   INTEGER,
  PRIMARY KEY (mission_id, crew_id)
) WITHOUT ROWID;

-- 11. Monthly clinical panel (blood work, weight, exercise…) ----------
CREATE TABLE IF NOT EXISTS monthly_health (
  crew_id              TEXT NOT NULL REFERENCES crew(id) ON DELETE CASCADE,
  month                TEXT NOT NULL,                 -- 'YYYY-MM'
  avg_hr REAL, avg_spo2 REAL, avg_sleep_h REAL, avg_hrv REAL,
  hemoglobin           REAL,                          -- g/dL
  wbc                  REAL,                          -- /µL
  platelets            REAL,                          -- /µL
  glucose_fasting      REAL,                          -- mg/dL
  orthostatic_drop_sys REAL,                          -- mmHg (alert ≥ 20)
  weight_kg            REAL,
  weight_change_pct    REAL,                          -- 30 days (alert ≤ −5 %)
  exercise_pct         REAL,                          -- resistance-exercise compliance
  radiation_month_msv  REAL,
  mental_index         INTEGER,
  PRIMARY KEY (crew_id, month)
) WITHOUT ROWID;

-- Views ----------------------------------------------------------------
-- Latest reading of each astronaut (the live triage itself is computed in
-- memory by the engine; this view is for SQL inspection and exports).
CREATE VIEW IF NOT EXISTS v_live_triage AS
SELECT c.id AS crew_id, c.name, c.role, t.mission_id,
       datetime(t.ts / 1000, 'unixepoch', 'localtime') AS last_reading,
       t.hr, t.spo2, t.temp, t.rr, t.hrv, t.effort, t.ipv, t.status
FROM crew c
JOIN telemetry t ON t.id = (SELECT t2.id FROM telemetry t2 WHERE t2.crew_id = c.id ORDER BY t2.ts DESC LIMIT 1)
ORDER BY t.ipv DESC;

-- Automatic monthly clinical audit.
CREATE VIEW IF NOT EXISTS v_monthly_clinical AS
SELECT c.id AS crew_id, c.name, c.sex, m.month,
       m.avg_hr, m.avg_spo2, m.avg_sleep_h, m.hemoglobin,
       CASE WHEN (c.sex = 'F' AND m.hemoglobin < 12.0) OR (c.sex = 'M' AND m.hemoglobin < 13.5)
            THEN 'ANEMIA_ALERT' ELSE 'NORMAL' END AS hemoglobin_status,
       m.weight_change_pct,
       CASE WHEN m.weight_change_pct <= -5.0 THEN 'MASS_LOSS_RISK'
            WHEN m.weight_change_pct <= -2.0 THEN 'OBSERVE'
            ELSE 'STABLE' END AS weight_status,
       ROUND(c.radiation_accum_msv / c.radiation_budget_msv * 100.0, 1) AS radiation_career_pct
FROM crew c
JOIN monthly_health m ON m.crew_id = c.id;
