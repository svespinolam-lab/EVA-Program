// Package engine is the triage brain of EVA Station: it ingests the
// telemetry of every explorer, keeps personal baselines, computes the
// Surveillance Priority Index (IPV), raises and escalates alerts, records
// the black box and assembles the reports.
//
// Concurrency: one mutex protects all state. Every public method takes it.
// The engine never calls back into the HTTP layer synchronously except
// through the Emitter, whose methods only enqueue messages.
package engine

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"eva/internal/model"
	"eva/internal/store"
)

// Emitter delivers messages to the connected consoles and phones.
type Emitter interface {
	Console(msg any)
	Device(crewID string, msg any) bool
	DevicesExcept(crewID string, msg any) // crewID "" = every device
}

// VirtualSuits controls the server-side suit simulator (internal/sim).
type VirtualSuits interface {
	Enabled(crewID string) bool
	SetEnabled(crewID string, on bool)
}

const (
	ringSeconds  = 45 * 60 // black-box ring buffer per astronaut
	trendSeconds = 15 * 60 // trend shown on the console
	linkLostMs   = 10000
	escalateMs   = 30000
	incidentPre  = 30 * 60 * 1000
	incidentPost = 15 * 60 * 1000
)

type point struct {
	T                       int64
	HR, SpO2, Temp, RR, HRV float64
	IPV                     int
}

type crewState struct {
	m       model.Crew
	b       model.Baseline
	latest  *model.Sample
	lastT   int64
	source  string
	ring    []point
	spo2w   []float64
	prevHR  float64
	prevHRT int64
	hrSame  int
	hrJump  bool
	cal     model.Calib
	today   *model.Readiness
	hist    []model.Readiness // newest first
	recent  *model.EVASummary
	conds   map[string]*cond
	eval    Evaluation
	factors []Factor
	vuln    int
	vulnAt  int64
	dose    float64
	help    int64 // > 0: crew asked for help at this time

	devOnline bool
	devName   string
	devSince  int64
	queued    int
}

func (cs *crewState) spo2Median(v float64) float64 {
	cs.spo2w = append(cs.spo2w, v)
	if len(cs.spo2w) > 5 {
		cs.spo2w = cs.spo2w[len(cs.spo2w)-5:]
	}
	c := append([]float64(nil), cs.spo2w...)
	sort.Float64s(c)
	return c[len(c)/2]
}

func (cs *crewState) pushRing(p point) {
	cs.ring = append(cs.ring, p)
	if len(cs.ring) > ringSeconds+60 {
		cs.ring = append(cs.ring[:0], cs.ring[len(cs.ring)-ringSeconds:]...)
	}
}

func nanOr(p *float64) float64 {
	if p == nil {
		return math.NaN()
	}
	return *p
}

// Evaluation is the live triage result of one astronaut.
type Evaluation struct {
	IPV       int       `json:"ipv"`
	Status    string    `json:"status"` // STABLE VULNERABLE RISK EMERGENCY NO_DATA
	Level     int       `json:"level"`  // 0..3 colour scale, -1 = no data
	Findings  []Finding `json:"findings"`
	Diagnosis string    `json:"diagnosis"`
	Protocol  []string  `json:"protocol"`
	Voice     string    `json:"voice,omitempty"`
	Updated   int64     `json:"updated"`
}

type openAlert struct {
	a        model.Alert
	notified int64 // last time it was (re)announced: start of the 30 s window
}

// Engine is the triage engine.
type Engine struct {
	mu        sync.Mutex
	st        *store.Store
	em        Emitter
	vs        VirtualSuits
	order     []string
	cs        map[string]*crewState
	mission   model.Mission
	open      map[int64]*openAlert
	recording map[int64]*model.Incident
	Started   int64
}

// New loads crew, baselines, readiness and the current mission.
func New(st *store.Store) (*Engine, error) {
	e := &Engine{st: st, cs: map[string]*crewState{}, open: map[int64]*openAlert{}, recording: map[int64]*model.Incident{}, Started: store.Now()}
	crew, err := st.ListCrew()
	if err != nil {
		return nil, err
	}
	bases, err := st.Baselines()
	if err != nil {
		return nil, err
	}
	now := store.Now()
	for _, c := range crew {
		cs := &crewState{m: c, b: bases[c.ID], conds: map[string]*cond{}}
		if cs.b.CrewID == "" {
			cs.b = model.Baseline{CrewID: c.ID, Phase: "ADAPTATION_14D", HRRest: 65, HRRestSD: 5, HRV: 50, HRVSD: 8, SpO2: 98, SpO2SD: 1, Temp: 36.6, TempSD: 0.2, RR: 14, RRSD: 2, BPSys: 118, BPDia: 76, SleepH: 7.5}
		}
		e.cs[c.ID] = cs
		e.order = append(e.order, c.ID)
		e.loadReadiness(cs)
		if xs, err := st.Summaries(c.ID, now-36*3600*1000); err == nil && len(xs) > 0 {
			x := xs[0]
			cs.recent = &x
		}
	}
	if m, err := st.CurrentMission(); err == nil {
		e.mission = m
	}
	// Alerts left open by a previous run are closed (their context is gone).
	_ = st.ResolveOpenAlerts(now)
	if incs, err := st.Incidents("RECORDING", 100); err == nil {
		for i := range incs {
			inc := incs[i]
			e.recording[inc.ID] = &inc
		}
	}
	for _, id := range e.order {
		e.evaluate(e.cs[id], now, false)
	}
	return e, nil
}

// SetEmitter connects the engine to the message hub.
func (e *Engine) SetEmitter(em Emitter) { e.mu.Lock(); e.em = em; e.mu.Unlock() }

// SetVirtualSuits connects the suit simulator.
func (e *Engine) SetVirtualSuits(v VirtualSuits) { e.mu.Lock(); e.vs = v; e.mu.Unlock() }

func (e *Engine) console(msg any) {
	if e.em != nil {
		e.em.Console(msg)
	}
}
func (e *Engine) device(crewID string, msg any) {
	if e.em != nil {
		e.em.Device(crewID, msg)
	}
}
func (e *Engine) devicesExcept(crewID string, msg any) {
	if e.em != nil {
		e.em.DevicesExcept(crewID, msg)
	}
}

