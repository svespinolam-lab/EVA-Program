// Package server is the HTTP + WebSocket layer of EVA Station.
//
//	/                 EVA Station (PC program)
//	/app/             EVA mobile app (open it on each explorer's phone)
//	/shared/          shared design system (tokens, components, icons, logo)
//	/ws/device        phone link (telemetry, readiness, acks ↔ alerts, instructions)
//	/ws/console       live feed for the PC program
//	/api/...          actions and data (JSON) · /reports/... printable reports
package server

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"eva/internal/engine"
	"eva/internal/model"
	"eva/internal/sim"
	"eva/internal/store"
	"eva/internal/ws"
)

// Version of EVA Station.
const Version = "1.0.0"

// Server wires the engine, the simulator, the hub and the web files.
type Server struct {
	Eng  *engine.Engine
	St   *store.Store
	Sim  *sim.Sim
	Hub  *Hub
	Web  fs.FS // contains app/, pc/ and shared/
	Port int
}

// Handler returns the HTTP routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Live links
	mux.HandleFunc("GET /ws/device", s.wsDevice)
	mux.HandleFunc("GET /ws/console", s.wsConsole)

	// Data and actions
	mux.HandleFunc("GET /api/info", s.apiInfo)
	mux.HandleFunc("GET /api/health/database", s.apiDatabaseHealth)
	mux.HandleFunc("GET /api/state", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, s.Eng.Snapshot()) })
	mux.HandleFunc("POST /api/mission/start", s.apiMission("start"))
	mux.HandleFunc("POST /api/mission/end", s.apiMission("end"))
	mux.HandleFunc("POST /api/mission/abort", s.apiMission("abort"))
	mux.HandleFunc("POST /api/mission/new", s.apiMission("new"))
	mux.HandleFunc("POST /api/alerts/{id}/ack", s.apiAck)
	mux.HandleFunc("POST /api/instruction", s.apiInstruction)
	mux.HandleFunc("POST /api/crew/{id}/incident", s.apiIncident)
	mux.HandleFunc("POST /api/crew/{id}/help-clear", func(w http.ResponseWriter, r *http.Request) {
		s.Eng.ClearHelp(r.PathValue("id"))
		writeJSON(w, 200, map[string]any{"ok": true})
	})
	mux.HandleFunc("GET /api/crew/{id}/readiness", s.apiReadiness)
	mux.HandleFunc("POST /api/incidents/{id}/seal", s.apiSeal)
	mux.HandleFunc("GET /api/sim", s.apiSim)
	mux.HandleFunc("POST /api/sim/inject", s.apiSimInject)
	mux.HandleFunc("POST /api/sim/clear", s.apiSimClear)
	mux.HandleFunc("POST /api/sim/virtual", s.apiSimVirtual)
	mux.HandleFunc("GET /api/blackbox", s.apiChain)
	mux.HandleFunc("GET /api/blackbox/verify", s.apiVerify)
	mux.HandleFunc("GET /api/export/{table}", s.apiExport)

	// Printable reports (HTML → Print → PDF)
	mux.HandleFunc("GET /reports/mission/{id}", s.reportMission)
	mux.HandleFunc("GET /reports/incident/{id}", s.reportIncident)
	mux.HandleFunc("GET /reports/monthly/{crew}", s.reportMonthly)

	// Static front-ends
	sub := func(dir string) http.Handler {
		f, err := fs.Sub(s.Web, dir)
		if err != nil {
			return http.NotFoundHandler()
		}
		return noCache(http.FileServer(http.FS(f)))
	}
	mux.Handle("GET /app/", http.StripPrefix("/app/", sub("app")))
	mux.Handle("GET /shared/", http.StripPrefix("/shared/", sub("shared")))
	mux.HandleFunc("GET /app", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/app/", http.StatusFound) })
	mux.Handle("GET /", sub("pc"))
	return cors(mux)
}

func noCache(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		h.ServeHTTP(w, r)
	})
}

// cors allows a phone app served from another origin (e.g. file:// or a
// dev server) to call the API on the isolated mission network.
func cors(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]any{"ok": false, "error": err.Error()})
}

