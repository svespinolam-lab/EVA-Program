package engine

// reports.go — deterministic report data (no machine learning):
//   * Post-EVA mission report: duration, crew ranked by physiological
//     load, Banister TRIMP, Keytel energy expenditure, radiation dose vs
//     career limit, alerts and incidents. It also sets the next-day
//     recovery estimate (used by the vulnerability factors).
//   * Monthly health report per astronaut: 30-day trends vs baseline and
//     the periodic clinical panel with automatic flags.

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"eva/internal/model"
	"eva/internal/store"
)

// ComputeSummary derives the post-EVA physiological load of one astronaut.
func ComputeSummary(missionID string, c model.Crew, b model.Baseline, rows []store.TelRow, alerts int, endedAt int64) model.EVASummary {
	x := model.EVASummary{MissionID: missionID, CrewID: c.ID, Samples: len(rows), Alerts: alerts, EndedAt: endedAt, SpO2Min: 100}
	if len(rows) == 0 {
		x.SpO2Min = 0
		return x
	}
	hrRest := b.HRRest
	if hrRest <= 0 {
		hrRest = 65
	}
	hrMax := hrMaxOf(c.Age)
	k, y := 0.86, 1.67 // Banister TRIMP weighting, women
	if c.Sex == "M" {
		k, y = 0.64, 1.92
	}
	var sumHR, nHR float64
	var riskS float64
	for i, r := range rows {
		dt := 1.0
		if i > 0 {
			dt = math.Min(math.Max(float64(r.T-rows[i-1].T)/1000, 0), 5)
		}
		if r.HR != nil {
			hr := *r.HR
			sumHR += hr
			nHR++
			x.HRMax = math.Max(x.HRMax, hr)
			hrr := math.Max(0, math.Min(1, (hr-hrRest)/(hrMax-hrRest)))
			x.TRIMP += dt / 60 * hrr * k * math.Exp(y*hrr)
			// Keytel et al. 2005, kJ/min → kcal/min (floor at resting expenditure)
			var kj float64
			if c.Sex == "M" {
				kj = -55.0969 + 0.6309*hr + 0.1988*c.WeightKg + 0.2017*float64(c.Age)
			} else {
				kj = -20.4022 + 0.4472*hr - 0.1263*c.WeightKg + 0.074*float64(c.Age)
			}
			x.Kcal += math.Max(kj/4.184, 1.2) * dt / 60
		}
		if r.SpO2 != nil {
			x.SpO2Min = math.Min(x.SpO2Min, *r.SpO2)
		}
		if r.Temp != nil {
			x.TempMax = math.Max(x.TempMax, *r.Temp)
		}
		if r.Rad != nil {
			x.DoseMSv += *r.Rad * dt / 3600
		}
		if r.IPV >= 70 {
			riskS += dt
		}
		if r.IPV > x.PeakIPV {
			x.PeakIPV = r.IPV
		}
	}
	if nHR > 0 {
		x.HRMean = math.Round(sumHR/nHR*10) / 10
	}
	x.DurationS = (rows[len(rows)-1].T - rows[0].T) / 1000
	x.RiskS = int64(riskS)
	x.TRIMP = math.Round(x.TRIMP*10) / 10
	x.Kcal = math.Round(x.Kcal)
	x.DoseMSv = math.Round(x.DoseMSv*1000) / 1000
	// Recovery estimate: 8 h base + training load + severity of the worst event
	rec := 8 + x.TRIMP/12
	switch {
	case x.PeakIPV >= 90:
		rec += 6
	case x.PeakIPV >= 70:
		rec += 3
	}
	x.RecoveryH = math.Round(math.Max(8, math.Min(36, rec))*10) / 10
	return x
}

// CrewLoad is one row of the mission report.
type CrewLoad struct {
	Crew      model.Crew
	S         model.EVASummary
	DosePct   float64 // EVA dose as % of the career limit
	CareerPct float64 // career dose after this EVA
}

// MissionReport is the post-EVA summary and black-box index.
type MissionReport struct {
	Mission   model.Mission
	Generated int64
	DurationS int64
	Live      bool
	Crew      []CrewLoad
	Alerts    []model.Alert
	Incidents []model.Incident
	OpLog     []model.OpLog
}

