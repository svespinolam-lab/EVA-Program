package server

// Phase-4 API: session, server status, sync, missions with crew, alert
// history, protocols, crew records, vitals, contacts, sensors, users, paired
// phones, backups and the security audit. Permissions are declared in
// Handler (server.go); see docs/API.md for the contract.

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"eva/internal/model"
	"eva/internal/store"
)

func (s *Server) dbPath() string     { return filepath.Join(s.DataDir, "eva-station.db") }
func (s *Server) backupDir() string  { return filepath.Join(s.DataDir, "backups") }
func pathID(r *http.Request) string  { return strings.TrimSpace(r.PathValue("id")) }
func notFound(w http.ResponseWriter) { writeJSON(w, 404, map[string]any{"error": "not found"}) }

/* ---------- session and status ---------- */

func (s *Server) apiMe(w http.ResponseWriter, r *http.Request) {
	p := who(r)
	writeJSON(w, 200, map[string]any{"actor": p.Actor, "name": p.Name, "role": p.Role, "local": p.Local, "permissions": p.Permissions()})
}

func (s *Server) apiStatus(w http.ResponseWriter, r *http.Request) {
	consoles, phones := s.Hub.Counts()
	var dbBytes int64
	if fi, err := os.Stat(s.dbPath()); err == nil {
		dbBytes = fi.Size()
	}
	var last any
	if bs, err := store.ListBackups(s.backupDir()); err == nil && len(bs) > 0 {
		last = bs[0]
	}
	pairing := "code"
	if s.OpenPairing {
		pairing = "open (demo)"
	}
	var https any
	if s.HTTPSPort > 0 {
		https = map[string]any{"port": s.HTTPSPort, "fingerprint": s.TLSFingerprint}
	}
	writeJSON(w, 200, map[string]any{
		"version": Version, "uptimeS": int64(time.Since(s.started).Seconds()),
		"schema": s.St.SchemaVersion(), "dbBytes": dbBytes, "consoles": consoles, "phones": phones,
		"lastBackup": last, "pairing": pairing, "https": https, "serverTime": store.Now(),
	})
}

// apiSync: link and store-and-forward state of every crew member's phone.
func (s *Server) apiSync(w http.ResponseWriter, r *http.Request) {
	crew, err := s.St.ListCrew()
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	devs, _ := s.St.Devices()
	pending, _ := s.St.OutboxPendingCount()
	out := []map[string]any{}
	for _, c := range crew {
		row := map[string]any{"crewId": c.ID, "role": c.Role, "online": s.Hub.Online(c.ID), "outboxPending": pending[c.ID]}
		for _, d := range devs { // newest first: the current phone
			if d.CrewID == c.ID && !d.Revoked {
				row["device"] = d
				break
			}
		}
		out = append(out, row)
	}
	writeJSON(w, 200, out)
}

/* ---------- missions ---------- */

func (s *Server) apiMissions(w http.ResponseWriter, r *http.Request) {
	ms, err := s.St.Missions(100)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	if ms == nil {
		ms = []model.Mission{}
	}
	writeJSON(w, 200, ms)
}

type missionBody struct {
	model.Mission
	Crew []store.Assignment `json:"crew"`
}

func (s *Server) apiCreateMission(w http.ResponseWriter, r *http.Request) {
	var b missionBody
	if err := readJSON(r, &b); err != nil {
		writeErr(w, 400, err)
		return
	}
	m, err := s.Eng.CreateMission(b.Mission, b.Crew)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 201, map[string]any{"mission": m, "crew": b.Crew})
}

func (s *Server) apiMissionCrew(w http.ResponseWriter, r *http.Request) {
	as, err := s.St.MissionCrew(pathID(r))
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, as)
}

func (s *Server) apiSetMissionCrew(w http.ResponseWriter, r *http.Request) {
	m, err := s.St.Mission(pathID(r))
	if err != nil {
		notFound(w)
		return
	}
	if m.Status != "PLANNED" {
		writeErr(w, 409, errors.New("the crew can only change while the EVA is planned"))
		return
	}
	var as []store.Assignment
	if err := readJSON(r, &as); err != nil {
		writeErr(w, 400, err)
		return
	}
	for _, a := range as {
		if !s.Eng.Known(a.CrewID) {
			writeErr(w, 400, errors.New("unknown crew member "+a.CrewID))
			return
		}
	}
	if err := s.St.SetMissionCrew(m.ID, as); err != nil {
		writeErr(w, 400, err)
		return
	}
	s.St.Chain("MISSION_CREW", "", m.ID, map[string]any{"crew": as})
	writeJSON(w, 200, as)
}

