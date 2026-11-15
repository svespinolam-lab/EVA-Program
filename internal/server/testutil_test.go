package server

import (
	"path/filepath"
	"testing"
	"testing/fstest"

	"eva/internal/engine"
	"eva/internal/sim"
	"eva/internal/store"
)

const testToken = "test-operator-token-0123456789"

// newTestServer wires a full station (store + engine + simulator + hub) on a
// temporary database, exactly like cmd/eva-station does.
func newTestServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "eva-station.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.SeedIfEmpty(); err != nil {
		t.Fatal(err)
	}
	eng, err := engine.New(st)
	if err != nil {
		t.Fatal(err)
	}
	crew, _ := st.ListCrew()
	bases, _ := st.Baselines()
	sm := sim.New(eng, crew, bases)
	web := fstest.MapFS{
		"app/index.html":    {Data: []byte("<p>eva app</p>")},
		"pc/index.html":     {Data: []byte("<p>eva console</p>")},
		"shared/tokens.css": {Data: []byte(":root{}")},
	}
	srv := &Server{Eng: eng, St: st, Sim: sm, Hub: NewHub(), Web: web, OperatorToken: testToken, DataDir: dir}
	st.OnCommit = srv.OnCommit
	eng.SetEmitter(srv.Emitter())
	eng.SetVirtualSuits(sm)
	return srv
}

// pairedHello pairs a phone for crewID directly in the store (as the console
// would) and returns the hello message that phone sends.
func pairedHello(t *testing.T, srv *Server, crewID string) map[string]any {
	t.Helper()
	d, token, err := srv.St.CreateDevice(crewID, "test phone", "test")
	if err != nil {
		t.Fatal(err)
	}
	return map[string]any{"type": "hello", "crewId": crewID, "device": "test phone", "deviceId": d.ID, "token": token}
}