// MissionReport builds the post-EVA report (live if the EVA is still active).
func (e *Engine) MissionReport(id string) (MissionReport, error) {
	m, err := e.st.Mission(id)
	if err != nil {
		return MissionReport{}, errors.New("mission not found")
	}
	e.st.Flush()
	now := store.Now()
	rep := MissionReport{Mission: m, Generated: now, Live: m.Status == "ACTIVE"}
	if m.StartedAt != nil {
		end := now
		if m.EndedAt != nil {
			end = *m.EndedAt
		}
		rep.DurationS = (end - *m.StartedAt) / 1000
	}
	rep.Alerts, _ = e.st.Alerts(id, false, 1000)
	all, _ := e.st.Incidents("", 500)
	for _, inc := range all {
		if inc.MissionID == id {
			rep.Incidents = append(rep.Incidents, inc)
		}
	}
	logs, _ := e.st.OpLogs("", 0, 0, 2000)
	for _, l := range logs {
		if l.MissionID == id {
			rep.OpLog = append(rep.OpLog, l)
		}
	}
	saved := map[string]model.EVASummary{}
	for _, cid := range e.CrewIDs() {
		if xs, err := e.st.Summaries(cid, 0); err == nil {
			for _, x := range xs {
				if x.MissionID == id {
					saved[cid] = x
				}
			}
		}
	}
	for _, cid := range e.CrewIDs() {
		c, b, _ := e.Member(cid)
		if !c.IsEVA() {
			continue
		}
		x, ok := saved[cid]
		if !ok || rep.Live {
			rows, _ := e.st.MissionTelemetry(id, cid)
			n := 0
			for _, a := range rep.Alerts {
				if a.CrewID == cid {
					n++
				}
			}
			x = ComputeSummary(id, c, b, rows, n, now)
		}
		if x.Samples == 0 && !ok {
			continue
		}
		cl := CrewLoad{Crew: c, S: x}
		if c.RadBudget > 0 {
			cl.DosePct = x.DoseMSv / c.RadBudget * 100
			career := c.RadAccum
			if rep.Live {
				career += x.DoseMSv
			}
			cl.CareerPct = career / c.RadBudget * 100
		}
		rep.Crew = append(rep.Crew, cl)
	}
	sort.SliceStable(rep.Crew, func(i, j int) bool {
		if rep.Crew[i].S.PeakIPV != rep.Crew[j].S.PeakIPV {
			return rep.Crew[i].S.PeakIPV > rep.Crew[j].S.PeakIPV
		}
		return rep.Crew[i].S.TRIMP > rep.Crew[j].S.TRIMP
	})
	return rep, nil
}

// Flag is one automatic finding of the monthly report.
type Flag struct {
	Level  int
	Label  string
	Detail string
}

// MonthlyReport is the monthly health report of one astronaut.
type MonthlyReport struct {
	Crew      model.Crew
	Baseline  model.Baseline
	Month     string
	Months    []string
	Cur       *model.Monthly
	Prev      *model.Monthly
	Flags     []Flag
	EVAs      []model.EVASummary
	SleepAvg  *float64
	Readiness []model.Readiness
	CareerPct float64
	Generated int64
}

// MonthlyReport builds the monthly report (month "" = latest available).
func (e *Engine) MonthlyReport(crewID, month string) (MonthlyReport, error) {
	c, b, ok := e.Member(crewID)
	if !ok {
		return MonthlyReport{}, errors.New("crew member not found")
	}
	rep := MonthlyReport{Crew: c, Baseline: b, Generated: store.Now()}
	rep.Months, _ = e.st.Months(crewID)
	if month == "" {
		if len(rep.Months) > 0 {
			month = rep.Months[0]
		} else {
			month = time.Now().Format("2006-01")
		}
	}
	rep.Month = month
	if m, err := e.st.Monthly(crewID, month); err == nil {
		rep.Cur = &m
	}
	if t, err := time.Parse("2006-01", month); err == nil {
		if m, err := e.st.Monthly(crewID, t.AddDate(0, -1, 0).Format("2006-01")); err == nil {
			rep.Prev = &m
		}
		from := t.UnixMilli()
		to := t.AddDate(0, 1, 0).UnixMilli()
		if xs, err := e.st.Summaries(crewID, from); err == nil {
			for _, x := range xs {
				if x.EndedAt < to {
					rep.EVAs = append(rep.EVAs, x)
				}
			}
		}
		if rs, err := e.st.Readiness(crewID, 62); err == nil {
			sum, n := 0.0, 0
			for _, r := range rs {
				if strings.HasPrefix(r.Day, month) {
					rep.Readiness = append(rep.Readiness, r)
					if r.SleepH != nil {
						sum += *r.SleepH
						n++
					}
				}
			}
			if n > 0 {
				v := math.Round(sum/float64(n)*10) / 10
				rep.SleepAvg = &v
			}
		}
	}
	if c.RadBudget > 0 {
		rep.CareerPct = c.RadAccum / c.RadBudget * 100
	}
	rep.Flags = monthlyFlags(c, b, rep.Cur, rep.SleepAvg, rep.CareerPct)
	return rep, nil
}