func (e *Engine) missionIDAt(ts int64) string {
	if e.mission.Status == "ACTIVE" && e.mission.StartedAt != nil && ts >= *e.mission.StartedAt-5000 {
		return e.mission.ID
	}
	return ""
}

func (e *Engine) log(actor, crewID, action, detail string) {
	entry, err := e.st.Log(e.missionIDAt(store.Now()), actor, crewID, action, detail)
	if err == nil {
		e.console(map[string]any{"type": "oplog", "entry": entry})
	}
}

func today() string { return time.Now().Format("2006-01-02") }

func (e *Engine) loadReadiness(cs *crewState) {
	hist, err := e.st.Readiness(cs.m.ID, 21)
	if err != nil {
		return
	}
	cs.hist = hist
	cs.today = nil
	if len(hist) > 0 && hist[0].Day == today() {
		r := hist[0]
		cs.today = &r
	}
	cs.vulnAt = 0
}

/* =====================================================================
   Ingestion
   ===================================================================== */

// Ingest processes one live reading from a phone ("device") or a virtual suit.
func sampleSource(s model.Sample, fallback string) string {
	switch s.Source {
	case "device", "virtual", "replay":
		return s.Source
	}
	return fallback
}

func (e *Engine) Ingest(s model.Sample, source string) {
	source = sampleSource(s, source)
	e.mu.Lock()
	defer e.mu.Unlock()
	cs := e.cs[s.CrewID]
	if cs == nil {
		return
	}
	now := store.Now()
	if s.T <= 0 || s.T > now+5000 {
		s.T = now
	}
	if cs.latest != nil && s.T <= cs.latest.T {
		e.backfill(cs, s, source) // duplicate or late reading
		return
	}
	if now-s.T > 15000 {
		e.backfill(cs, s, source) // old reading from the offline queue
		return
	}
	e.ingestLive(cs, s, source, now)
}

// IngestBatch processes a store-and-forward batch (oldest first).
func (e *Engine) IngestBatch(crewID string, items []model.Sample, source string) (int, int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	cs := e.cs[crewID]
	if cs == nil {
		return 0, 0
	}
	sort.Slice(items, func(i, j int) bool { return items[i].T < items[j].T })
	now := store.Now()
	stored, worst := 0, 0
	var firstEmergency int64
	for _, s := range items {
		sourceForSample := sampleSource(s, source)
		s.CrewID = crewID
		if s.T <= 0 || s.T > now+5000 {
			continue
		}
		if now-s.T <= 15000 && (cs.latest == nil || s.T > cs.latest.T) {
			e.ingestLive(cs, s, sourceForSample, now)
		} else {
			lvl := e.backfill(cs, s, sourceForSample)
			if lvl > worst {
				worst = lvl
			}
			if lvl >= 3 && firstEmergency == 0 {
				firstEmergency = s.T
			}
		}
		stored++
	}
	if stored > 0 {
		e.log("crew:"+crewID, crewID, "SYNC", fmt.Sprintf("Store-and-forward: %d readings received after a link outage (worst level %s)", stored, levelWord(worst)))
	}
	if firstEmergency > 0 {
		e.openIncident(cs, "EMERGENCY", nil, firstEmergency)
	}
	return stored, worst
}

// backfill stores a past reading with a stateless evaluation (no live alerts).
func (e *Engine) backfill(cs *crewState, s model.Sample, source string) int {
	raws := rawFindings(cs, &s, false)
	var fs []Finding
	for _, r := range raws {
		fs = append(fs, Finding{Code: r.code, Level: r.level})
	}
	ipv, status, level := ipvOf(fs, cs.vuln, proximity(cs, &s))
	e.st.AddTelemetry(s, e.missionIDAt(s.T), ipv, status, source)
	return level
}

func (e *Engine) ingestLive(cs *crewState, s model.Sample, source string, now int64) {
	// live filters: flat line and abrupt jumps of heart rate
	if hr, ok := val(s.HR); ok {
		if cs.prevHRT > 0 && hr == cs.prevHR {
			cs.hrSame++
		} else {
			cs.hrSame = 0
		}
		cs.hrJump = cs.prevHRT > 0 && s.T-cs.prevHRT <= 2000 && math.Abs(hr-cs.prevHR) > 45
		cs.prevHR, cs.prevHRT = hr, s.T
	}
	if s.Cal != nil {
		cs.cal = *s.Cal
	}
	if cs.lastT > 0 && e.mission.Status == "ACTIVE" {
		if r, ok := val(s.Rad); ok {
			dt := math.Min(math.Max(float64(s.T-cs.lastT)/1000, 0), 5)
			cs.dose += r * dt / 3600
		}
	}
	// keep the last blood pressure if this reading has none
	if s.BPSys == nil && cs.latest != nil {
		s.BPSys, s.BPDia = cs.latest.BPSys, cs.latest.BPDia
	}
	ss := s
	cs.latest = &ss
	cs.lastT = s.T
	cs.source = source
	e.evaluate(cs, now, true)
	cs.pushRing(point{T: s.T, HR: nanOr(s.HR), SpO2: nanOr(s.SpO2), Temp: nanOr(s.Temp), RR: nanOr(s.RR), HRV: nanOr(s.HRV), IPV: cs.eval.IPV})
	status := cs.eval.Status
	if status == "NO_DATA" {
		status = "STABLE"
	}
	e.st.AddTelemetry(s, e.missionIDAt(s.T), cs.eval.IPV, status, source)
}

/* =====================================================================
   Evaluation and alerts
   ===================================================================== */

