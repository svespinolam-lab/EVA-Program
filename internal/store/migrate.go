package store

// Versioned schema migrations.
//
// PRAGMA user_version holds the schema version of a database file. Migration
// 1 is the original schema (schema.sql, idempotent CREATE ... IF NOT EXISTS),
// so databases created before migrations existed (user_version 0) upgrade in
// place without losing data. Every later migration runs exactly once, inside
// a transaction together with its version bump, and is recorded in
// schema_migrations (version, name, applied_at).

import (
	"database/sql"
	"fmt"
)

type migration struct {
	version int
	name    string
	sql     string
}

// LatestSchema is the schema version this build creates and expects.
var LatestSchema = migrations[len(migrations)-1].version

var migrations = []migration{
	{1, "baseline schema (crew, missions, telemetry, alerts, incidents, black box, reports)", schemaSQL},
	{2, "access control, devices, audit, mission crew, sensors, alert responses, phone outbox, protocols, contacts, event ids", v2SQL},
}

// migrate brings db to LatestSchema and reports the versions before and after.
func migrate(db *sql.DB) (from, to int, err error) {
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&from); err != nil {
		return 0, 0, err
	}
	to = from
	for _, m := range migrations {
		if m.version <= to {
			continue
		}
		tx, err := db.Begin()
		if err != nil {
			return from, to, err
		}
		steps := []string{
			m.sql,
			`CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at INTEGER NOT NULL)`,
		}
		for _, q := range steps {
			if _, err := tx.Exec(q); err != nil {
				_ = tx.Rollback()
				return from, to, fmt.Errorf("migration %d (%s): %w", m.version, m.name, err)
			}
		}
		if _, err := tx.Exec(`INSERT OR REPLACE INTO schema_migrations (version, name, applied_at) VALUES (?,?,?)`, m.version, m.name, Now()); err != nil {
			_ = tx.Rollback()
			return from, to, err
		}
		if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, m.version)); err != nil {
			_ = tx.Rollback()
			return from, to, err
		}
		if err := tx.Commit(); err != nil {
			return from, to, fmt.Errorf("migration %d: %w", m.version, err)
		}
		to = m.version
	}
	return from, to, nil
}

// SchemaVersion returns the PRAGMA user_version of the open database.
func (s *Store) SchemaVersion() int {
	var v int
	_ = s.R.QueryRow(`PRAGMA user_version`).Scan(&v)
	return v
}