func readJSON(r *http.Request, v any) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return err
	}
	if len(body) == 0 {
		return nil
	}
	return json.Unmarshal(body, v)
}

/* =====================================================================
   WebSocket: phones
   ===================================================================== */

type envelope struct {
	Type     string          `json:"type"`
	CrewID   string          `json:"crewId"`
	Device   string          `json:"device"`
	AlertID  int64           `json:"alertId"`
	Response string          `json:"response"`
	N        int             `json:"n"`
	Items    []model.Sample  `json:"items"`
	Raw      json.RawMessage `json:"-"`
}

func (s *Server) wsDevice(w http.ResponseWriter, r *http.Request) {
	conn, err := ws.Upgrade(w, r)
	if err != nil {
		return
	}
	defer conn.Close()

	// The first message must be {type:"hello", crewId, device}
	_ = conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	b, err := conn.ReadMessage()
	if err != nil {
		return
	}
	var hello envelope
	if json.Unmarshal(b, &hello) != nil || hello.Type != "hello" || !s.Eng.Known(hello.CrewID) {
		_ = conn.WriteText(marshal(map[string]any{"type": "error", "error": "unknown crew member: send {type:'hello', crewId:'AST-001'}"}))
		return
	}
	name := strings.TrimSpace(hello.Device)
	if name == "" {
		name = "Phone"
	}
	if len(name) > 60 {
		name = name[:60]
	}
	c := newClient(conn)
	c.crewID, c.name = hello.CrewID, name
	defer c.close()
	s.Hub.addDevice(c)
	s.Eng.DeviceOnline(c.crewID, name+" · "+hostOnly(conn.RemoteAddr()))
	c.send(marshal(s.Eng.Welcome(c.crewID)))

	for {
		_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		b, err := conn.ReadMessage()
		if err != nil {
			break
		}
		var env envelope
		if json.Unmarshal(b, &env) != nil {
			continue
		}
		switch env.Type {
		case "telemetry":
			var smp model.Sample
			if json.Unmarshal(b, &smp) == nil {
				smp.CrewID = c.crewID
				s.Eng.Ingest(smp, "device")
			}
		case "batch":
			n, _ := s.Eng.IngestBatch(c.crewID, env.Items, "device")
			c.send(marshal(map[string]any{"type": "batch-ok", "n": n}))
		case "queue":
			s.Eng.SetQueued(c.crewID, env.N)
		case "readiness":
			var rd model.Readiness
			if json.Unmarshal(b, &rd) == nil {
				rd.CrewID = c.crewID
				if _, err := s.Eng.SetReadiness(rd); err != nil {
					c.send(marshal(map[string]any{"type": "error", "error": err.Error()}))
				} else {
					c.send(marshal(map[string]any{"type": "readiness-ok", "day": rd.Day}))
				}
			}
		case "ack":
			if _, err := s.Eng.Ack(env.AlertID, env.Response, "crew"); err != nil {
				c.send(marshal(map[string]any{"type": "error", "error": err.Error()}))
			}
		case "incident":
			_, _ = s.Eng.ReportIncident(c.crewID, "crew:"+c.crewID)
		case "ping":
			c.send(marshal(map[string]any{"type": "pong", "t": store.Now()}))
		}
	}
	if s.Hub.removeDevice(c) {
		s.Eng.DeviceOffline(c.crewID)
	}
}

func hostOnly(addr string) string {
	if h, _, err := net.SplitHostPort(addr); err == nil {
		return h
	}
	return addr
}

// OnCommit acknowledges stored readings to each phone (store-and-forward):
// the phone deletes its queued readings up to that sequence number.
func (s *Server) OnCommit(maxSeq map[string]int64) {
	for crew, seq := range maxSeq {
		s.Hub.Device(crew, map[string]any{"type": "ack-seq", "seq": seq})
	}
}

/* =====================================================================
   WebSocket: PC consoles
   ===================================================================== */