func (e *Engine) systemFindings(cs *crewState, now int64) []Finding {
	var out []Finding
	if e.mission.Status == "ACTIVE" && cs.m.IsEVA() && cs.lastT > 0 && now-cs.lastT > linkLostMs {
		out = append(out, Finding{Code: "LINK_LOST", Level: 2, Label: "Telemetry lost", Since: cs.lastT + linkLostMs})
	}
	for _, oa := range e.open {
		if oa.a.CrewID == cs.m.ID && oa.a.Escalated && oa.a.Response == "PENDING" && oa.a.EscalatedAt != nil {
			out = append(out, Finding{Code: "NO_RESPONSE", Level: 3, Label: "No response to alert", Since: *oa.a.EscalatedAt})
			break
		}
	}
	if cs.help > 0 {
		out = append(out, Finding{Code: "CREW_HELP", Level: 3, Label: "Crew member asked for help", Since: cs.help})
	}
	return out
}

// evaluate recomputes the triage of one astronaut. advance=true when a new
// reading arrived (it counts for persistence); false for timer updates.
func (e *Engine) evaluate(cs *crewState, now int64, advance bool) {
	if advance && cs.latest != nil {
		best := map[string]rawFinding{}
		for _, r := range rawFindings(cs, cs.latest, true) {
			if x, ok := best[r.code]; !ok || r.level > x.level {
				best[r.code] = r
			}
		}
		for code, r := range best {
			c := cs.conds[code]
			if c == nil {
				c = &cond{}
				cs.conds[code] = c
			}
			c.step(r.level, r.immediate, now)
			c.last = r
		}
		for code, c := range cs.conds {
			if _, ok := best[code]; ok {
				continue
			}
			c.step(0, false, now)
			if c.level == 0 {
				delete(cs.conds, code)
			}
		}
	}
	var fs []Finding
	for code, c := range cs.conds {
		if c.level <= 0 {
			continue
		}
		l, v := labelValue(code, c.level, cs.latest)
		fs = append(fs, Finding{Code: code, Level: c.level, Metric: c.last.metric, Label: l, Value: v, Since: c.since, Fault: c.last.fault})
	}
	fs = append(fs, e.systemFindings(cs, now)...)
	sortFindings(fs)
	if now-cs.vulnAt > 30000 {
		e.computeVuln(cs, now)
	}
	ipv, status, level := ipvOf(fs, cs.vuln, proximity(cs, cs.latest))
	ev := Evaluation{IPV: ipv, Status: status, Level: level, Findings: fs, Updated: now}
	switch {
	case len(fs) > 0:
		top := fs[0]
		ev.Diagnosis = diagnosis(cs, top, now)
		ev.Protocol = protocol(top.Code, top.Level)
		ev.Voice = voice(top.Code, top.Level, cs.latest)
	case status == "VULNERABLE":
		ev.Diagnosis = "Preventive watch: " + factorText(cs.factors, 3)
		ev.Protocol = []string{"Keep preventive watch during the EVA", "Limit the EVA to 2 h if readiness is YELLOW", "Radio check-in every 30 min"}
	case cs.latest != nil:
		ev.Diagnosis = "Within personal baseline · " + effortWord[effortOf(cs.latest)]
		ev.Protocol = []string{"Passive monitoring"}
	default:
		ev.Diagnosis = "No telemetry yet"
		ev.Protocol = []string{"Waiting for the suit link"}
	}
	if cs.latest == nil && len(fs) == 0 {
		ev.Status, ev.Level = "NO_DATA", -1
	}
	cs.eval = ev
	e.syncAlerts(cs, now)
}

func factorText(fs []Factor, max int) string {
	var parts []string
	for i, f := range fs {
		if i >= max {
			parts = append(parts, fmt.Sprintf("+%d more", len(fs)-max))
			break
		}
		parts = append(parts, f.Label)
	}
	if len(parts) == 0 {
		return "accumulated factors"
	}
	return strings.Join(parts, " · ")
}

func levelName(l int) string {
	switch {
	case l >= 3:
		return "EMERGENCY"
	case l == 2:
		return "RISK"
	}
	return "ADVISORY"
}

// syncAlerts opens, upgrades and resolves the alerts of one astronaut so
// that they mirror the persisted findings (one alert per condition family).
func (e *Engine) syncAlerts(cs *crewState, now int64) {
	active := map[string]Finding{}
	for _, f := range cs.eval.Findings {
		if f.Code == "NO_RESPONSE" || f.Code == "CREW_HELP" {
			continue // consequences of another alert, not new alerts
		}
		active[f.Code] = f
	}
	for id, oa := range e.open {
		if oa.a.CrewID != cs.m.ID {
			continue
		}
		f, ok := active[oa.a.Code]
		if !ok {
			e.resolveAlert(oa, now)
			delete(e.open, id)
			continue
		}
		delete(active, oa.a.Code)
		oa.a.Reason = diagnosis(cs, f, now)
		if f.Level > oa.a.LevelNum() {
			e.upgradeAlert(cs, oa, f, now)
		}
	}
	codes := make([]string, 0, len(active))
	for c := range active {
		codes = append(codes, c)
	}
	sort.Strings(codes)
	for _, c := range codes {
		e.raiseAlert(cs, active[c], now)
	}
}

func (e *Engine) raiseAlert(cs *crewState, f Finding, now int64) {
	a := model.Alert{
		MissionID: e.missionIDAt(now), CrewID: cs.m.ID, T: now, Level: levelName(f.Level), Code: f.Code, Metric: f.Metric,
		Voice: voice(f.Code, f.Level, cs.latest), Reason: diagnosis(cs, f, now), Protocol: protocol(f.Code, f.Level), Response: "PENDING",
	}
	id, err := e.st.InsertAlert(a)
	if err != nil {
		return
	}
	a.ID = id
	e.open[id] = &openAlert{a: a, notified: now}
	e.st.Chain("ALERT_RAISED", cs.m.ID, strconv.FormatInt(id, 10), map[string]any{"level": a.Level, "code": a.Code, "reason": a.Reason})
	e.console(map[string]any{"type": "alert", "alert": a})
	if f.Level >= 2 {
		e.device(cs.m.ID, map[string]any{"type": "alert", "alert": a})
	}
	if f.Level >= 3 {
		e.openIncident(cs, "EMERGENCY", &id, now)
	}
}

