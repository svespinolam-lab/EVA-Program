-- =====================================================================
-- EVA · phone database (app_astronaut.db) — reference for a native app
-- ---------------------------------------------------------------------
-- Keeps 100 % of the wearer's physical and psychological data. Only the
-- readiness verdict, live suit telemetry, alerts and responses are sent
-- to EVA Station. daily_log and menstrual_cycle NEVER leave the phone.
-- Encrypt this file at rest (SQLCipher) in the native app.
-- The current web app stores the same fields in browser storage.
-- Timestamps: INTEGER Unix milliseconds (UTC). Days: 'YYYY-MM-DD'.
-- =====================================================================
PRAGMA foreign_keys = ON;
PRAGMA journal_mode = WAL;

CREATE TABLE IF NOT EXISTS local_profile (
  crew_id TEXT PRIMARY KEY,                 -- 'AST-001' (same id as the station)
  name TEXT NOT NULL, role TEXT NOT NULL, sex TEXT CHECK (sex IN ('F','M')), age INTEGER,
  cycle_tracking INTEGER NOT NULL DEFAULT 0, -- 1 = silent luteal-phase calibration enabled
  station_host TEXT                          -- e.g. '192.168.137.1:8080'
);

CREATE TABLE IF NOT EXISTS baseline (
  crew_id TEXT PRIMARY KEY REFERENCES local_profile(crew_id),
  phase TEXT NOT NULL DEFAULT 'ADAPTATION_14D',
  hr_rest REAL, hr_rest_sd REAL, hrv REAL, hrv_sd REAL, spo2 REAL, temp REAL, rr REAL,
  bp_sys REAL, bp_dia REAL, sleep_h REAL, updated_at INTEGER
);

-- PRIVATE: never synchronised
CREATE TABLE IF NOT EXISTS daily_log (
  crew_id TEXT NOT NULL, day TEXT NOT NULL,
  sleep_h REAL, rested TEXT CHECK (rested IN ('YES','SOMEWHAT','NO')),
  hydration INTEGER CHECK (hydration BETWEEN 1 AND 8),   -- urine-colour scale
  back_pain INTEGER CHECK (back_pain BETWEEN 0 AND 10), exercise_min INTEGER, blurred_vision INTEGER,
  mood INTEGER CHECK (mood BETWEEN 1 AND 5), fatigue INTEGER CHECK (fatigue BETWEEN 1 AND 5),
  gad2 INTEGER CHECK (gad2 BETWEEN 0 AND 6), phq2 INTEGER CHECK (phq2 BETWEEN 0 AND 6),
  attention INTEGER, team_cohesion INTEGER, loneliness INTEGER, motivation INTEGER,
  self_harm_risk INTEGER NOT NULL DEFAULT 0, notes TEXT,
  -- privacy-filtered result: the ONLY part sent to the station
  readiness TEXT CHECK (readiness IN ('GREEN','YELLOW','RED')), vulnerability INTEGER, safe_summary TEXT, factors TEXT,
  PRIMARY KEY (crew_id, day)
);

-- PRIVATE: never synchronised (only anonymous calibration offsets travel)
CREATE TABLE IF NOT EXISTS menstrual_cycle (
  crew_id TEXT NOT NULL, day TEXT NOT NULL, cycle_day INTEGER,
  phase TEXT CHECK (phase IN ('MENSTRUAL','FOLLICULAR','OVULATION','LUTEAL','HORMONAL_SUPPRESSION')),
  temp_offset_c REAL DEFAULT 0, hr_offset_bpm REAL DEFAULT 0, symptoms TEXT,
  PRIMARY KEY (crew_id, day)
);

CREATE TABLE IF NOT EXISTS countermeasures (
  id INTEGER PRIMARY KEY, crew_id TEXT NOT NULL, created_at INTEGER NOT NULL,
  trigger TEXT NOT NULL, action TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE','DONE','POSTPONED','DISMISSED')),
  green_days INTEGER NOT NULL DEFAULT 0      -- closes itself after 3 green days
);

-- Local "black box" of the wearer + store-and-forward queue
CREATE TABLE IF NOT EXISTS local_telemetry (
  seq INTEGER PRIMARY KEY,                  -- monotonic: acked by the station (ack-seq)
  ts INTEGER NOT NULL, mission_id TEXT,
  hr REAL, spo2 REAL, temp REAL, rr REAL, hrv REAL, rad REAL, effort INTEGER,
  suit_p REAL, o2_min REAL, co2 REAL, batt REAL, bp_sys REAL, bp_dia REAL,
  level INTEGER, synced INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS ix_local_telemetry_pending ON local_telemetry (synced, seq);

CREATE TABLE IF NOT EXISTS local_alerts (
  id INTEGER PRIMARY KEY, ts INTEGER NOT NULL, station_alert_id INTEGER,
  level INTEGER NOT NULL, metric TEXT, message TEXT NOT NULL, voice TEXT,
  response TEXT CHECK (response IN ('PENDING','COPIED','IM_OK','NEED_HELP','NO_RESPONSE')), responded_at INTEGER
);