/* ---------- alerts and protocols ---------- */

func (s *Server) apiAlerts(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	as, err := s.St.Alerts(q.Get("mission"), q.Get("open") == "1", limit)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	if as == nil {
		as = []model.Alert{}
	}
	writeJSON(w, 200, as)
}

func (s *Server) apiAlertResponses(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(pathID(r), 10, 64)
	if err != nil {
		writeErr(w, 400, errors.New("bad alert id"))
		return
	}
	rs, err := s.St.AlertResponses(id)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, rs)
}

func (s *Server) apiProtocols(w http.ResponseWriter, r *http.Request) {
	ps, err := s.St.Protocols()
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, ps)
}

/* ---------- crew records ---------- */

func (s *Server) apiCrewList(w http.ResponseWriter, r *http.Request) {
	crew, err := s.St.ListCrew()
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	out := []map[string]any{}
	for _, c := range crew {
		cs, _ := s.St.Contacts(c.ID)
		out = append(out, map[string]any{"crew": c, "contacts": cs})
	}
	writeJSON(w, 200, out)
}

func (s *Server) apiCreateCrew(w http.ResponseWriter, r *http.Request) {
	var c model.Crew
	if err := readJSON(r, &c); err != nil {
		writeErr(w, 400, err)
		return
	}
	if s.Eng.Known(strings.ToUpper(strings.TrimSpace(c.ID))) {
		writeErr(w, 409, errors.New("crew id already exists"))
		return
	}
	saved, err := s.Eng.UpsertCrew(c)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 201, saved)
}

func (s *Server) apiUpdateCrew(w http.ResponseWriter, r *http.Request) {
	id := strings.ToUpper(pathID(r))
	if !s.Eng.Known(id) {
		notFound(w)
		return
	}
	var c model.Crew
	if err := readJSON(r, &c); err != nil {
		writeErr(w, 400, err)
		return
	}
	c.ID = id
	saved, err := s.Eng.UpsertCrew(c)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 200, saved)
}

// apiVitals: stored telemetry of one astronaut (default: the last hour; at most 24 h).
func (s *Server) apiVitals(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	if !s.Eng.Known(id) {
		notFound(w)
		return
	}
	q := r.URL.Query()
	to, _ := strconv.ParseInt(q.Get("to"), 10, 64)
	from, _ := strconv.ParseInt(q.Get("from"), 10, 64)
	if to <= 0 {
		to = store.Now()
	}
	if from <= 0 || from >= to {
		from = to - 3600*1000
	}
	if to-from > 24*3600*1000 {
		from = to - 24*3600*1000
	}
	s.St.Flush()
	rows, err := s.St.Window(id, from, to)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	if rows == nil {
		rows = []store.TelRow{}
	}
	writeJSON(w, 200, map[string]any{"crewId": id, "from": from, "to": to, "rows": rows})
}

func (s *Server) apiContacts(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	if !s.Eng.Known(id) {
		notFound(w)
		return
	}
	cs, err := s.St.Contacts(id)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, cs)
}

func (s *Server) apiSetContacts(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	if !s.Eng.Known(id) {
		notFound(w)
		return
	}
	var cs []store.Contact
	if err := readJSON(r, &cs); err != nil {
		writeErr(w, 400, err)
		return
	}
	if err := s.St.SetContacts(id, cs); err != nil {
		writeErr(w, 400, err)
		return
	}
	out, _ := s.St.Contacts(id)
	writeJSON(w, 200, out)
}

/* ---------- sensors ---------- */

func (s *Server) apiSensors(w http.ResponseWriter, r *http.Request) {
	xs, err := s.St.Sensors(r.URL.Query().Get("crew"))
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, xs)
}