func (e *Engine) upgradeAlert(cs *crewState, oa *openAlert, f Finding, now int64) {
	a := &oa.a
	a.Level = levelName(f.Level)
	a.Voice = voice(f.Code, f.Level, cs.latest)
	a.Protocol = protocol(f.Code, f.Level)
	a.Response, a.RespondedBy, a.RespondedAt, a.ResponseS = "PENDING", "", nil, nil // a more serious level needs a new confirmation
	oa.notified = now
	_ = e.st.UpdateAlert(*a)
	e.st.Chain("ALERT_UPGRADED", cs.m.ID, strconv.FormatInt(a.ID, 10), map[string]any{"level": a.Level, "reason": a.Reason})
	e.console(map[string]any{"type": "alert", "alert": *a})
	if f.Level >= 2 {
		e.device(cs.m.ID, map[string]any{"type": "alert", "alert": *a})
	}
	if f.Level >= 3 {
		id := a.ID
		e.openIncident(cs, "EMERGENCY", &id, now)
	}
}

func (e *Engine) resolveAlert(oa *openAlert, now int64) {
	a := &oa.a
	a.ResolvedAt = &now
	_ = e.st.UpdateAlert(*a)
	e.st.Chain("ALERT_RESOLVED", a.CrewID, strconv.FormatInt(a.ID, 10), map[string]any{"code": a.Code, "durationS": (now - a.T) / 1000})
	e.console(map[string]any{"type": "alert-update", "alert": *a})
	e.device(a.CrewID, map[string]any{"type": "alert-update", "alert": *a})
}

