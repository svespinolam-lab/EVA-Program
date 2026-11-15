package engine

// blackbox.go — the biomedical "black box", modelled on an aircraft
// flight data recorder:
//   * it records continuously (every reading goes to the telemetry table
//     and every event to the hash-chained log);
//   * an incident freezes a window from 30 min before to 15 min after the
//     event (emergency alert, EVA abort or manual "Report incident");
//   * the frozen copy is sealed with a SHA-256 digest and linked into the
//     tamper-evident chain, so later edits are detectable;
//   * deterministic rules classify the event as SUDDEN, PROGRESSIVE or
//     SENSOR_FAULT and build a timeline (first deviation → alert → crew
//     response → operator actions → resolution) for the flight surgeon.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"eva/internal/model"
	"eva/internal/store"
)

// Series is a frozen 1 Hz series in column form (compact JSON).
type Series struct {
	T    []int64    `json:"t"`
	HR   []*float64 `json:"hr"`
	SpO2 []*float64 `json:"spo2"`
	Temp []*float64 `json:"temp"`
	RR   []*float64 `json:"rr"`
	IPV  []int      `json:"ipv"`
}

// TimelineEntry is one line of the incident timeline.
type TimelineEntry struct {
	T     int64  `json:"t"`
	Kind  string `json:"kind"`
	Text  string `json:"text"`
	Level int    `json:"level"`
}

// IncidentReport is everything the incident report shows.
type IncidentReport struct {
	Incident    model.Incident
	Crew        model.Crew
	Baseline    model.Baseline
	Mission     *model.Mission
	Alert       *model.Alert
	Series      Series
	Context     []model.Readiness
	Timeline    []TimelineEntry
	Provisional bool // still recording: data up to now
	HashOK      bool
	Generated   int64
}

func (e *Engine) openIncident(cs *crewState, trigger string, alertID *int64, eventT int64) *model.Incident {
	for _, inc := range e.recording {
		// the same emergency escalating again is the same incident
		if inc.CrewID == cs.m.ID && trigger == "EMERGENCY" && inc.Trigger == "EMERGENCY" && eventT-inc.EventT < incidentPost {
			return inc
		}
	}
	inc := model.Incident{MissionID: e.missionIDAt(eventT), CrewID: cs.m.ID, AlertID: alertID, Trigger: trigger,
		EventT: eventT, WindowStart: eventT - incidentPre, WindowEnd: eventT + incidentPost, Status: "RECORDING"}
	id, err := e.st.InsertIncident(inc)
	if err != nil {
		return nil
	}
	inc.ID = id
	e.recording[id] = &inc
	e.st.Chain("INCIDENT_OPENED", cs.m.ID, strconv.FormatInt(id, 10), map[string]any{"trigger": trigger, "eventT": eventT})
	e.log("system", cs.m.ID, "INCIDENT", fmt.Sprintf("Black box: incident #%d opened (%s) · window T−30 min … T+15 min", id, strings.ToLower(trigger)))
	e.console(map[string]any{"type": "incident", "incident": inc})
	return &inc
}

// SealNow freezes a recording incident immediately (operator request);
// the post-event window then contains only the data received so far.
func (e *Engine) SealNow(id int64) (model.Incident, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	inc := e.recording[id]
	if inc == nil {
		return model.Incident{}, errors.New("incident not recording (already sealed or unknown)")
	}
	e.seal(inc, store.Now())
	delete(e.recording, id)
	return *inc, nil
}

func (e *Engine) seal(inc *model.Incident, now int64) {
	rep, ctxJSON, serJSON, tlJSON := e.buildIncident(*inc, now)
	*inc = rep.Incident
	inc.Status = "SEALED"
	inc.SealedAt = &now
	inc.SHA256 = digest(serJSON, tlJSON, ctxJSON)
	if err := e.st.SealIncident(*inc, ctxJSON, serJSON, tlJSON); err != nil {
		return
	}
	e.st.Chain("INCIDENT_SEALED", inc.CrewID, strconv.FormatInt(inc.ID, 10), map[string]any{"sha256": inc.SHA256, "classification": inc.Classification, "samples": inc.SamplesCount})
	e.log("system", inc.CrewID, "SEAL", fmt.Sprintf("Black box: incident #%d sealed · %s · %d readings", inc.ID, inc.Classification, inc.SamplesCount))
	e.console(map[string]any{"type": "incident", "incident": *inc})
}