func (s *Server) apiAddSensor(w http.ResponseWriter, r *http.Request) {
	var x store.Sensor
	if err := readJSON(r, &x); err != nil {
		writeErr(w, 400, err)
		return
	}
	if !s.Eng.Known(x.CrewID) {
		writeErr(w, 400, errors.New("unknown crew member"))
		return
	}
	saved, err := s.St.AddSensor(x)
	if err != nil {
		writeErr(w, 400, errors.New("invalid sensor: kind, interface or status not allowed"))
		return
	}
	writeJSON(w, 201, saved)
}

func (s *Server) apiUpdateSensor(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Status  string   `json:"status"`
		Battery *float64 `json:"batteryPct"`
	}
	if err := readJSON(r, &b); err != nil {
		writeErr(w, 400, err)
		return
	}
	switch err := s.St.UpdateSensor(pathID(r), b.Status, b.Battery); {
	case errors.Is(err, store.ErrNotFound):
		notFound(w)
	case err != nil:
		writeErr(w, 400, errors.New("invalid status or battery"))
	default:
		writeJSON(w, 200, map[string]any{"ok": true})
	}
}

/* ---------- users ---------- */

func (s *Server) apiUsers(w http.ResponseWriter, r *http.Request) {
	us, err := s.St.Users()
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, us)
}

func (s *Server) apiCreateUser(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Name string `json:"name"`
		Role string `json:"role"`
	}
	if err := readJSON(r, &b); err != nil {
		writeErr(w, 400, err)
		return
	}
	u, token, err := s.St.CreateUser(b.Name, b.Role, who(r).Actor)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	s.St.Chain("USER_CREATED", "", u.ID, map[string]any{"name": u.Name, "role": u.Role, "by": who(r).Actor})
	// The token is shown only now; the station keeps its SHA-256.
	writeJSON(w, 201, map[string]any{"user": u, "token": token, "link": "/?token=" + token})
}

func (s *Server) apiDisableUser(w http.ResponseWriter, r *http.Request) {
	if err := s.St.DisableUser(pathID(r)); err != nil {
		notFound(w)
		return
	}
	s.St.Chain("USER_DISABLED", "", pathID(r), map[string]any{"by": who(r).Actor})
	writeJSON(w, 200, map[string]any{"ok": true})
}

/* ---------- paired phones ---------- */

func (s *Server) apiDevices(w http.ResponseWriter, r *http.Request) {
	ds, err := s.St.Devices()
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, ds)
}

// apiPair issues a one-time code (5 min) to pair a phone with a crew member.
func (s *Server) apiPair(w http.ResponseWriter, r *http.Request) {
	var b struct {
		CrewID string `json:"crewId"`
	}
	if err := readJSON(r, &b); err != nil || !s.Eng.Known(b.CrewID) {
		writeErr(w, 400, errors.New("unknown crew member"))
		return
	}
	code, exp := s.pairing.issue(b.CrewID, who(r).Actor)
	writeJSON(w, 201, map[string]any{"code": code, "crewId": b.CrewID, "expiresAt": exp})
}

func (s *Server) apiRevokeDevice(w http.ResponseWriter, r *http.Request) {
	crew, err := s.St.RevokeDevice(pathID(r))
	if err != nil {
		notFound(w)
		return
	}
	s.Hub.CloseDevice(pathID(r))
	s.St.Chain("DEVICE_REVOKED", crew, pathID(r), map[string]any{"by": who(r).Actor})
	s.Hub.Console(map[string]any{"type": "devices"})
	writeJSON(w, 200, map[string]any{"ok": true, "crewId": crew})
}

/* ---------- backups and audit ---------- */

func (s *Server) apiBackups(w http.ResponseWriter, r *http.Request) {
	bs, err := store.ListBackups(s.backupDir())
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	if bs == nil {
		bs = []store.BackupInfo{}
	}
	writeJSON(w, 200, bs)
}

func (s *Server) apiCreateBackup(w http.ResponseWriter, r *http.Request) {
	info, err := s.St.Backup(s.backupDir())
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	s.St.Chain("BACKUP", "", info.Name, map[string]any{"bytes": info.Bytes, "entries": info.Entries, "by": who(r).Actor})
	writeJSON(w, 201, info)
}

func (s *Server) apiAudit(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	es, err := s.St.AuditLog(limit)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, es)
}