// Ack records a response to an alert: by "crew" (from the phone) or
// "operator" (from the console). response: COPIED | IM_OK | NEED_HELP.
func (e *Engine) Ack(id int64, response, by string) (model.Alert, error) {
	switch response {
	case "COPIED", "IM_OK", "NEED_HELP":
	default:
		return model.Alert{}, errors.New("invalid response")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	now := store.Now()
	oa := e.open[id]
	var a model.Alert
	notified := now
	if oa != nil {
		a, notified = oa.a, oa.notified
	} else {
		x, err := e.st.Alert(id)
		if err != nil {
			return model.Alert{}, errors.New("alert not found")
		}
		a = x
		notified = x.T
	}
	a.Response, a.RespondedBy, a.RespondedAt = response, by, &now
	rs := math.Round(float64(now-notified)/100) / 10
	a.ResponseS = &rs
	if oa != nil {
		oa.a = a
	}
	_ = e.st.UpdateAlert(a)
	e.st.Chain("ALERT_ACK", a.CrewID, strconv.FormatInt(id, 10), map[string]any{"response": response, "by": by, "responseS": rs})
	actor := "operator"
	if by == "crew" {
		actor = "crew:" + a.CrewID
	}
	e.log(actor, a.CrewID, "ACK", fmt.Sprintf("%s · %s (%.1f s)", strings.ReplaceAll(response, "_", " "), a.Code, rs))
	cs := e.cs[a.CrewID]
	if cs != nil {
		if response == "NEED_HELP" {
			cs.help = now
			e.devicesExcept(a.CrewID, map[string]any{"type": "buddy", "alertId": a.ID, "crewId": a.CrewID, "role": cs.m.Role, "name": cs.m.Name,
				"voice": buddyVoice(cs.m.Role, "CREW_HELP"), "reason": "Crew member answered “Need help”", "level": 3})
		} else if by == "operator" {
			cs.help = 0
		}
	}
	e.console(map[string]any{"type": "alert-update", "alert": a})
	e.device(a.CrewID, map[string]any{"type": "alert-update", "alert": a})
	if cs != nil {
		e.evaluate(cs, now, false)
	}
	return a, nil
}

// ClearHelp marks a "Need help" request as handled by the operator.
func (e *Engine) ClearHelp(crewID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if cs := e.cs[crewID]; cs != nil && cs.help > 0 {
		cs.help = 0
		e.log("operator", crewID, "HELP_HANDLED", "Help request handled by the operator")
		e.evaluate(cs, store.Now(), false)
	}
}

/* =====================================================================
   Timers (called every second)
   ===================================================================== */

// Tick runs the 1 Hz housekeeping: link loss, 30 s escalation, black-box
// sealing and the console / device broadcasts.
func (e *Engine) Tick() {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := store.Now()
	changed := map[string]bool{}
	for _, oa := range e.open {
		a := &oa.a
		if a.LevelNum() < 2 || a.Response != "PENDING" || a.Escalated || now-oa.notified < escalateMs {
			continue
		}
		a.Escalated = true
		a.EscalatedAt = &now
		_ = e.st.UpdateAlert(*a)
		e.st.Chain("ALERT_ESCALATED", a.CrewID, strconv.FormatInt(a.ID, 10), map[string]any{"afterS": (now - oa.notified) / 1000})
		cs := e.cs[a.CrewID]
		role, name := a.CrewID, a.CrewID
		if cs != nil {
			role, name = cs.m.Role, cs.m.Name
		}
		e.log("system", a.CrewID, "ESCALATE", fmt.Sprintf("%s not acknowledged in 30 s · relayed to the other explorers", a.Code))
		e.devicesExcept(a.CrewID, map[string]any{"type": "buddy", "alertId": a.ID, "crewId": a.CrewID, "role": role, "name": name,
			"voice": buddyVoice(role, a.Code), "reason": a.Reason, "level": 3})
		e.console(map[string]any{"type": "alert-update", "alert": *a})
		changed[a.CrewID] = true
	}
	for _, id := range e.order {
		cs := e.cs[id]
		linkLost := e.mission.Status == "ACTIVE" && cs.m.IsEVA() && cs.lastT > 0 && now-cs.lastT > linkLostMs
		hadLink := false
		for _, f := range cs.eval.Findings {
			if f.Code == "LINK_LOST" {
				hadLink = true
			}
		}
		if changed[id] || linkLost != hadLink || now-cs.eval.Updated > 5000 {
			e.evaluate(cs, now, false)
		}
	}
	for id, inc := range e.recording {
		if now >= inc.WindowEnd {
			e.seal(inc, now)
			delete(e.recording, id)
		}
	}
	e.console(map[string]any{"type": "tick", "t": now, "mission": e.mission, "crew": e.crewViews(false)})
	e.devicesExcept("", map[string]any{"type": "crew", "t": now, "mission": e.mission, "crew": e.deviceCrew()})
}

/* =====================================================================
   Missions
   ===================================================================== */

// StartMission switches the planned EVA to ACTIVE.
func (e *Engine) StartMission() (model.Mission, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.mission.Status != "PLANNED" {
		return e.mission, errors.New("there is no planned EVA to start")
	}
	now := store.Now()
	if err := e.st.SetMissionStatus(e.mission.ID, "ACTIVE", &now, nil); err != nil {
		return e.mission, err
	}
	e.mission.Status, e.mission.StartedAt = "ACTIVE", &now
	for _, cs := range e.cs {
		cs.dose = 0
	}
	e.st.Chain("MISSION_START", "", e.mission.ID, map[string]any{"name": e.mission.Name})
	e.log("operator", "", "START", "EVA started: "+e.mission.Name)
	e.broadcastMission()
	return e.mission, nil
}

// EndMission completes (abort=false) or aborts (abort=true) the active EVA.
func (e *Engine) EndMission(abort bool, reason string) (model.Mission, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.mission.Status != "ACTIVE" {
		return e.mission, errors.New("there is no active EVA")
	}
	now := store.Now()
	status := "COMPLETED"
	if abort {
		status = "ABORTED"
		if reason == "" {
			reason = "Operator decision"
		}
		for _, id := range e.order {
			if cs := e.cs[id]; cs.m.IsEVA() {
				e.openIncident(cs, "ABORT", nil, now)
			}
		}
		e.devicesExcept("", map[string]any{"type": "abort", "missionId": e.mission.ID, "reason": reason, "voice": "EVA aborted. Return to airlock now."})
		e.st.Chain("MISSION_ABORT", "", e.mission.ID, map[string]any{"reason": reason})
		e.log("operator", "", "ABORT", "EVA ABORTED · "+reason)
	} else {
		e.st.Chain("MISSION_END", "", e.mission.ID, map[string]any{})
		e.log("operator", "", "END", "EVA completed: "+e.mission.Name)
	}
	if err := e.st.SetMissionStatus(e.mission.ID, status, nil, &now); err != nil {
		return e.mission, err
	}
	e.mission.Status, e.mission.EndedAt = status, &now
	e.st.Flush()
	for _, id := range e.order {
		cs := e.cs[id]
		if !cs.m.IsEVA() {
			continue
		}
		rows, _ := e.st.MissionTelemetry(e.mission.ID, id)
		alerts, _ := e.st.Alerts(e.mission.ID, false, 1000)
		n := 0
		for _, a := range alerts {
			if a.CrewID == id {
				n++
			}
		}
		x := ComputeSummary(e.mission.ID, cs.m, cs.b, rows, n, now)
		if x.Samples == 0 {
			continue
		}
		_ = e.st.SaveSummary(x)
		_ = e.st.AddRadiation(id, x.DoseMSv)
		cs.m.RadAccum += x.DoseMSv
		cs.recent = &x
		cs.vulnAt = 0
	}
	e.broadcastMission()
	return e.mission, nil
}

var missionNum = regexp.MustCompile(`^(.*?)(\d+)$`)

// NewMission plans the next EVA after the current one ended.
func (e *Engine) NewMission() (model.Mission, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.mission.Status == "ACTIVE" || e.mission.Status == "PLANNED" {
		return e.mission, errors.New("finish the current EVA first")
	}
	m := model.Mission{ID: "SELENE3-EVA01", Name: "SELENE-3 · EVA 1", Environment: "Moon", Site: "Shackleton crater · south pole", PlannedH: 6, Status: "PLANNED"}
	if e.mission.ID != "" {
		m.Environment, m.Site, m.PlannedH = e.mission.Environment, e.mission.Site, e.mission.PlannedH
		if g := missionNum.FindStringSubmatch(e.mission.ID); g != nil {
			n, _ := strconv.Atoi(g[2])
			m.ID = fmt.Sprintf("%s%0*d", g[1], len(g[2]), n+1)
			name := e.mission.Name
			if k := strings.LastIndex(name, " "); k > 0 {
				name = name[:k]
			}
			m.Name = fmt.Sprintf("%s %d", name, n+1)
		}
	}
	if err := e.st.InsertMission(m); err != nil {
		return e.mission, err
	}
	e.mission = m
	e.log("operator", "", "PLAN", "EVA planned: "+m.Name)
	e.broadcastMission()
	return m, nil
}

func (e *Engine) broadcastMission() {
	msg := map[string]any{"type": "mission", "mission": e.mission}
	e.console(msg)
	e.devicesExcept("", msg)
}

/* =====================================================================
   Operator actions and device events
   ===================================================================== */

// Instruction sends a short text instruction to one explorer ("" = all).
func (e *Engine) Instruction(crewID, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return errors.New("empty instruction")
	}
	if len([]rune(text)) > 140 {
		text = string([]rune(text)[:140])
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if crewID != "" && e.cs[crewID] == nil {
		return errors.New("unknown crew member")
	}
	now := store.Now()
	msg := map[string]any{"type": "instruction", "t": now, "text": text, "from": "EVA Station", "crewId": crewID}
	if crewID == "" {
		e.devicesExcept("", msg)
	} else {
		e.device(crewID, msg)
	}
	e.st.Chain("INSTRUCTION", crewID, "", map[string]any{"text": text})
	target := "all explorers"
	if crewID != "" {
		target = e.cs[crewID].m.Role + " " + e.cs[crewID].m.Name
	}
	e.log("operator", crewID, "INSTRUCTION", "To "+target+": "+text)
	return nil
}

// ReportIncident opens a manual black-box incident.
func (e *Engine) ReportIncident(crewID, actor string) (model.Incident, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	cs := e.cs[crewID]
	if cs == nil {
		return model.Incident{}, errors.New("unknown crew member")
	}
	inc := e.openIncident(cs, "MANUAL", nil, store.Now())
	e.log(actor, crewID, "REPORT_INCIDENT", "Incident reported manually")
	if inc == nil {
		return model.Incident{}, errors.New("could not open the incident")
	}
	return *inc, nil
}

// SetReadiness stores the daily privacy-filtered verdict sent by a phone.
func (e *Engine) SetReadiness(r model.Readiness) (model.Readiness, error) {
	switch r.Status {
	case "GREEN", "YELLOW", "RED":
	default:
		return r, errors.New("status must be GREEN, YELLOW or RED")
	}
	if _, err := time.Parse("2006-01-02", r.Day); err != nil {
		r.Day = today()
	}
	r.Vulnerability = clampInt(r.Vulnerability, 0, 100)
	if len(r.Summary) > 200 {
		r.Summary = r.Summary[:200]
	}
	r.ReceivedAt = store.Now()
	e.mu.Lock()
	defer e.mu.Unlock()
	cs := e.cs[r.CrewID]
	if cs == nil {
		return r, errors.New("unknown crew member")
	}
	if err := e.st.SaveReadiness(r); err != nil {
		return r, err
	}
	e.loadReadiness(cs)
	e.updateBaseline(cs)
	now := store.Now()
	e.computeVuln(cs, now)
	e.evaluate(cs, now, false)
	e.st.Chain("READINESS", r.CrewID, r.Day, map[string]any{"status": r.Status, "vulnerability": r.Vulnerability})
	e.log("crew:"+r.CrewID, r.CrewID, "READINESS", fmt.Sprintf("%s · vulnerability %d/100 · %s", r.Status, r.Vulnerability, r.Summary))
	e.console(map[string]any{"type": "readiness", "crewId": r.CrewID, "readiness": r})
	return r, nil
}

// DeviceOnline registers a phone for a crew member. The phone replaces the
// virtual suit of that astronaut.
func (e *Engine) DeviceOnline(crewID, name string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	cs := e.cs[crewID]
	if cs == nil {
		return false
	}
	if e.vs != nil && e.vs.Enabled(crewID) {
		e.vs.SetEnabled(crewID, false)
	}
	cs.devOnline, cs.devName, cs.devSince = true, name, store.Now()
	e.st.Chain("DEVICE_LINK", crewID, "", map[string]any{"device": name})
	e.log("crew:"+crewID, crewID, "LINK", "Phone linked: "+name)
	return true
}

// DeviceOffline marks the phone of a crew member as disconnected.
func (e *Engine) DeviceOffline(crewID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	cs := e.cs[crewID]
	if cs == nil || !cs.devOnline {
		return
	}
	cs.devOnline = false
	e.st.Chain("DEVICE_UNLINK", crewID, "", map[string]any{})
	e.log("system", crewID, "UNLINK", "Phone disconnected")
}

// SetQueued records how many readings the phone still has to upload.
func (e *Engine) SetQueued(crewID string, n int) {
	e.mu.Lock()
	if cs := e.cs[crewID]; cs != nil {
		cs.queued = n
	}
	e.mu.Unlock()
}

// Welcome returns what a phone needs right after connecting.
func (e *Engine) Welcome(crewID string) map[string]any {
	e.mu.Lock()
	defer e.mu.Unlock()
	var mine []model.Alert
	for _, oa := range e.open {
		if oa.a.CrewID == crewID && oa.a.LevelNum() >= 2 {
			mine = append(mine, oa.a)
		}
	}
	var crew []model.Crew
	for _, id := range e.order {
		crew = append(crew, e.cs[id].m)
	}
	return map[string]any{"type": "welcome", "crewId": crewID, "serverTime": store.Now(), "mission": e.mission, "crew": crew, "alerts": mine}
}

// Known reports whether a crew id exists.
func (e *Engine) Known(crewID string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cs[crewID] != nil
}

// CrewIDs returns the crew ids in display order (EV first).
func (e *Engine) CrewIDs() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.order...)
}