func digest(parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(h[:])
}

func p64(v *float64) *float64 {
	if v == nil {
		return nil
	}
	x := *v
	return &x
}

// buildIncident gathers the window, context, timeline and classification.
func (e *Engine) buildIncident(inc model.Incident, now int64) (IncidentReport, string, string, string) {
	e.st.Flush()
	end := inc.WindowEnd
	if now < end {
		end = now
	}
	rows, _ := e.st.Window(inc.CrewID, inc.WindowStart, end)
	var ser Series
	ser.T, ser.HR, ser.SpO2, ser.Temp, ser.RR, ser.IPV = []int64{}, []*float64{}, []*float64{}, []*float64{}, []*float64{}, []int{}
	for _, r := range rows {
		ser.T = append(ser.T, r.T)
		ser.HR = append(ser.HR, p64(r.HR))
		ser.SpO2 = append(ser.SpO2, p64(r.SpO2))
		ser.Temp = append(ser.Temp, p64(r.Temp))
		ser.RR = append(ser.RR, p64(r.RR))
		ser.IPV = append(ser.IPV, r.IPV)
	}

	// Context: the 7 days before the event (privacy-filtered readiness only)
	var ctx []model.Readiness
	evDay := time.UnixMilli(inc.EventT).Format("2006-01-02")
	if hist, err := e.st.Readiness(inc.CrewID, 21); err == nil {
		for _, r := range hist {
			if r.Day <= evDay && len(ctx) < 7 {
				ctx = append(ctx, r)
			}
		}
	}

	var alert *model.Alert
	if inc.AlertID != nil {
		if a, err := e.st.Alert(*inc.AlertID); err == nil {
			alert = &a
		}
	}

	// First biometric deviation: walk back from the event while IPV ≥ 40
	var first *int64
	for i := len(rows) - 1; i >= 0; i-- {
		if rows[i].T > inc.EventT {
			continue
		}
		if rows[i].IPV < 40 {
			break
		}
		t := rows[i].T
		first = &t
	}
	inc.FirstDeviation = first
	if first != nil && alert != nil {
		d := float64(alert.T-*first) / 1000
		inc.DetectionS = &d
	}
	if alert != nil {
		inc.ResponseS = alert.ResponseS
	}
	inc.SamplesCount = len(rows)
	inc.Classification = classify(inc, rows, ctx, alert, first)

	// Timeline
	var tl []TimelineEntry
	add := func(t int64, kind, text string, level int) { tl = append(tl, TimelineEntry{T: t, Kind: kind, Text: text, Level: level}) }
	if first != nil {
		add(*first, "DEVIATION", "First biometric deviation (IPV ≥ 40)", 1)
	}
	add(inc.EventT, "EVENT", map[string]string{"EMERGENCY": "Emergency detected", "ABORT": "EVA aborted", "MANUAL": "Incident reported"}[inc.Trigger], 3)
	if alerts, err := e.st.Alerts("", false, 500); err == nil {
		for _, a := range alerts {
			if a.CrewID != inc.CrewID || a.T < inc.WindowStart || a.T > end {
				continue
			}
			add(a.T, "ALERT", fmt.Sprintf("%s alert %s · voice: “%s”", strings.Title(strings.ToLower(a.Level)), a.Code, a.Voice), a.LevelNum())
			if a.RespondedAt != nil {
				who := "Crew"
				if a.RespondedBy == "operator" {
					who = "Operator"
				}
				rs := 0.0
				if a.ResponseS != nil {
					rs = *a.ResponseS
				}
				add(*a.RespondedAt, "RESPONSE", fmt.Sprintf("%s answered %s after %.1f s", who, strings.ReplaceAll(a.Response, "_", " "), rs), 0)
			}
			if a.EscalatedAt != nil {
				add(*a.EscalatedAt, "ESCALATION", "No response in 30 s · relayed to the other explorers", 3)
			}
			if a.ResolvedAt != nil && *a.ResolvedAt <= end {
				add(*a.ResolvedAt, "RESOLVED", a.Code+" back within limits", 0)
			}
		}
	}
	if logs, err := e.st.OpLogs(inc.CrewID, inc.WindowStart, end, 200); err == nil {
		for _, l := range logs {
			switch l.Action {
			case "INSTRUCTION", "ABORT", "START", "END", "REPORT_INCIDENT", "SYNC", "LINK", "UNLINK", "HELP_HANDLED":
				add(l.T, "ACTION", l.Detail, 0)
			}
		}
	}
	sort.SliceStable(tl, func(i, j int) bool { return tl[i].T < tl[j].T })
	if ctx == nil {
		ctx = []model.Readiness{}
	}

	ctxB, _ := json.Marshal(ctx)
	serB, _ := json.Marshal(ser)
	tlB, _ := json.Marshal(tl)
	rep := IncidentReport{Incident: inc, Alert: alert, Series: ser, Context: ctx, Timeline: tl, Provisional: inc.Status != "SEALED", Generated: now}
	return rep, string(ctxB), string(serB), string(tlB)
}

