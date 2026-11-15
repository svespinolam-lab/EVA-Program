package engine

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"eva/internal/model"
	"eva/internal/store"
)

// recorder captures what the engine sends to the console and to phones.
type recorder struct {
	mu      sync.Mutex
	console []any
	device  map[string][]any
	others  []map[string]any // DevicesExcept messages, with "except" added
}

func (r *recorder) Console(msg any) { r.mu.Lock(); r.console = append(r.console, msg); r.mu.Unlock() }
func (r *recorder) Device(crewID string, msg any) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.device == nil {
		r.device = map[string][]any{}
	}
	r.device[crewID] = append(r.device[crewID], msg)
	return true
}
func (r *recorder) DevicesExcept(crewID string, msg any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if m, ok := msg.(map[string]any); ok {
		c := map[string]any{"except": crewID}
		for k, v := range m {
			c[k] = v
		}
		r.others = append(r.others, c)
	}
}

// fakeClock drives store.Now so time-based rules run without sleeping.
func fakeClock(t *testing.T, start int64) *int64 {
	t.Helper()
	now := start
	prev := store.Now
	store.Now = func() int64 { return now }
	t.Cleanup(func() { store.Now = prev })
	return &now
}

func newTestEngine(t *testing.T) (*Engine, *store.Store, *recorder) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "eva-test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.SeedIfEmpty(); err != nil {
		t.Fatal(err)
	}
	e, err := New(st)
	if err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	e.SetEmitter(rec)
	return e, st, rec
}

// sample is an at-rest reading for Ana (AST-001, resting HR 62) with the
// given SpO₂; everything else is nominal.
func sample(t int64, spo2 float64) model.Sample {
	return model.Sample{CrewID: "AST-001", T: t, HR: model.F(70), SpO2: model.F(spo2), Temp: model.F(36.8), RR: model.F(15),
		HRV: model.F(55), Effort: model.I(0), SuitP: model.F(4.3), O2: model.F(360), CO2: model.F(2.1), Batt: model.F(80), Rad: model.F(0.05)}
}

func openFor(e *Engine, crew, metric string) *model.Alert {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, oa := range e.open {
		if oa.a.CrewID == crew && oa.a.Metric == metric {
			a := oa.a
			return &a
		}
	}
	return nil
}

func TestHypoxiaRaisesAlertEscalatesAt30sAndResolves(t *testing.T) {
	now := fakeClock(t, 1_800_000_000_000)
	e, st, rec := newTestEngine(t)
	if _, err := e.StartMission(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		*now += 1000
		e.Ingest(sample(*now, 84), "device")
	}
	a := openFor(e, "AST-001", "spo2")
	if a == nil {
		t.Fatal("SpO₂ 84 % for 6 s did not raise an alert")
	}
	if a.LevelNum() < 2 || a.Response != "PENDING" {
		t.Fatalf("alert = level %s response %s, want ≥ RISK and PENDING", a.Level, a.Response)
	}

	// 29 s after the alert: no escalation yet.
	*now = a.T + 29_000
	e.Ingest(sample(*now, 84), "device")
	e.Tick()
	if x := openFor(e, "AST-001", "spo2"); x == nil || x.Escalated {
		t.Fatalf("escalated before 30 s: %+v", x)
	}

	// 31 s: escalated, and the buddy's phones (everyone except AST-001) are told.
	*now = a.T + 31_000
	e.Ingest(sample(*now, 84), "device")
	e.Tick()
	if x := openFor(e, "AST-001", "spo2"); x == nil || !x.Escalated {
		t.Fatalf("not escalated after 31 s: %+v", x)
	}
	buddy := false
	rec.mu.Lock()
	for _, m := range rec.others {
		if m["type"] == "buddy" && m["crewId"] == "AST-001" && m["except"] == "AST-001" {
			buddy = true
		}
	}
	rec.mu.Unlock()
	if !buddy {
		t.Fatal("escalation did not notify the buddy's phone")
	}

	// The explorer answers "I'm OK".
	got, err := e.Ack(a.ID, "IM_OK", "crew")
	if err != nil || got.Response != "IM_OK" || got.RespondedBy != "crew" {
		t.Fatalf("ack = %+v, %v", got, err)
	}
	if rs, err := st.AlertResponses(a.ID); err != nil || len(rs) != 1 || rs[0].Response != "IM_OK" || rs[0].By != "crew" {
		t.Fatalf("response history = %+v, %v", rs, err)
	}

	// SpO₂ recovers: the alert resolves on its own (hysteresis: 5 normal readings).
	for i := 0; i < 10; i++ {
		*now += 1000
		e.Ingest(sample(*now, 98), "device")
	}
	if x := openFor(e, "AST-001", "spo2"); x != nil {
		t.Fatalf("alert still open after recovery: %+v", x)
	}
}