// Member returns one crew member and baseline.
func (e *Engine) Member(crewID string) (model.Crew, model.Baseline, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	cs := e.cs[crewID]
	if cs == nil {
		return model.Crew{}, model.Baseline{}, false
	}
	return cs.m, cs.b, true
}

// Mission returns the current mission.
func (e *Engine) Mission() model.Mission {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.mission
}

// LatestSample returns the latest reading of a crew member (for the simulator).
func (e *Engine) LatestSample(crewID string) *model.Sample {
	e.mu.Lock()
	defer e.mu.Unlock()
	if cs := e.cs[crewID]; cs != nil && cs.latest != nil {
		s := *cs.latest
		return &s
	}
	return nil
}

/* =====================================================================
   Vulnerability (accumulated risk) and personal baseline
   ===================================================================== */

var factorLabels = map[string]Factor{
	"SLEEP_LT6_3D":    {Code: "SLEEP_LT6_3D", Label: "Sleep < 6 h for 3 days", Points: 15},
	"SLEEP_SHORT":     {Code: "SLEEP_SHORT", Label: "Short sleep last night", Points: 8},
	"HYDRATION_LOW":   {Code: "HYDRATION_LOW", Label: "Dehydration risk", Points: 10},
	"FATIGUE_HIGH":    {Code: "FATIGUE_HIGH", Label: "High fatigue reported", Points: 10},
	"BEHAVIORAL_LOAD": {Code: "BEHAVIORAL_LOAD", Label: "Behavioural load elevated (private detail)", Points: 10},
}