func (s *Server) wsConsole(w http.ResponseWriter, r *http.Request) {
	conn, err := ws.Upgrade(w, r)
	if err != nil {
		return
	}
	c := newClient(conn)
	defer c.close()
	snap := s.Eng.Snapshot()
	snap["sim"] = s.simState()
	snap["info"] = s.info()
	c.send(marshal(snap))
	s.Hub.addConsole(c)
	defer s.Hub.removeConsole(c)
	for {
		_ = conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		if _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}

/* =====================================================================
   REST API
   ===================================================================== */

// LANURLs lists http://IPv4:port for every active network interface.
func LANURLs(port int) []string {
	var out []string
	ifaces, _ := net.Interfaces()
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok {
				if ip4 := ipn.IP.To4(); ip4 != nil && !ip4.IsLinkLocalUnicast() {
					out = append(out, fmt.Sprintf("http://%s:%d", ip4, port))
				}
			}
		}
	}
	sort.Strings(out)
	return out
}

func (s *Server) info() map[string]any {
	cons, devs := s.Hub.Counts()
	host, _ := os.Hostname()
	return map[string]any{"version": Version, "host": host, "port": s.Port, "urls": LANURLs(s.Port), "consoles": cons, "devices": devs, "startedAt": s.Eng.Started}
}

func (s *Server) apiInfo(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, s.info()) }

func (s *Server) apiMission(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Reason string `json:"reason"`
		}
		_ = readJSON(r, &body)
		var m model.Mission
		var err error
		switch action {
		case "start":
			m, err = s.Eng.StartMission()
		case "end":
			m, err = s.Eng.EndMission(false, "")
		case "abort":
			m, err = s.Eng.EndMission(true, body.Reason)
		case "new":
			m, err = s.Eng.NewMission()
		}
		if err != nil {
			writeErr(w, http.StatusConflict, err)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "mission": m})
	}
}

func (s *Server) apiAck(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, 400, errors.New("bad alert id"))
		return
	}
	var body struct {
		Response string `json:"response"`
	}
	_ = readJSON(r, &body)
	if body.Response == "" {
		body.Response = "COPIED"
	}
	a, err := s.Eng.Ack(id, body.Response, "operator")
	if err != nil {
		writeErr(w, 404, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "alert": a})
}

func (s *Server) apiInstruction(w http.ResponseWriter, r *http.Request) {
	var body struct {
		CrewID string `json:"crewId"`
		Text   string `json:"text"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, 400, err)
		return
	}
	if err := s.Eng.Instruction(body.CrewID, body.Text); err != nil {
		writeErr(w, 400, err)
		return
	}
	delivered := body.CrewID == "" || s.Hub.Online(body.CrewID)
	writeJSON(w, 200, map[string]any{"ok": true, "delivered": delivered})
}

func (s *Server) apiIncident(w http.ResponseWriter, r *http.Request) {
	inc, err := s.Eng.ReportIncident(r.PathValue("id"), "operator")
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "incident": inc})
}

func (s *Server) apiReadiness(w http.ResponseWriter, r *http.Request) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days <= 0 || days > 60 {
		days = 14
	}
	rs, err := s.St.Readiness(r.PathValue("id"), days)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	if rs == nil {
		rs = []model.Readiness{}
	}
	writeJSON(w, 200, rs)
}

func (s *Server) apiSeal(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	inc, err := s.Eng.SealNow(id)
	if err != nil {
		writeErr(w, 409, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "incident": inc})
}

func (s *Server) simState() map[string]any {
	return map[string]any{"scenarios": sim.Scenarios, "virtual": s.Sim.States(), "active": s.Sim.Active()}
}

func (s *Server) apiSim(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, s.simState()) }

func (s *Server) broadcastSim() {
	s.Hub.Console(map[string]any{"type": "sim", "sim": s.simState()})
}

// apiSimInject sends the failure to the explorer's phone if it is linked
// (the phone simulates its own suit), otherwise to the virtual suit.
func (s *Server) apiSimInject(w http.ResponseWriter, r *http.Request) {
	var body struct {
		CrewID   string `json:"crewId"`
		Scenario string `json:"scenario"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, 400, err)
		return
	}
	target := ""
	switch {
	case s.Hub.Online(body.CrewID):
		s.Hub.Device(body.CrewID, map[string]any{"type": "inject", "scenario": body.Scenario})
		target = "phone"
	case s.Sim.Has(body.CrewID):
		if !s.Sim.Enabled(body.CrewID) {
			s.Sim.SetEnabled(body.CrewID, true)
		}
		if !s.Sim.Inject(body.CrewID, body.Scenario) {
			writeErr(w, 400, errors.New("unknown scenario"))
			return
		}
		target = "virtual suit"
	default:
		writeErr(w, 400, errors.New("no phone or virtual suit for this crew member"))
		return
	}
	_, _ = s.St.Log(s.Eng.Mission().ID, "operator", body.CrewID, "INJECT", "Simulated failure "+body.Scenario+" → "+target)
	s.broadcastSim()
	writeJSON(w, 200, map[string]any{"ok": true, "target": target})
}

