package engine

// Administration: crew records, mission planning with an assigned crew, and
// the protocol catalogue mirrored to the database.

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"eva/internal/model"
	"eva/internal/store"
)

// syncProtocols mirrors the deterministic protocol rules to the protocols
// table (rows edited by the medical team are left untouched by the store).
func (e *Engine) syncProtocols() {
	for code := range codePriority {
		for level := 1; level <= 3; level++ {
			steps := protocol(code, level)
			if len(steps) == 0 {
				continue
			}
			b, _ := json.Marshal(steps)
			e.st.UpsertBuiltinProtocol(code, level, string(b))
		}
	}
}

// UpsertCrew creates or updates an astronaut record and makes it live in
// the engine immediately (new members start with a default baseline in the
// 14-day adaptation phase).
func (e *Engine) UpsertCrew(c model.Crew) (model.Crew, error) {
	c.ID = strings.ToUpper(strings.TrimSpace(c.ID))
	if err := e.st.UpsertCrew(c); err != nil {
		return model.Crew{}, err
	}
	crew, err := e.st.ListCrew()
	if err != nil {
		return model.Crew{}, err
	}
	var saved model.Crew
	for _, x := range crew {
		if x.ID == c.ID {
			saved = x
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if cs := e.cs[c.ID]; cs != nil {
		cs.m = saved
	} else {
		cs := &crewState{m: saved, conds: map[string]*cond{},
			b: model.Baseline{CrewID: c.ID, Phase: "ADAPTATION_14D", HRRest: 65, HRRestSD: 5, HRV: 50, HRVSD: 8, SpO2: 98, SpO2SD: 1,
				Temp: 36.6, TempSD: 0.2, RR: 14, RRSD: 2, BPSys: 118, BPDia: 76, SleepH: 7.5}}
		e.cs[c.ID] = cs
		e.order = append(e.order, c.ID)
		e.evaluate(cs, store.Now(), false)
	}
	e.st.Chain("CREW_RECORD", c.ID, "", map[string]any{"name": saved.Name, "role": saved.Role})
	e.log("operator", c.ID, "CREW", "Crew record saved: "+saved.Role+" "+saved.Name)
	return saved, nil
}

var missionID = regexp.MustCompile(`^[A-Z0-9][A-Z0-9-]{2,31}$`)

// CreateMission plans a new EVA with its crew (replaces the preset
// NewMission when the operator configures the mission).
func (e *Engine) CreateMission(m model.Mission, crew []store.Assignment) (model.Mission, error) {
	m.ID = strings.ToUpper(strings.TrimSpace(m.ID))
	m.Name = strings.TrimSpace(m.Name)
	switch {
	case !missionID.MatchString(m.ID):
		return model.Mission{}, errors.New("id: 3-32 characters, A-Z 0-9 and dashes (e.g. SELENE3-EVA03)")
	case m.Name == "" || len([]rune(m.Name)) > 80:
		return model.Mission{}, errors.New("name must be 1-80 characters")
	case m.PlannedH <= 0 || m.PlannedH > 12:
		return model.Mission{}, errors.New("planned duration must be 0-12 h")
	case len(crew) == 0:
		return model.Mission{}, errors.New("assign at least one crew member")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.mission.Status == "ACTIVE" || e.mission.Status == "PLANNED" {
		return e.mission, errors.New("finish the current EVA first")
	}
	eva := 0
	for _, a := range crew {
		if e.cs[a.CrewID] == nil {
			return model.Mission{}, errors.New("unknown crew member " + a.CrewID)
		}
		if strings.HasPrefix(a.Role, "EV") {
			eva++
		}
	}
	if eva == 0 {
		return model.Mission{}, errors.New("assign at least one EV (outside) crew member")
	}
	m.Status, m.StartedAt, m.EndedAt = "PLANNED", nil, nil
	if err := e.st.InsertMission(m); err != nil {
		return model.Mission{}, err
	}
	if err := e.st.SetMissionCrew(m.ID, crew); err != nil {
		return model.Mission{}, err
	}
	e.mission = m
	e.st.Chain("MISSION_PLAN", "", m.ID, map[string]any{"name": m.Name, "crew": crew})
	e.log("operator", "", "PLAN", "EVA planned with assigned crew: "+m.Name)
	e.broadcastMission()
	return m, nil
}

// applyMissionCrew gives each assigned astronaut the role of the mission
// being started (called with e.mu held). Unassigned crew keep their role.
func (e *Engine) applyMissionCrew() {
	as, err := e.st.MissionCrew(e.mission.ID)
	if err != nil || len(as) == 0 {
		return
	}
	for _, a := range as {
		cs := e.cs[a.CrewID]
		if cs == nil || cs.m.Role == a.Role {
			continue
		}
		cs.m.Role = a.Role
		_ = e.st.UpsertCrew(cs.m)
	}
}