func (e *Engine) computeVuln(cs *crewState, now int64) {
	var fs []Factor
	seen := map[string]bool{}
	add := func(f Factor) {
		if !seen[f.Code] {
			seen[f.Code] = true
			fs = append(fs, f)
		}
	}
	if r := cs.today; r != nil {
		switch r.Status {
		case "RED":
			add(Factor{"READINESS_RED", "Readiness RED · private medical review", 30})
		case "YELLOW":
			add(Factor{"READINESS_YELLOW", "Readiness: with reservations", 12})
		}
		if r.Vulnerability > 0 {
			add(Factor{"DEVICE_VULN", fmt.Sprintf("Pre-mission vulnerability %d/100", r.Vulnerability), r.Vulnerability / 3})
		}
		if r.FatigueHigh {
			add(factorLabels["FATIGUE_HIGH"])
		}
		if r.Hydration != nil && *r.Hydration >= 6 {
			f := factorLabels["HYDRATION_LOW"]
			f.Label = fmt.Sprintf("Dehydration risk (urine scale %d/8)", *r.Hydration)
			add(f)
		}
	}
	// Sleep < 6 h three days in a row (server-side check of the history)
	if len(cs.hist) >= 3 {
		short := 0
		for _, r := range cs.hist[:3] {
			if r.SleepH != nil && *r.SleepH < 6 {
				short++
			}
		}
		if short == 3 {
			add(factorLabels["SLEEP_LT6_3D"])
		}
	}
	if r := cs.today; r != nil {
		for _, code := range r.Factors {
			if f, ok := factorLabels[code]; ok {
				add(f)
			}
		}
	}
	// Baseline deviation of morning resting HR (yellow: |z|>1 for 3 days or
	// |z|>2 for one day · red: |z|>2 for 2 days in a row)
	if cs.b.HRRestSD > 0 && len(cs.hist) >= 1 {
		var z []float64
		for _, r := range cs.hist {
			if len(z) == 3 {
				break
			}
			if r.HRRest != nil {
				z = append(z, (*r.HRRest-cs.b.HRRest)/cs.b.HRRestSD)
			}
		}
		over := func(n int, lim float64) bool {
			if len(z) < n {
				return false
			}
			for _, v := range z[:n] {
				if math.Abs(v) <= lim {
					return false
				}
			}
			return true
		}
		switch {
		case over(2, 2):
			add(Factor{"BASELINE_DEV_RED", fmt.Sprintf("Resting HR %+.1f SD vs baseline for 2 days", z[0]), 25})
		case over(3, 1) || over(1, 2):
			add(Factor{"BASELINE_DEV", fmt.Sprintf("Resting HR %+.1f SD vs personal baseline", z[0]), 12})
		}
	}
	// HRV drop > 20 % over the last 10 minutes of telemetry
	if cs.b.HRV > 0 && len(cs.ring) > 60 {
		sum, n := 0.0, 0
		for i := len(cs.ring) - 1; i >= 0 && cs.ring[i].T > now-600000; i-- {
			if !math.IsNaN(cs.ring[i].HRV) {
				sum += cs.ring[i].HRV
				n++
			}
		}
		if n > 60 {
			if mean := sum / float64(n); mean < 0.8*cs.b.HRV {
				add(Factor{"HRV_DOWN", fmt.Sprintf("HRV −%.0f %% vs baseline (10 min)", (1-mean/cs.b.HRV)*100), 10})
			}
		}
	}
	// Career radiation over 50 % of the budget
	if cs.m.RadBudget > 0 {
		if pct := (cs.m.RadAccum + cs.dose) / cs.m.RadBudget * 100; pct > 50 {
			add(Factor{"RAD_50", fmt.Sprintf("Career radiation %.0f %% of limit", pct), 10})
		}
	}
	// Recovery after the previous EVA not finished yet
	if x := cs.recent; x != nil {
		if left := float64(x.EndedAt)+x.RecoveryH*3600000-float64(now); left > 0 {
			add(Factor{"RECOVERY", fmt.Sprintf("Recovery after %s incomplete (%.0f h left)", x.MissionID, left/3600000), 10})
		}
	}
	sort.SliceStable(fs, func(i, j int) bool { return fs[i].Points > fs[j].Points })
	// Factors reported by the phone (from the private diary) overlap with
	// the phone's own vulnerability index: count that group once, as the
	// larger of the two, capped at 60. Station-side factors add on top.
	devPts, srvPts := 0, 0
	for _, f := range fs {
		switch f.Code {
		case "READINESS_RED", "READINESS_YELLOW", "SLEEP_LT6_3D", "SLEEP_SHORT", "HYDRATION_LOW", "FATIGUE_HIGH", "BEHAVIORAL_LOAD":
			devPts += f.Points
		case "DEVICE_VULN":
		default:
			srvPts += f.Points
		}
	}
	if r := cs.today; r != nil && r.Vulnerability > devPts {
		devPts = r.Vulnerability
	}
	if devPts > 60 {
		devPts = 60
	}
	cs.factors, cs.vuln, cs.vulnAt = fs, clampInt(devPts+srvPts, 0, 100), now
}

// updateBaseline recomputes the personal baseline from the morning
// readiness values: days 1–14 = adaptation (mean ± SD of all days),
// from day 15 = rolling 7-day mean (SD kept from the adaptation period).
func (e *Engine) updateBaseline(cs *crewState) {
	var hr, hrv, sleep []float64 // oldest first
	for i := len(cs.hist) - 1; i >= 0; i-- {
		r := cs.hist[i]
		if r.HRRest != nil {
			hr = append(hr, *r.HRRest)
		}
		if r.HRVRest != nil {
			hrv = append(hrv, *r.HRVRest)
		}
		if r.SleepH != nil {
			sleep = append(sleep, *r.SleepH)
		}
	}
	if len(hr) < 3 {
		return
	}
	b := cs.b
	b.Days = len(hr)
	if len(hr) < 14 {
		b.Phase = "ADAPTATION_14D"
		b.HRRest, b.HRRestSD = meanSD(hr)
		if len(hrv) >= 3 {
			b.HRV, b.HRVSD = meanSD(hrv)
		}
	} else {
		b.Phase = "ROLLING_7D"
		_, sd := meanSD(hr[:14])
		b.HRRest, _ = meanSD(hr[len(hr)-7:])
		b.HRRestSD = sd
		if len(hrv) >= 14 {
			_, hsd := meanSD(hrv[:14])
			b.HRV, _ = meanSD(hrv[len(hrv)-7:])
			b.HRVSD = hsd
		}
	}
	b.HRRestSD = math.Max(b.HRRestSD, 2)
	b.HRVSD = math.Max(b.HRVSD, 3)
	if len(sleep) >= 3 {
		k := len(sleep)
		if k > 7 {
			k = 7
		}
		b.SleepH, _ = meanSD(sleep[len(sleep)-k:])
	}
	b.UpdatedAt = store.Now()
	cs.b = b
	_ = e.st.SaveBaseline(b)
}