// v2SQL: identity, access control, audit and sync (phase 4).
const v2SQL = `
-- Console users (people, not phones). Only the SHA-256 of each personal
-- token is stored. The station PC itself acts as the built-in operator.
CREATE TABLE IF NOT EXISTS roles (
  name        TEXT PRIMARY KEY,
  description TEXT NOT NULL,
  permissions TEXT NOT NULL            -- comma list: view, medical, respond, command, admin
);
-- Both roles see clinical data (the live console carries it); only the
-- operator controls the mission, the simulator, users, devices and backups.
INSERT OR IGNORE INTO roles (name, description, permissions) VALUES
  ('operator', 'Cabin operator: full mission control',                      'view,medical,respond,command,admin'),
  ('surgeon',  'Flight surgeon: medical records, reports, alert responses', 'view,medical,respond');

CREATE TABLE IF NOT EXISTS users (
  id          TEXT PRIMARY KEY,
  name        TEXT NOT NULL,
  role        TEXT NOT NULL REFERENCES roles(name),
  token_hash  TEXT NOT NULL UNIQUE,
  created_at  INTEGER NOT NULL,
  created_by  TEXT NOT NULL,
  disabled_at INTEGER
);

-- Security audit: who did what, from where, with which result.
CREATE TABLE IF NOT EXISTS audit_log (
  id      INTEGER PRIMARY KEY,
  ts      INTEGER NOT NULL,
  actor   TEXT NOT NULL,                -- user id, 'station' (this PC), 'device:<id>'
  role    TEXT NOT NULL,
  ip      TEXT,
  action  TEXT NOT NULL,                -- e.g. 'POST /api/mission/abort', 'PAIR', 'REVOKE'
  target  TEXT,
  result  INTEGER NOT NULL              -- HTTP status (or 1 = ok, 0 = refused)
);
CREATE INDEX IF NOT EXISTS ix_audit_ts ON audit_log (ts);

-- Paired phones. A phone proves who it is with a per-device token issued
-- after entering a one-time pairing code shown on the console.
CREATE TABLE IF NOT EXISTS devices (
  id          TEXT PRIMARY KEY,
  crew_id     TEXT NOT NULL REFERENCES crew(id),
  name        TEXT NOT NULL DEFAULT 'Phone',
  token_hash  TEXT NOT NULL UNIQUE,
  paired_at   INTEGER NOT NULL,
  paired_by   TEXT NOT NULL,
  last_seen   INTEGER,
  last_seq    INTEGER,                  -- highest telemetry sequence stored (sync state)
  revoked_at  INTEGER
);
CREATE INDEX IF NOT EXISTS ix_devices_crew ON devices (crew_id);

-- Crew assigned to each mission, with the role for that mission.
CREATE TABLE IF NOT EXISTS mission_crew (
  mission_id TEXT NOT NULL REFERENCES missions(id) ON DELETE CASCADE,
  crew_id    TEXT NOT NULL REFERENCES crew(id),
  role       TEXT NOT NULL CHECK (role GLOB 'EV[0-9]*' OR role = 'IV'),
  PRIMARY KEY (mission_id, crew_id)
);

-- Sensors registered per crew member. No invented protocols: 'profile' names
-- a standard GATT service or is NULL for manual / simulated sources.
CREATE TABLE IF NOT EXISTS sensors (
  id          TEXT PRIMARY KEY,
  crew_id     TEXT NOT NULL REFERENCES crew(id),
  kind        TEXT NOT NULL CHECK (kind IN ('hr','hrv','spo2','temp','rr','bp','glucose','scale','dosimeter','suit','other')),
  model       TEXT,
  interface   TEXT NOT NULL CHECK (interface IN ('ble-gatt','manual','suit-bus','simulated')),
  profile     TEXT,
  status      TEXT NOT NULL DEFAULT 'registered' CHECK (status IN ('registered','connected','disconnected','retired')),
  battery_pct REAL CHECK (battery_pct IS NULL OR (battery_pct >= 0 AND battery_pct <= 100)),
  last_seen   INTEGER,
  created_at  INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS ix_sensors_crew ON sensors (crew_id);

-- Every response to an alert (alerts keeps only the latest one).
CREATE TABLE IF NOT EXISTS alert_responses (
  id        INTEGER PRIMARY KEY,
  alert_id  INTEGER NOT NULL REFERENCES alerts(id),
  ts        INTEGER NOT NULL,
  response  TEXT NOT NULL CHECK (response IN ('COPIED','IM_OK','NEED_HELP')),
  by_kind   TEXT NOT NULL CHECK (by_kind IN ('crew','operator')),
  actor     TEXT
);
CREATE INDEX IF NOT EXISTS ix_alert_responses_alert ON alert_responses (alert_id);

-- Messages for a phone (instructions, buddy alerts, aborts), kept until the
-- phone confirms them, so nothing is lost while it is offline.
CREATE TABLE IF NOT EXISTS device_outbox (
  id           INTEGER PRIMARY KEY,
  crew_id      TEXT NOT NULL REFERENCES crew(id),
  type         TEXT NOT NULL,
  payload      TEXT NOT NULL,           -- JSON message as sent
  created_at   INTEGER NOT NULL,
  expires_at   INTEGER NOT NULL,
  delivered_at INTEGER
);
CREATE INDEX IF NOT EXISTS ix_outbox_pending ON device_outbox (crew_id, delivered_at);

-- Protocol catalogue (mirror of the deterministic rules; editable by the
-- medical team in a later phase: source = 'builtin' | 'medical').
CREATE TABLE IF NOT EXISTS protocols (
  code       TEXT NOT NULL,
  level      INTEGER NOT NULL CHECK (level BETWEEN 1 AND 3),
  steps      TEXT NOT NULL,             -- JSON array of steps
  source     TEXT NOT NULL DEFAULT 'builtin' CHECK (source IN ('builtin','medical')),
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (code, level)
);

-- Emergency contacts (crew profile).
CREATE TABLE IF NOT EXISTS emergency_contacts (
  id       INTEGER PRIMARY KEY,
  crew_id  TEXT NOT NULL REFERENCES crew(id) ON DELETE CASCADE,
  name     TEXT NOT NULL,
  relation TEXT,
  channel  TEXT
);

-- Unique event id per telemetry reading ('<device id>:<sequence>').
ALTER TABLE telemetry ADD COLUMN event_id TEXT;
CREATE UNIQUE INDEX IF NOT EXISTS ux_telemetry_event ON telemetry (event_id) WHERE event_id IS NOT NULL;
`