func (s *Server) apiSimClear(w http.ResponseWriter, r *http.Request) {
	var body struct {
		CrewID string `json:"crewId"`
	}
	_ = readJSON(r, &body)
	s.Sim.Clear(body.CrewID)
	if body.CrewID == "" {
		s.Hub.DevicesExcept("", map[string]any{"type": "clear"})
	} else {
		s.Hub.Device(body.CrewID, map[string]any{"type": "clear"})
	}
	s.broadcastSim()
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) apiSimVirtual(w http.ResponseWriter, r *http.Request) {
	var body struct {
		CrewID string `json:"crewId"`
		On     bool   `json:"on"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, 400, err)
		return
	}
	if body.On && s.Hub.Online(body.CrewID) {
		writeErr(w, 409, errors.New("this crew member's phone is linked; it provides the telemetry"))
		return
	}
	if !s.Sim.Has(body.CrewID) {
		writeErr(w, 400, errors.New("only EVA crew members have a suit"))
		return
	}
	s.Sim.SetEnabled(body.CrewID, body.On)
	s.broadcastSim()
	writeJSON(w, 200, map[string]any{"ok": true})
}

// apiDatabaseHealth performs a read-only SQLite integrity check and reports row counts.
func (s *Server) apiDatabaseHealth(w http.ResponseWriter, r *http.Request) {
	s.St.Flush()
	if err := s.St.R.Ping(); err != nil {
		writeJSON(w, 503, map[string]any{"ok": false, "database": "unavailable", "error": err.Error()})
		return
	}
	var integrity string
	if err := s.St.R.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil {
		writeJSON(w, 500, map[string]any{"ok": false, "database": "check_failed", "error": err.Error()})
		return
	}
	counts := map[string]int64{}
	for _, table := range []string{"crew", "missions", "telemetry", "alerts", "incidents", "readiness", "oplog"} {
		var n int64
		if err := s.St.R.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
			writeJSON(w, 500, map[string]any{"ok": false, "database": "query_failed", "table": table, "error": err.Error()})
			return
		}
		counts[table] = n
	}
	ok := integrity == "ok"
	status := 200
	if !ok { status = 500 }
	writeJSON(w, status, map[string]any{"ok": ok, "database": "SQLite", "integrity": integrity, "journalMode": "WAL", "counts": counts, "checkedAt": time.Now().UnixMilli()})
}

func (s *Server) apiChain(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 1000 {
		limit = 150
	}
	list, err := s.St.ChainList(limit)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	if list == nil {
		list = []model.ChainEntry{}
	}
	writeJSON(w, 200, list)
}

func (s *Server) apiVerify(w http.ResponseWriter, r *http.Request) {
	rep, err := s.St.VerifyChain()
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, rep)
}

func (s *Server) apiExport(w http.ResponseWriter, r *http.Request) {
	table := strings.TrimSuffix(r.PathValue("table"), ".csv")
	cols, rows, err := s.St.ExportRows(table)
	if err != nil {
		writeErr(w, 404, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="eva-%s-%s.csv"`, table, time.Now().Format("2006-01-02")))
	_, _ = w.Write([]byte{0xEF, 0xBB, 0xBF}) // UTF-8 BOM so Excel shows accents and units
	cw := csv.NewWriter(w)
	_ = cw.Write(cols)
	_ = cw.WriteAll(rows)
	cw.Flush()
}
