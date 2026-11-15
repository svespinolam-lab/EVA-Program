package store

import (
	"path/filepath"
	"sync"
	"testing"

	"eva/internal/model"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "eva-test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.SeedIfEmpty(); err != nil {
		t.Fatal(err)
	}
	return st
}

// Store-and-forward: a phone that reconnects replays readings the station
// may already have. The (crew_id, ts) key keeps them exactly once, and the
// commit callback still acknowledges the highest device sequence.
func TestTelemetryReplayIsIdempotentAndAcknowledged(t *testing.T) {
	st := openTest(t)
	var mu sync.Mutex
	acked := map[string]int64{}
	st.OnCommit = func(m map[string]int64) {
		mu.Lock()
		defer mu.Unlock()
		for k, v := range m {
			if v > acked[k] {
				acked[k] = v
			}
		}
	}
	const ts = int64(1_800_000_000_000)
	for i := 0; i < 3; i++ { // original + two replays
		st.AddTelemetry(model.Sample{CrewID: "AST-001", T: ts, Seq: 7, HR: model.F(70)}, "", 10, "STABLE", "device")
	}
	st.AddTelemetry(model.Sample{CrewID: "AST-001", T: ts + 1000, Seq: 8, HR: model.F(71)}, "", 10, "STABLE", "device")
	st.Flush()

	var n int
	if err := st.R.QueryRow(`SELECT COUNT(*) FROM telemetry WHERE crew_id = 'AST-001' AND ts IN (?, ?)`, ts, ts+1000).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("stored %d rows for 2 distinct readings (4 sends), want 2", n)
	}
	mu.Lock()
	defer mu.Unlock()
	if acked["AST-001"] != 8 {
		t.Fatalf("ack seq = %d, want 8", acked["AST-001"])
	}
}

// The black-box chain must reveal any later edit of a stored event.
func TestBlackboxChainDetectsTampering(t *testing.T) {
	st := openTest(t)
	st.Chain("TEST_EVENT", "AST-001", "a", map[string]any{"spo2": 84})
	st.Chain("TEST_EVENT", "AST-001", "b", map[string]any{"spo2": 85})
	rep, err := st.VerifyChain()
	if err != nil || !rep.OK || rep.Entries < 2 {
		t.Fatalf("fresh chain: %+v, %v", rep, err)
	}
	if _, err := st.W.Exec(`UPDATE blackbox_chain SET payload = '{"spo2":98}' WHERE ref = 'a'`); err != nil {
		t.Fatal(err)
	}
	rep, err = st.VerifyChain()
	if err != nil {
		t.Fatal(err)
	}
	if rep.OK {
		t.Fatal("tampered chain still verifies")
	}
}

// Data survives closing and reopening the database (station restart).
func TestDataSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "eva-test.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SeedIfEmpty(); err != nil {
		t.Fatal(err)
	}
	st.AddTelemetry(model.Sample{CrewID: "AST-002", T: 1_800_000_000_000, HR: model.F(90)}, "", 20, "STABLE", "device")
	st.Chain("TEST_EVENT", "AST-002", "x", map[string]any{"ok": true})
	st.Flush()
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	st2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	if seeded, err := st2.SeedIfEmpty(); err != nil || seeded {
		t.Fatalf("reopen re-seeded (%v) or failed: %v", seeded, err)
	}
	var hr float64
	if err := st2.R.QueryRow(`SELECT hr FROM telemetry WHERE crew_id = 'AST-002' AND ts = 1800000000000`).Scan(&hr); err != nil || hr != 90 {
		t.Fatalf("telemetry after restart: hr=%v err=%v", hr, err)
	}
	if rep, err := st2.VerifyChain(); err != nil || !rep.OK {
		t.Fatalf("chain after restart: %+v, %v", rep, err)
	}
}
