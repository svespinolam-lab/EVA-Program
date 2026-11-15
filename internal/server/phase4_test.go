package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"eva/internal/store"
)

func decode(t *testing.T, w *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(w.Body.Bytes(), v); err != nil {
		t.Fatalf("decode %q: %v", w.Body.String(), err)
	}
}

// createUser creates a console user from the station PC and returns its token and id.
func createUser(t *testing.T, h http.Handler, name, role string) (string, string) {
	t.Helper()
	w := call(h, "POST", "/api/users", local, nil, `{"name":"`+name+`","role":"`+role+`"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create user = %d %s", w.Code, w.Body.String())
	}
	var out struct {
		Token string `json:"token"`
		User  struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	decode(t, w, &out)
	return out.Token, out.User.ID
}

func TestRolePermissions(t *testing.T) {
	h := newTestServer(t).Handler()
	tok, id := createUser(t, h, "Dr. Vega", "surgeon")
	bearer := map[string]string{"Authorization": "Bearer " + tok}

	var me struct {
		Role        string   `json:"role"`
		Permissions []string `json:"permissions"`
	}
	decode(t, call(h, "GET", "/api/auth/me", phone, bearer, ""), &me)
	if me.Role != "surgeon" || strings.Join(me.Permissions, ",") != "view,medical,respond" {
		t.Fatalf("me = %+v", me)
	}
	cases := []struct {
		method, path, body string
		want               int
	}{
		{"GET", "/api/export/telemetry", "", 200},
		{"GET", "/api/crew", "", 200},
		{"POST", "/api/instruction", `{"crewId":"AST-001","text":"Report status"}`, 200},
		{"POST", "/api/mission/start", "{}", 403},
		{"POST", "/api/sim/inject", `{"crewId":"AST-002","scenario":"hypoxia"}`, 403},
		{"POST", "/api/users", `{"name":"x","role":"operator"}`, 403},
		{"POST", "/api/devices/pair", `{"crewId":"AST-001"}`, 403},
		{"GET", "/api/audit", "", 403},
	}
	for _, c := range cases {
		if w := call(h, c.method, c.path, phone, bearer, c.body); w.Code != c.want {
			t.Errorf("surgeon %s %s = %d, want %d (%s)", c.method, c.path, w.Code, c.want, w.Body.String())
		}
	}
	if w := call(h, "POST", "/api/users/"+id+"/disable", local, nil, ""); w.Code != 200 {
		t.Fatalf("disable = %d", w.Code)
	}
	if w := call(h, "GET", "/api/state", phone, bearer, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("disabled user = %d, want 401", w.Code)
	}
}

func TestAuditLogRecordsChangesClinicalReadsAndRefusals(t *testing.T) {
	h := newTestServer(t).Handler()
	tok, _ := createUser(t, h, "Dr. Vega", "surgeon")
	call(h, "GET", "/api/export/telemetry", local, nil, "")
	call(h, "POST", "/api/mission/abort", phone, map[string]string{"Authorization": "Bearer " + tok}, `{"reason":"x"}`)
	call(h, "GET", "/api/state", phone, nil, "")

	var log []store.AuditEntry
	decode(t, call(h, "GET", "/api/audit", local, nil, ""), &log)
	found := map[string]int{}
	for _, e := range log {
		found[e.Action+"|"+e.Role] = e.Result
	}
	if found["POST /api/users|operator"] != 201 {
		t.Errorf("user creation not audited: %v", found)
	}
	if found["GET /api/export/telemetry|operator"] != 200 {
		t.Errorf("clinical export not audited: %v", found)
	}
	if found["POST /api/mission/abort|surgeon"] != 403 {
		t.Errorf("refused abort not audited: %v", found)
	}
	if found["GET /api/state|-"] != 401 {
		t.Errorf("anonymous access not audited: %v", found)
	}
}

func TestPhonePairingLifecycle(t *testing.T) {
	srv := newTestServer(t)
	h := srv.Handler()
	ts := httptest.NewServer(h)
	defer ts.Close()

	// Unpaired phone: refused with an explicit code.
	ph := dialWS(t, ts.URL, "/ws/device")
	ph.send(t, map[string]any{"type": "hello", "crewId": "AST-001"})
	if m := ph.next(t, "error", 5*time.Second); m["code"] != "pairing_required" {
		t.Fatalf("unpaired hello = %v", m)
	}

	// The operator issues a code; the phone pairs with it and gets its token.
	var pc struct{ Code string }
	decode(t, call(h, "POST", "/api/devices/pair", local, nil, `{"crewId":"AST-001"}`), &pc)
	if len(pc.Code) != 6 {
		t.Fatalf("code = %q", pc.Code)
	}
	ph = dialWS(t, ts.URL, "/ws/device")
	ph.send(t, map[string]any{"type": "hello", "crewId": "AST-001", "device": "Ana phone", "pairCode": pc.Code})
	paired := ph.next(t, "paired", 5*time.Second)
	devID, _ := paired["deviceId"].(string)
	token, _ := paired["token"].(string)
	if devID == "" || token == "" || paired["crewId"] != "AST-001" {
		t.Fatalf("paired = %v", paired)
	}
	ph.next(t, "welcome", 5*time.Second)

	// The code decides the crew member even if the phone had another one selected.
	var pc2 struct{ Code string }
	decode(t, call(h, "POST", "/api/devices/pair", local, nil, `{"crewId":"AST-002"}`), &pc2)
	luis := dialWS(t, ts.URL, "/ws/device")
	luis.send(t, map[string]any{"type": "hello", "crewId": "AST-001", "device": "Luis phone", "pairCode": pc2.Code})
	if m := luis.next(t, "paired", 5*time.Second); m["crewId"] != "AST-002" {
		t.Fatalf("code for AST-002 paired as %v", m["crewId"])
	}

	// The code is single use.
	other := dialWS(t, ts.URL, "/ws/device")
	other.send(t, map[string]any{"type": "hello", "crewId": "AST-001", "pairCode": pc.Code})
	if m := other.next(t, "error", 5*time.Second); m["code"] != "pairing_failed" {
		t.Fatalf("reused code = %v", m)
	}
	// The token cannot be used for another crew member.
	other = dialWS(t, ts.URL, "/ws/device")
	other.send(t, map[string]any{"type": "hello", "crewId": "AST-002", "deviceId": devID, "token": token})
	if m := other.next(t, "error", 5*time.Second); m["code"] != "crew_mismatch" {
		t.Fatalf("crew mismatch = %v", m)
	}

	// Revoking closes the live link and the token stops working.
	if w := call(h, "POST", "/api/devices/"+devID+"/revoke", local, nil, ""); w.Code != 200 {
		t.Fatalf("revoke = %d", w.Code)
	}
	if m := ph.next(t, "error", 5*time.Second); m["code"] != "device_revoked" {
		t.Fatalf("revoked link = %v", m)
	}
	again := dialWS(t, ts.URL, "/ws/device")
	again.send(t, map[string]any{"type": "hello", "crewId": "AST-001", "deviceId": devID, "token": token})
	if m := again.next(t, "error", 5*time.Second); m["code"] != "device_unknown" {
		t.Fatalf("revoked token = %v", m)
	}
}

func TestInstructionWaitsForOfflinePhoneUntilAcknowledged(t *testing.T) {
	srv := newTestServer(t)
	h := srv.Handler()
	ts := httptest.NewServer(h)
	defer ts.Close()
	hello := pairedHello(t, srv, "AST-001")

	// Sent while the phone is offline.
	if w := call(h, "POST", "/api/instruction", local, nil, `{"crewId":"AST-001","text":"Return to airlock now."}`); w.Code != 200 {
		t.Fatalf("instruction = %d %s", w.Code, w.Body.String())
	}
	ph := dialWS(t, ts.URL, "/ws/device")
	ph.send(t, hello)
	in := ph.next(t, "instruction", 5*time.Second)
	if in["text"] != "Return to airlock now." || in["msgId"] == nil {
		t.Fatalf("replayed instruction = %v", in)
	}
	ph.send(t, map[string]any{"type": "msg-ack", "msgId": in["msgId"]})

	// Once acknowledged it is not pending any more (polled: the ack is async).
	deadline := time.Now().Add(5 * time.Second)
	for {
		pend, _ := srv.St.PendingOutbox("AST-001")
		if len(pend) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("acknowledged message still pending: %v", pend)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestPairedReadingsGetUniqueEventIDs(t *testing.T) {
	srv := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	hello := pairedHello(t, srv, "AST-001")
	ph := dialWS(t, ts.URL, "/ws/device")
	ph.send(t, hello)
	ph.next(t, "welcome", 5*time.Second)
	t0 := time.Now().UnixMilli() - 2000
	ph.send(t, map[string]any{"type": "telemetry", "seq": 5, "t": t0, "hr": 72, "spo2": 98})
	ph.next(t, "ack-seq", 5*time.Second)
	srv.St.Flush()
	var eid string
	if err := srv.St.R.QueryRow(`SELECT event_id FROM telemetry WHERE crew_id = 'AST-001' AND ts = ?`, t0).Scan(&eid); err != nil {
		t.Fatal(err)
	}
	if eid != hello["deviceId"].(string)+":5" {
		t.Fatalf("event_id = %q", eid)
	}
}

func TestMissionWithAssignedCrew(t *testing.T) {
	h := newTestServer(t).Handler()
	// Finish the seeded planned EVA first.
	call(h, "POST", "/api/mission/start", local, nil, "{}")
	if w := call(h, "POST", "/api/mission/end", local, nil, "{}"); w.Code != 200 {
		t.Fatalf("end = %d %s", w.Code, w.Body.String())
	}
	bad := `{"id":"SELENE3-EVA03","name":"EVA 3","environment":"Moon","site":"Rim","plannedH":4,"crew":[{"crewId":"AST-001","role":"IV"}]}`
	if w := call(h, "POST", "/api/missions", local, nil, bad); w.Code != 400 {
		t.Fatalf("mission without EV crew = %d", w.Code)
	}
	ok := `{"id":"SELENE3-EVA03","name":"EVA 3","environment":"Moon","site":"Rim","plannedH":4,"crew":[{"crewId":"AST-003","role":"EV1"},{"crewId":"AST-001","role":"IV"}]}`
	if w := call(h, "POST", "/api/missions", local, nil, ok); w.Code != 201 {
		t.Fatalf("create mission = %d %s", w.Code, w.Body.String())
	}
	var crew []store.Assignment
	decode(t, call(h, "GET", "/api/missions/SELENE3-EVA03/crew", local, nil, ""), &crew)
	if len(crew) != 2 {
		t.Fatalf("assigned crew = %v", crew)
	}
	if w := call(h, "POST", "/api/mission/start", local, nil, "{}"); w.Code != 200 {
		t.Fatalf("start = %d %s", w.Code, w.Body.String())
	}
	var list []struct {
		Crew struct{ ID, Role string } `json:"crew"`
	}
	decode(t, call(h, "GET", "/api/crew", local, nil, ""), &list)
	roles := map[string]string{}
	for _, x := range list {
		roles[x.Crew.ID] = x.Crew.Role
	}
	if roles["AST-003"] != "EV1" || roles["AST-001"] != "IV" {
		t.Fatalf("roles after start = %v", roles)
	}
	if w := call(h, "PUT", "/api/missions/SELENE3-EVA03/crew", local, nil, `[]`); w.Code != 409 {
		t.Fatalf("changing the crew of an active EVA = %d, want 409", w.Code)
	}
}

func TestCrewRecordsAndContacts(t *testing.T) {
	srv := newTestServer(t)
	h := srv.Handler()
	if w := call(h, "POST", "/api/crew", local, nil, `{"id":"AST-004","name":"Kid","role":"EV3","sex":"F","age":10}`); w.Code != 400 {
		t.Fatalf("invalid age = %d", w.Code)
	}
	body := `{"id":"AST-004","name":"Rosa Ibarra","role":"EV3","specialty":"Biologist","sex":"F","age":36,"heightCm":165,"weightKg":60,"bloodType":"O-","allergies":"None known"}`
	if w := call(h, "POST", "/api/crew", local, nil, body); w.Code != 201 {
		t.Fatalf("create crew = %d %s", w.Code, w.Body.String())
	}
	if !srv.Eng.Known("AST-004") {
		t.Fatal("engine does not know the new astronaut")
	}
	if w := call(h, "POST", "/api/crew", local, nil, body); w.Code != 409 {
		t.Fatalf("duplicate = %d", w.Code)
	}
	if w := call(h, "PUT", "/api/crew/AST-004", local, nil, strings.Replace(body, "None known", "Latex", 1)); w.Code != 200 {
		t.Fatalf("update = %d %s", w.Code, w.Body.String())
	}
	if w := call(h, "PUT", "/api/crew/AST-004/contacts", local, nil, `[{"name":"Marco Ibarra","relation":"Brother","channel":"Ground relay"}]`); w.Code != 200 {
		t.Fatalf("contacts = %d %s", w.Code, w.Body.String())
	}
	var cs []store.Contact
	decode(t, call(h, "GET", "/api/crew/AST-004/contacts", local, nil, ""), &cs)
	if len(cs) != 1 || cs[0].Name != "Marco Ibarra" {
		t.Fatalf("contacts = %v", cs)
	}
	if w := call(h, "GET", "/api/crew/AST-001/vitals", local, nil, ""); w.Code != 200 {
		t.Fatalf("vitals = %d", w.Code)
	}
}

func TestBackupsStatusSyncSensorsAndBlackboxFilter(t *testing.T) {
	h := newTestServer(t).Handler()
	var b store.BackupInfo
	w := call(h, "POST", "/api/backups", local, nil, "")
	decode(t, w, &b)
	if w.Code != 201 || !b.Verified {
		t.Fatalf("backup = %d %+v", w.Code, b)
	}
	var bs []store.BackupInfo
	decode(t, call(h, "GET", "/api/backups", local, nil, ""), &bs)
	if len(bs) != 1 {
		t.Fatalf("backups = %v", bs)
	}
	var st struct {
		Schema int `json:"schema"`
	}
	decode(t, call(h, "GET", "/api/status", local, nil, ""), &st)
	if st.Schema != store.LatestSchema {
		t.Fatalf("status schema = %d", st.Schema)
	}
	var sync []map[string]any
	decode(t, call(h, "GET", "/api/sync", local, nil, ""), &sync)
	if len(sync) != 3 {
		t.Fatalf("sync rows = %d", len(sync))
	}
	if w := call(h, "POST", "/api/sensors", local, nil, `{"crewId":"AST-001","kind":"teleporter","interface":"ble-gatt"}`); w.Code != 400 {
		t.Fatalf("invalid sensor = %d", w.Code)
	}
	var sen store.Sensor
	w = call(h, "POST", "/api/sensors", local, nil, `{"crewId":"AST-001","kind":"hr","interface":"ble-gatt","profile":"Heart Rate 0x180D","model":"Chest strap"}`)
	decode(t, w, &sen)
	if w.Code != 201 || sen.ID == "" {
		t.Fatalf("add sensor = %d %s", w.Code, w.Body.String())
	}
	if w := call(h, "PUT", "/api/sensors/"+sen.ID, local, nil, `{"status":"connected","batteryPct":80}`); w.Code != 200 {
		t.Fatalf("update sensor = %d %s", w.Code, w.Body.String())
	}
	var chain []struct{ Kind string }
	decode(t, call(h, "GET", "/api/blackbox?kind=BACKUP", local, nil, ""), &chain)
	if len(chain) == 0 {
		t.Fatal("no BACKUP entry found by the filter")
	}
	for _, e := range chain {
		if e.Kind != "BACKUP" {
			t.Fatalf("filter returned %q", e.Kind)
		}
	}
}
