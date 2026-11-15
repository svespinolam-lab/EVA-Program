package store

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

// A database created before versioned migrations (user_version 0, baseline
// schema, real data) upgrades in place: data kept, new tables present,
// version recorded, and reopening does not run anything again.
func TestOldDatabaseUpgradesWithoutLosingData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	old, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	if _, err := old.Exec(schemaSQL); err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(`INSERT INTO crew (id, name, initials, role, specialty, sex, age, created_at) VALUES ('AST-009','Old Record','OR','EV1','Geologist','F',40,1700000000000)`); err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(`INSERT INTO telemetry (crew_id, ts, hr) VALUES ('AST-009', 1700000000000, 71)`); err != nil {
		t.Fatal(err)
	}
	var v int
	_ = old.QueryRow(`PRAGMA user_version`).Scan(&v)
	if v != 0 {
		t.Fatalf("setup: user_version = %d, want 0", v)
	}
	old.Close()

	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.SchemaFrom != 0 || st.SchemaTo != LatestSchema || st.SchemaVersion() != LatestSchema {
		t.Fatalf("migrated %d -> %d (now %d), want 0 -> %d", st.SchemaFrom, st.SchemaTo, st.SchemaVersion(), LatestSchema)
	}
	if bs, err := ListBackups(filepath.Join(filepath.Dir(path), "backups")); err != nil || len(bs) != 1 || !strings.HasPrefix(bs[0].Name, "eva-pre-migration-v0-") {
		t.Fatalf("pre-migration safety copy missing: %+v %v", bs, err)
	}
	var name string
	var hr float64
	if err := st.R.QueryRow(`SELECT name FROM crew WHERE id = 'AST-009'`).Scan(&name); err != nil || name != "Old Record" {
		t.Fatalf("crew lost: %q %v", name, err)
	}
	if err := st.R.QueryRow(`SELECT hr FROM telemetry WHERE crew_id = 'AST-009'`).Scan(&hr); err != nil || hr != 71 {
		t.Fatalf("telemetry lost: %v %v", hr, err)
	}
	for _, table := range []string{"devices", "users", "roles", "audit_log", "mission_crew", "sensors", "alert_responses", "device_outbox", "protocols", "emergency_contacts", "schema_migrations"} {
		var n int
		if err := st.R.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&n); err != nil || n != 1 {
			t.Errorf("table %s missing after migration", table)
		}
	}
	var applied int
	_ = st.R.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&applied)
	if applied != len(migrations) {
		t.Errorf("schema_migrations has %d rows, want %d", applied, len(migrations))
	}
	// event_id is usable and unique
	if _, err := st.W.Exec(`INSERT INTO telemetry (crew_id, ts, hr, event_id) VALUES ('AST-009', 1700000001000, 72, 'dev_x:1')`); err != nil {
		t.Fatal(err)
	}
	if res, err := st.W.Exec(`INSERT OR IGNORE INTO telemetry (crew_id, ts, hr, event_id) VALUES ('AST-009', 1700000002000, 73, 'dev_x:1')`); err != nil {
		t.Fatal(err)
	} else if n, _ := res.RowsAffected(); n != 0 {
		t.Error("duplicate event_id stored twice")
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	st2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	if st2.SchemaFrom != LatestSchema || st2.SchemaTo != LatestSchema {
		t.Fatalf("reopen migrated again: %d -> %d", st2.SchemaFrom, st2.SchemaTo)
	}
}

// A fresh database is created directly at the latest version, with roles seeded.
func TestFreshDatabaseIsLatestWithRoles(t *testing.T) {
	st := openTest(t)
	if st.SchemaFrom != 0 || st.SchemaTo != LatestSchema {
		t.Fatalf("fresh db %d -> %d", st.SchemaFrom, st.SchemaTo)
	}
	var perms string
	if err := st.R.QueryRow(`SELECT permissions FROM roles WHERE name = 'surgeon'`).Scan(&perms); err != nil || perms != "view,medical,respond" {
		t.Fatalf("surgeon role = %q, %v", perms, err)
	}
}