func monthlyFlags(c model.Crew, b model.Baseline, m *model.Monthly, sleepAvg *float64, careerPct float64) []Flag {
	var fs []Flag
	add := func(l int, label, detail string) { fs = append(fs, Flag{l, label, detail}) }
	if m != nil {
		if m.Hemoglobin != nil {
			lim := 12.0
			if c.Sex == "M" {
				lim = 13.5
			}
			if *m.Hemoglobin < lim {
				add(2, "Anemia risk", fmt.Sprintf("Hemoglobin %.1f g/dL (limit %.1f for %s)", *m.Hemoglobin, lim, map[string]string{"F": "women", "M": "men"}[c.Sex]))
			}
		}
		if m.WeightChangePct != nil {
			switch w := *m.WeightChangePct; {
			case w <= -5:
				add(2, "Body-mass loss > 5 % in 30 days", fmt.Sprintf("Weight change %.1f %%", w))
			case w <= -2:
				add(1, "Weight loss to observe", fmt.Sprintf("Weight change %.1f %% (2–5 %%)", w))
			}
		}
		if m.OrthostaticDrop != nil && *m.OrthostaticDrop >= 20 {
			add(2, "Orthostatic intolerance", fmt.Sprintf("Systolic drop on standing %.0f mmHg (≥ 20)", *m.OrthostaticDrop))
		}
		if m.Glucose != nil {
			switch g := *m.Glucose; {
			case g >= 126:
				add(2, "Fasting glucose in diabetic range", fmt.Sprintf("%.0f mg/dL (≥ 126)", g))
			case g >= 100:
				add(1, "Impaired fasting glucose", fmt.Sprintf("%.0f mg/dL (100–125)", g))
			}
		}
		if m.WBC != nil && (*m.WBC < 4000 || *m.WBC > 11000) {
			add(1, "White blood cells out of range", fmt.Sprintf("%.0f /µL (4 000–11 000)", *m.WBC))
		}
		if m.Platelets != nil && (*m.Platelets < 150000 || *m.Platelets > 450000) {
			add(1, "Platelets out of range", fmt.Sprintf("%.0f /µL (150 000–450 000)", *m.Platelets))
		}
		if m.ExercisePct != nil && *m.ExercisePct < 80 {
			add(1, "Countermeasure exercise below target", fmt.Sprintf("Resistance exercise compliance %.0f %% (target ≥ 80 %%)", *m.ExercisePct))
		}
		if m.AvgHR != nil && b.HRRest > 0 && *m.AvgHR > b.HRRest+8 {
			add(1, "Resting heart rate above baseline", fmt.Sprintf("%.1f bpm vs baseline %.0f", *m.AvgHR, b.HRRest))
		}
		if m.AvgHRV != nil && b.HRV > 0 && *m.AvgHRV < 0.8*b.HRV {
			add(1, "HRV below baseline", fmt.Sprintf("%.0f ms vs baseline %.0f (−%.0f %%)", *m.AvgHRV, b.HRV, (1-*m.AvgHRV/b.HRV)*100))
		}
	}
	sl := sleepAvg
	if sl == nil && m != nil {
		sl = m.AvgSleepH
	}
	if sl != nil && *sl < 6.5 {
		add(1, "Insufficient sleep", fmt.Sprintf("Average %.1f h per night (target ≥ 7 h)", *sl))
	}
	switch {
	case careerPct > 80:
		add(2, "Career radiation above 80 %", fmt.Sprintf("%.1f %% of the 600 mSv limit", careerPct))
	case careerPct > 50:
		add(1, "Career radiation above 50 %", fmt.Sprintf("%.1f %% of the 600 mSv limit", careerPct))
	}
	sort.SliceStable(fs, func(i, j int) bool { return fs[i].Level > fs[j].Level })
	return fs
}