// classify applies the deterministic black-box rules.
func classify(inc model.Incident, rows []store.TelRow, ctx []model.Readiness, alert *model.Alert, first *int64) string {
	// 1. Possible sensor fault: fault code, signal loss, or impossible values near the event
	if alert != nil && (strings.HasPrefix(alert.Code, "SENSOR_") || alert.Code == "LINK_LOST") {
		return "SENSOR_FAULT"
	}
	for _, r := range rows {
		if r.T < inc.EventT-60000 || r.T > inc.EventT+60000 {
			continue
		}
		if (r.HR != nil && (*r.HR < 25 || *r.HR > 240)) || (r.SpO2 != nil && (*r.SpO2 < 50 || *r.SpO2 > 100.5)) || (r.Temp != nil && (*r.Temp < 30 || *r.Temp > 43.5)) {
			return "SENSOR_FAULT"
		}
	}
	// 2. Progressive: previous deterioration (sleep, hydration, readiness) or a slow build-up
	for _, r := range ctx {
		if r.Status != "GREEN" || r.Vulnerability >= 35 || (r.SleepH != nil && *r.SleepH < 6) || (r.Hydration != nil && *r.Hydration >= 6) {
			return "PROGRESSIVE"
		}
	}
	if first != nil && inc.EventT-*first >= 5*60*1000 {
		return "PROGRESSIVE"
	}
	sum, n := 0, 0
	for _, r := range rows {
		if r.T >= inc.EventT-30*60000 && r.T <= inc.EventT-10*60000 {
			sum += r.IPV
			n++
		}
	}
	if n > 60 && sum/n >= 40 {
		return "PROGRESSIVE"
	}
	// 3. Otherwise sudden (no previous deviation)
	return "SUDDEN"
}

// Incident returns the report data of one incident (provisional if still recording).
func (e *Engine) Incident(id int64) (IncidentReport, error) {
	inc, ctxJ, serJ, tlJ, err := e.st.Incident(id)
	if err != nil {
		return IncidentReport{}, errors.New("incident not found")
	}
	var rep IncidentReport
	if inc.Status == "SEALED" {
		rep = IncidentReport{Incident: inc, Generated: store.Now()}
		_ = json.Unmarshal([]byte(ctxJ), &rep.Context)
		_ = json.Unmarshal([]byte(serJ), &rep.Series)
		_ = json.Unmarshal([]byte(tlJ), &rep.Timeline)
		rep.HashOK = digest(serJ, tlJ, ctxJ) == inc.SHA256
		if inc.AlertID != nil {
			if a, err := e.st.Alert(*inc.AlertID); err == nil {
				rep.Alert = &a
			}
		}
	} else {
		e.mu.Lock()
		rep, _, _, _ = e.buildIncident(inc, store.Now())
		e.mu.Unlock()
		rep.Provisional = true
	}
	if c, b, ok := e.Member(inc.CrewID); ok {
		rep.Crew, rep.Baseline = c, b
	}
	if inc.MissionID != "" {
		if m, err := e.st.Mission(inc.MissionID); err == nil {
			rep.Mission = &m
		}
	}
	return rep, nil
}