func TestPhonesNeverReceiveOtherCrewMedicalRecords(t *testing.T) {
	fakeClock(t, 1_800_000_000_000)
	e, _, _ := newTestEngine(t)

	b, _ := json.Marshal(e.Welcome("AST-002"))
	welcome := string(b)
	for _, private := range []string{"allergies", "conditions", "medication", "bloodType", "weightKg", "heightCm", "radAccum", "Penicillin"} {
		if strings.Contains(welcome, private) {
			t.Errorf("welcome leaks %q to a phone", private)
		}
	}
	if !strings.Contains(welcome, "AST-001") || !strings.Contains(welcome, "Ana Ruiz") {
		t.Error("welcome lost the public roster")
	}

	e.mu.Lock()
	dc := e.deviceCrew()
	e.mu.Unlock()
	b, _ = json.Marshal(dc)
	for _, private := range []string{`"ipv"`, `"diagnosis"`, `"status"`} {
		if strings.Contains(string(b), private) {
			t.Errorf("periodic crew broadcast leaks %s", private)
		}
	}
}

// The console must know whether a linked phone streams hardware or simulated
// readings, so simulated data is never presented as real.
func TestConsoleSeesReadingProvenance(t *testing.T) {
	now := fakeClock(t, 1_800_000_000_000)
	e, _, _ := newTestEngine(t)
	data := func() string {
		e.mu.Lock()
		defer e.mu.Unlock()
		for _, v := range e.crewViews(true) {
			if v.Crew.ID == "AST-001" {
				return v.Device.Data
			}
		}
		return "missing"
	}
	s := sample(*now, 98)
	s.Source = "virtual"
	e.Ingest(s, "device")
	if got := data(); got != "virtual" {
		t.Fatalf("simulated reading shows as %q, want virtual", got)
	}
	*now += 1000
	s = sample(*now, 98)
	s.Source = "device"
	e.Ingest(s, "device")
	if got := data(); got != "device" {
		t.Fatalf("hardware reading shows as %q, want device", got)
	}
}

// A phone can only answer alerts about its own crew member.
func TestPhoneCannotAcknowledgeAnotherCrewAlert(t *testing.T) {
	now := fakeClock(t, 1_800_000_000_000)
	e, _, _ := newTestEngine(t)
	if _, err := e.StartMission(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		*now += 1000
		e.Ingest(sample(*now, 84), "device")
	}
	a := openFor(e, "AST-001", "spo2")
	if a == nil {
		t.Fatal("no alert raised")
	}
	if _, err := e.AckByCrew(a.ID, "IM_OK", "AST-002", ""); err == nil {
		t.Fatal("AST-002 acknowledged an alert of AST-001")
	}
	if x := openFor(e, "AST-001", "spo2"); x == nil || x.Response != "PENDING" {
		t.Fatalf("foreign ack changed the alert: %+v", x)
	}
	if got, err := e.AckByCrew(a.ID, "COPIED", "AST-001", ""); err != nil || got.Response != "COPIED" {
		t.Fatalf("own ack = %+v, %v", got, err)
	}
}
