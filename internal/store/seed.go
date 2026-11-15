package store

import (
	"fmt"
	"time"

	"eva/internal/model"
)

// SeedIfEmpty loads the demonstration crew, baselines, readiness history,
// missions and monthly panels the first time the database is created.
// The values are synthetic (concept demo) and match the EVA app crew.
func (s *Store) SeedIfEmpty() (bool, error) {
	var n int
	if err := s.R.QueryRow(`SELECT COUNT(*) FROM crew`).Scan(&n); err != nil {
		return false, err
	}
	if n > 0 {
		return false, nil
	}
	now := time.Now()
	ms := now.UnixMilli()

	crew := []model.Crew{
		{ID: "AST-001", Name: "Ana Ruiz", Initials: "AR", Role: "EV1", Specialty: "Planetary geologist", Sex: "F", Age: 38,
			HeightCm: 167, WeightKg: 61, BloodType: "A+", Conditions: "No relevant history", Allergies: "Penicillin",
			Medication: "None", RadBudget: 600, RadAccum: 145},
		{ID: "AST-002", Name: "Luis Ortega", Initials: "LO", Role: "EV2", Specialty: "Systems engineer", Sex: "M", Age: 44,
			HeightCm: 178, WeightKg: 78, BloodType: "O+", Conditions: "Mild hypertension, controlled", Allergies: "None known",
			Medication: "Losartan 25 mg daily", RadBudget: 600, RadAccum: 310},
		{ID: "AST-003", Name: "Marta Vidal", Initials: "MV", Role: "IV", Specialty: "Flight physician", Sex: "F", Age: 41,
			HeightCm: 170, WeightKg: 64, BloodType: "B+", Conditions: "Corrected myopia", Allergies: "None known",
			Medication: "None", RadBudget: 600, RadAccum: 120},
	}
	baselines := []model.Baseline{
		{CrewID: "AST-001", HRRest: 62, HRRestSD: 3.2, HRV: 58, HRVSD: 7, SpO2: 98, SpO2SD: 0.7, Temp: 36.6, TempSD: 0.18, RR: 14, RRSD: 1.6, BPSys: 116, BPDia: 74, SleepH: 7.4},
		{CrewID: "AST-002", HRRest: 68, HRRestSD: 3.6, HRV: 48, HRVSD: 7, SpO2: 97, SpO2SD: 0.8, Temp: 36.7, TempSD: 0.2, RR: 15, RRSD: 1.8, BPSys: 124, BPDia: 80, SleepH: 6.8},
		{CrewID: "AST-003", HRRest: 64, HRRestSD: 3.0, HRV: 62, HRVSD: 8, SpO2: 98, SpO2SD: 0.6, Temp: 36.5, TempSD: 0.17, RR: 13, RRSD: 1.5, BPSys: 112, BPDia: 72, SleepH: 7.2},
	}

	tx, err := s.W.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	for _, c := range crew {
		if _, err := tx.Exec(`INSERT INTO crew (id, name, initials, role, specialty, sex, age, height_cm, weight_kg, blood_type, conditions,
			allergies, medication, radiation_budget_msv, radiation_accum_msv, created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			c.ID, c.Name, c.Initials, c.Role, c.Specialty, c.Sex, c.Age, c.HeightCm, c.WeightKg, c.BloodType, c.Conditions,
			c.Allergies, c.Medication, c.RadBudget, c.RadAccum, ms); err != nil {
			return false, err
		}
	}
	for _, b := range baselines {
		b.Phase, b.Days, b.UpdatedAt = "ROLLING_7D", 21, ms
		if _, err := tx.Exec(`INSERT INTO baseline (crew_id, phase, days, hr_rest_mean, hr_rest_sd, hrv_mean, hrv_sd, spo2_mean, spo2_sd,
			temp_mean, temp_sd, rr_mean, rr_sd, bp_sys, bp_dia, sleep_h, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			b.CrewID, b.Phase, b.Days, b.HRRest, b.HRRestSD, b.HRV, b.HRVSD, b.SpO2, b.SpO2SD, b.Temp, b.TempSD, b.RR, b.RRSD,
			b.BPSys, b.BPDia, b.SleepH, b.UpdatedAt); err != nil {
			return false, err
		}
	}

	// Readiness history: 14 days before today (+ today for crew without a phone in the demo).
	type day struct {
		sleep  float64
		hyd    int
		hr     float64
		hrv    float64
		vuln   int
		status string
		sum    string
		fac    string
		tired  int
	}
	plan := map[string]func(d int) day{
		"AST-001": func(d int) day { // stable
			return day{sleep: 7.2 + 0.1*float64(d%4), hyd: 3, hr: 61 + float64(d%3), hrv: 57 + float64(d%5), vuln: 8, status: "GREEN", sum: "Ready for EVA: baseline parameters optimal.", fac: "[]"}
		},
		"AST-002": func(d int) day { // last 3 nights short sleep → vulnerable
			if d <= 3 {
				return day{sleep: 5.3 + 0.1*float64(d), hyd: 6, hr: 72 + float64(d%2), hrv: 38, vuln: 40, status: "YELLOW",
					sum: "With reservations: 3 nights < 6 h sleep, mild dehydration.", fac: `["SLEEP_LT6_3D","HYDRATION_LOW"]`, tired: 1}
			}
			return day{sleep: 6.6 + 0.1*float64(d%3), hyd: 4, hr: 67 + float64(d%3), hrv: 47 + float64(d%4), vuln: 14, status: "GREEN", sum: "Ready for EVA: baseline parameters optimal.", fac: "[]"}
		},
		"AST-003": func(d int) day {
			return day{sleep: 7.0 + 0.1*float64(d%3), hyd: 3, hr: 63 + float64(d%3), hrv: 61 + float64(d%4), vuln: 6, status: "GREEN", sum: "Ready for EVA: baseline parameters optimal.", fac: "[]"}
		},
	}
	for id, f := range plan {
		for d := 14; d >= 0; d-- {
			if d == 0 && id == "AST-001" {
				continue // today's verdict for EV1 comes from the phone app
			}
			x := f(d)
			dayStr := now.AddDate(0, 0, -d).Format("2006-01-02")
			if _, err := tx.Exec(`INSERT INTO readiness (crew_id, day, status, vulnerability, summary, factors, sleep_h, hydration, fatigue_high,
				hr_rest, hrv_rest, received_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
				id, dayStr, x.status, x.vuln, x.sum, x.fac, x.sleep, x.hyd, x.tired, x.hr, x.hrv, now.AddDate(0, 0, -d).UnixMilli()); err != nil {
				return false, err
			}
		}
	}

	// Missions: a completed EVA 1 two days ago and the planned EVA 2.
	ev1Start := now.AddDate(0, 0, -2).Add(-6 * time.Hour).UnixMilli()
	ev1End := now.AddDate(0, 0, -2).UnixMilli()
	if _, err := tx.Exec(`INSERT INTO missions (id, name, environment, site, planned_h, status, started_at, ended_at, created_at)
		VALUES ('SELENE3-EVA01','SELENE-3 · EVA 1','Moon','Shackleton crater · south pole',6,'COMPLETED',?,?,?)`, ev1Start, ev1End, ev1Start); err != nil {
		return false, err
	}
	if _, err := tx.Exec(`INSERT INTO missions (id, name, environment, site, planned_h, status, created_at)
		VALUES ('SELENE3-EVA02','SELENE-3 · EVA 2','Moon','Shackleton crater · south pole',6,'PLANNED',?)`, ms); err != nil {
		return false, err
	}
	for _, x := range []model.EVASummary{
		{MissionID: "SELENE3-EVA01", CrewID: "AST-001", DurationS: 21600, Samples: 21600, HRMean: 98, HRMax: 141, SpO2Min: 95, TempMax: 37.4, TRIMP: 168, Kcal: 1520, DoseMSv: 0.34, PeakIPV: 46, Alerts: 1, RiskS: 0, RecoveryH: 14, EndedAt: ev1End},
		{MissionID: "SELENE3-EVA01", CrewID: "AST-002", DurationS: 21600, Samples: 21600, HRMean: 109, HRMax: 158, SpO2Min: 93, TempMax: 37.8, TRIMP: 231, Kcal: 2140, DoseMSv: 0.36, PeakIPV: 74, Alerts: 3, RiskS: 240, RecoveryH: 19, EndedAt: ev1End},
	} {
		if _, err := tx.Exec(`INSERT INTO eva_summary (mission_id, crew_id, duration_s, samples, hr_mean, hr_max, spo2_min, temp_max, trimp, kcal,
			dose_msv, peak_ipv, alerts, risk_s, recovery_h, ended_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			x.MissionID, x.CrewID, x.DurationS, x.Samples, x.HRMean, x.HRMax, x.SpO2Min, x.TempMax, x.TRIMP, x.Kcal, x.DoseMSv,
			x.PeakIPV, x.Alerts, x.RiskS, x.RecoveryH, x.EndedAt); err != nil {
			return false, err
		}
	}

	// Monthly clinical panels: previous and current month.
	cur := now.Format("2006-01")
	prev := now.AddDate(0, -1, 0).Format("2006-01")
	type mrow struct {
		id, month                                            string
		hr, spo2, sleep, hrv, hb, wbc, plt, glu, ortho, w, dw, ex, rad float64
		mental                                               int
	}
	for _, r := range []mrow{
		{"AST-001", prev, 64.1, 98.2, 7.5, 58, 13.6, 6100, 245000, 86, 6, 61.0, -0.3, 96, 17.9, 84},
		{"AST-001", cur, 65.2, 98.1, 7.4, 57, 13.4, 6200, 240000, 88, 8, 60.5, -0.8, 95, 18.5, 82},
		{"AST-002", prev, 70.3, 97.1, 6.6, 46, 14.4, 7400, 231000, 98, 14, 76.4, -0.9, 88, 22.8, 71},
		{"AST-002", cur, 74.5, 96.8, 5.6, 39, 14.1, 7800, 225000, 104, 22, 74.0, -3.2, 78, 24.0, 61},
		{"AST-003", prev, 63.2, 98.6, 7.1, 63, 12.3, 5800, 258000, 84, 9, 61.3, -0.6, 94, 15.8, 80},
		{"AST-003", cur, 63.8, 98.5, 6.9, 62, 11.6, 5900, 255000, 85, 10, 58.0, -5.4, 92, 16.2, 78},
	} {
		if _, err := tx.Exec(`INSERT INTO monthly_health (crew_id, month, avg_hr, avg_spo2, avg_sleep_h, avg_hrv, hemoglobin, wbc, platelets,
			glucose_fasting, orthostatic_drop_sys, weight_kg, weight_change_pct, exercise_pct, radiation_month_msv, mental_index)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, r.id, r.month, r.hr, r.spo2, r.sleep, r.hrv, r.hb, r.wbc, r.plt, r.glu, r.ortho,
			r.w, r.dw, r.ex, r.rad, r.mental); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	s.Chain("SYSTEM", "", "", map[string]any{"event": "database created", "crew": len(crew), "at": fmt.Sprint(ms)})
	return true, nil
}