// meanSD uses Welford's algorithm (numerically stable).
func meanSD(xs []float64) (float64, float64) {
	var n, mean, m2 float64
	for _, x := range xs {
		n++
		d := x - mean
		mean += d / n
		m2 += d * (x - mean)
	}
	if n < 2 {
		return mean, 0
	}
	return math.Round(mean*10) / 10, math.Round(math.Sqrt(m2/(n-1))*100) / 100
}

/* =====================================================================
   Views for the console and the phones
   ===================================================================== */

// DeviceView is the link state of an astronaut's phone.
type DeviceView struct {
	Online  bool   `json:"online"`
	Name    string `json:"name,omitempty"`
	Since   int64  `json:"since,omitempty"`
	Source  string `json:"source"` // device | virtual | none
	LastT   int64  `json:"lastT"`
	Queued  int    `json:"queued"`
	Virtual bool   `json:"virtual"`
}

// CrewView is one astronaut as shown on the console.
type CrewView struct {
	model.Crew
	EVA       bool              `json:"eva"`
	Baseline  model.Baseline    `json:"baseline"`
	Eval      Evaluation        `json:"eval"`
	Vitals    *model.Sample     `json:"vitals,omitempty"`
	Factors   []Factor          `json:"factors"`
	Vuln      int               `json:"vuln"`
	Dose      float64           `json:"dose"`
	Device    DeviceView        `json:"device"`
	Readiness *model.Readiness  `json:"readiness,omitempty"`
	LastEVA   *model.EVASummary `json:"lastEva,omitempty"`
	Help      bool              `json:"help"`
}

func (e *Engine) crewViews(full bool) []CrewView {
	out := make([]CrewView, 0, len(e.order))
	for _, id := range e.order {
		cs := e.cs[id]
		v := CrewView{Crew: cs.m, EVA: cs.m.IsEVA(), Baseline: cs.b, Eval: cs.eval, Vitals: cs.latest, Factors: cs.factors, Vuln: cs.vuln,
			Dose: math.Round(cs.dose*1000) / 1000, Readiness: cs.today, LastEVA: cs.recent, Help: cs.help > 0}
		v.Device = DeviceView{Online: cs.devOnline, Name: cs.devName, Since: cs.devSince, LastT: cs.lastT, Queued: cs.queued, Source: "none"}
		if e.vs != nil {
			v.Device.Virtual = e.vs.Enabled(id)
		}
		switch {
		case cs.devOnline:
			v.Device.Source = "device"
		case v.Device.Virtual:
			v.Device.Source = "virtual"
		}
		if v.Factors == nil {
			v.Factors = []Factor{}
		}
		out = append(out, v)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.EVA != b.EVA {
			return a.EVA
		}
		an, bn := a.Eval.Status == "NO_DATA", b.Eval.Status == "NO_DATA"
		if an != bn {
			return !an
		}
		return a.Eval.IPV > b.Eval.IPV
	})
	return out
}

// deviceCrew is the compact status sent to the phones every second.
func (e *Engine) deviceCrew() []map[string]any {
	out := make([]map[string]any, 0, len(e.order))
	for _, id := range e.order {
		cs := e.cs[id]
		out = append(out, map[string]any{"id": id, "role": cs.m.Role, "name": cs.m.Name, "ipv": cs.eval.IPV, "status": cs.eval.Status,
			"level": cs.eval.Level, "diagnosis": cs.eval.Diagnosis, "vitals": cs.latest, "lastT": cs.lastT, "online": cs.devOnline})
	}
	return out
}

// Trend is the 15-minute history of one astronaut (one point every 5 s).
type Trend struct {
	T    []int64    `json:"t"`
	HR   []*float64 `json:"hr"`
	SpO2 []*float64 `json:"spo2"`
	Temp []*float64 `json:"temp"`
	RR   []*float64 `json:"rr"`
	IPV  []int      `json:"ipv"`
}

func ptr(v float64) *float64 {
	if math.IsNaN(v) {
		return nil
	}
	x := math.Round(v*10) / 10
	return &x
}

func (e *Engine) trend(cs *crewState, now int64) Trend {
	t := Trend{T: []int64{}, HR: []*float64{}, SpO2: []*float64{}, Temp: []*float64{}, RR: []*float64{}, IPV: []int{}}
	var bucket int64 = -1
	for _, p := range cs.ring {
		if p.T < now-trendSeconds*1000 {
			continue
		}
		if b := p.T / 5000; b != bucket {
			bucket = b
			t.T = append(t.T, p.T)
			t.HR = append(t.HR, ptr(p.HR))
			t.SpO2 = append(t.SpO2, ptr(p.SpO2))
			t.Temp = append(t.Temp, ptr(p.Temp))
			t.RR = append(t.RR, ptr(p.RR))
			t.IPV = append(t.IPV, p.IPV)
		}
	}
	return t
}

// Snapshot is the full console state (sent on connect and by /api/state).
func (e *Engine) Snapshot() map[string]any {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := store.Now()
	trends := map[string]Trend{}
	for _, id := range e.order {
		trends[id] = e.trend(e.cs[id], now)
	}
	openAlerts := make([]model.Alert, 0, len(e.open))
	for _, oa := range e.open {
		openAlerts = append(openAlerts, oa.a)
	}
	sort.Slice(openAlerts, func(i, j int) bool { return openAlerts[i].T > openAlerts[j].T })
	recent, _ := e.st.Alerts("", false, 60)
	incidents, _ := e.st.Incidents("", 40)
	oplog, _ := e.st.OpLogs("", 0, 0, 60)
	missions, _ := e.st.Missions(12)
	return map[string]any{
		"type": "snapshot", "t": now, "startedAt": e.Started, "mission": e.mission, "missions": missions,
		"crew": e.crewViews(true), "trends": trends, "openAlerts": openAlerts, "alerts": recent, "incidents": incidents, "oplog": oplog,
	}
}
