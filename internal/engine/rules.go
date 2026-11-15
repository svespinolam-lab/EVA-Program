package engine

// rules.go — clinical and life-support rules of the triage engine.
//
// Precision measures (versus fixed thresholds):
//   - Effort-aware heart rate: the expected HR is the personal resting
//     baseline plus the load of the current activity (rest/moderate/high),
//     and the ceiling is 95 % of the age-predicted maximum (Tanaka:
//     208 − 0.7 × age).
//   - Personal baseline z-scores (mean ± SD from the adaptation phase).
//   - SpO₂ is a 5-second median, so motion artefacts do not trigger alarms.
//   - Persistence: a condition is raised after 3 consecutive readings and
//     cleared after 5 (hysteresis). Extreme values are raised at once.
//   - Plausibility checks flag a probable sensor fault (impossible values,
//     flat line, abrupt jumps) instead of a false emergency.
//   - Silent calibration offsets sent by the phone (e.g. luteal phase) are
//     applied to temperature and heart rate without revealing the reason.

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"eva/internal/model"
)

const (
	upTicks   = 3 // consecutive readings needed to raise a condition
	downTicks = 5 // consecutive readings needed to lower it
)

var effortWord = []string{"rest", "moderate effort", "high effort"}

// rawFinding is the instantaneous result of one rule on one reading.
type rawFinding struct {
	code      string // condition family, e.g. SPO2, HR_REST, SUIT_P
	level     int    // 1 advisory · 2 risk · 3 emergency
	metric    string
	immediate bool // skip persistence (extreme value or explicit request)
	fault     bool
}

// Finding is an active (persisted) condition.
type Finding struct {
	Code   string `json:"code"`
	Level  int    `json:"level"`
	Metric string `json:"metric,omitempty"`
	Label  string `json:"label"`
	Value  string `json:"value,omitempty"`
	Since  int64  `json:"since"`
	Fault  bool   `json:"fault,omitempty"`
}

// Factor is a vulnerability factor (accumulated risk, not an acute event).
type Factor struct {
	Code   string `json:"code"`
	Label  string `json:"label"`
	Points int    `json:"points"`
}

// cond tracks persistence (hysteresis) of one condition family.
type cond struct {
	level, up, down, upMin, downMax int
	since                           int64
	last                            rawFinding
}

func (c *cond) step(raw int, immediate bool, now int64) {
	switch {
	case raw > c.level:
		c.down, c.downMax = 0, 0
		c.up++
		if c.upMin == 0 || raw < c.upMin {
			c.upMin = raw
		}
		if c.up >= upTicks || immediate {
			if c.level == 0 {
				c.since = now
			}
			if immediate {
				c.level = raw
			} else {
				c.level = c.upMin
			}
			c.up, c.upMin = 0, 0
		}
	case raw < c.level:
		c.up, c.upMin = 0, 0
		c.down++
		if raw > c.downMax {
			c.downMax = raw
		}
		if c.down >= downTicks {
			c.level = c.downMax
			c.down, c.downMax = 0, 0
			if c.level == 0 {
				c.since = 0
			}
		}
	default:
		c.up, c.down, c.upMin, c.downMax = 0, 0, 0, 0
	}
}

func val(p *float64) (float64, bool) {
	if p == nil || math.IsNaN(*p) {
		return 0, false
	}
	return *p, true
}

func effortOf(s *model.Sample) int {
	if s != nil && s.Effort != nil && *s.Effort >= 0 && *s.Effort <= 2 {
		return *s.Effort
	}
	return model.EffortModerate
}

func hrMaxOf(age int) float64 {
	if age <= 0 {
		age = 40
	}
	return 208 - 0.7*float64(age) // Tanaka 2001
}

func expectedHR(base float64, effort int) float64 {
	return base + []float64{8, 35, 65}[effort]
}

// faultsOf returns the metrics whose reading is physically implausible.
func faultsOf(cs *crewState, s *model.Sample, useFilters bool) map[string]bool {
	f := map[string]bool{}
	if v, ok := val(s.HR); ok && (v < 25 || v > 240) {
		f["hr"] = true
	}
	if v, ok := val(s.SpO2); ok && (v < 50 || v > 100.5) {
		f["spo2"] = true
	}
	if v, ok := val(s.Temp); ok && (v < 30 || v > 43.5) {
		f["temp"] = true
	}
	if useFilters && (cs.hrSame >= 30 || cs.hrJump) {
		f["hr"] = true
	}
	return f
}

// rawFindings applies every rule to one reading. useFilters=false is used
// for back-filled (store-and-forward) readings, which skip the live filters.
func rawFindings(cs *crewState, s *model.Sample, useFilters bool) []rawFinding {
	var out []rawFinding
	add := func(code string, level int, metric string, immediate bool) {
		out = append(out, rawFinding{code: code, level: level, metric: metric, immediate: immediate})
	}
	b := cs.b
	cal := cs.cal
	eff := effortOf(s)
	fault := faultsOf(cs, s, useFilters)
	for _, m := range []string{"hr", "spo2", "temp"} {
		if fault[m] {
			out = append(out, rawFinding{code: "SENSOR_" + strings.ToUpper(m), level: 1, metric: m, fault: true})
		}
	}

	// Oxygen saturation (5-second median)
	if v, ok := val(s.SpO2); ok && !fault["spo2"] {
		if useFilters {
			v = cs.spo2Median(v)
		}
		switch {
		case v < 90:
			add("SPO2", 3, "spo2", v < 85)
		case v < 92:
			add("SPO2", 2, "spo2", false)
		case v < 94:
			add("SPO2", 1, "spo2", false)
		case b.SpO2 > 0 && v < b.SpO2-4:
			add("SPO2", 1, "spo2", false)
		}
	}

	// Heart rate, adjusted to effort and personal baseline
	if hr, ok := val(s.HR); ok && !fault["hr"] {
		base := b.HRRest + cal.HR
		if base <= 0 {
			base = 65
		}
		exp := expectedHR(base, eff)
		sd := math.Max(b.HRRestSD, 5)
		switch {
		case hr < 40:
			add("HR_LOW", 3, "hr", hr < 35)
		case hr < 45:
			add("HR_LOW", 2, "hr", false)
		}
		switch {
		case hr >= 0.95*hrMaxOf(cs.m.Age):
			add("HR_MAX", 3, "hr", hr >= hrMaxOf(cs.m.Age))
		case eff == model.EffortRest && hr > base+60:
			add("HR_REST", 3, "hr", false)
		case eff == model.EffortRest && hr >= base*1.35:
			add("HR_REST", 2, "hr", false)
		case eff > model.EffortRest && hr > exp+30:
			add("HR_EFFORT", 2, "hr", false)
		case eff == model.EffortRest && (hr-exp)/sd > 2:
			add("HR_REST", 1, "hr", false)
		case eff > model.EffortRest && hr > exp+18:
			add("HR_EFFORT", 1, "hr", false)
		}
	}

	// Core temperature (minus silent calibration)
	if t, ok := val(s.Temp); ok && !fault["temp"] {
		t -= cal.TempC
		switch {
		case t >= 39.0:
			add("TEMP_HIGH", 3, "temp", t >= 40)
		case t >= 38.0:
			add("TEMP_HIGH", 2, "temp", false)
		case t >= 37.6:
			add("TEMP_HIGH", 1, "temp", false)
		case t < 34.5:
			add("TEMP_LOW", 3, "temp", false)
		case t < 35.0:
			add("TEMP_LOW", 2, "temp", false)
		case t < 35.5:
			add("TEMP_LOW", 1, "temp", false)
		}
	}

	// Respiratory rate (higher tolerance under high effort)
	if rr, ok := val(s.RR); ok {
		hi := 0.0
		if eff == model.EffortHigh {
			hi = 6
		}
		switch {
		case rr > 36+hi || rr < 6:
			add("RR", 3, "rr", false)
		case rr > 30+hi || rr < 8:
			add("RR", 2, "rr", false)
		case rr > 24+hi:
			add("RR", 1, "rr", false)
		}
	}

	// HRV acute drop (stress marker)
	if hv, ok := val(s.HRV); ok && b.HRV > 0 && eff == model.EffortRest && hv < 0.65*b.HRV {
		add("HRV", 1, "hrv", false)
	}

	// Radiation dose rate (solar particle event)
	if r, ok := val(s.Rad); ok {
		switch {
		case r > 2:
			add("RAD", 3, "rad", true)
		case r > 0.5:
			add("RAD", 2, "rad", false)
		case r > 0.15:
			add("RAD", 1, "rad", false)
		}
	}

	// Suit and life support (same limits as the EVA app)
	if p, ok := val(s.SuitP); ok {
		switch {
		case p < 3.6:
			add("SUIT_P", 3, "suitP", false)
		case p < 3.9 || p > 4.9:
			add("SUIT_P", 2, "suitP", false)
		case p < 4.1 || p > 4.7:
			add("SUIT_P", 1, "suitP", false)
		}
	}
	if o, ok := val(s.O2); ok {
		switch {
		case o < 20:
			add("O2_RES", 3, "o2", false)
		case o < 45:
			add("O2_RES", 2, "o2", false)
		case o < 90:
			add("O2_RES", 1, "o2", false)
		}
	}
	if c, ok := val(s.CO2); ok {
		switch {
		case c > 10:
			add("CO2", 3, "co2", false)
		case c > 7.6:
			add("CO2", 2, "co2", false)
		case c > 5:
			add("CO2", 1, "co2", false)
		}
	}
	if bt, ok := val(s.Batt); ok {
		switch {
		case bt < 10:
			add("BATT", 3, "batt", false)
		case bt < 20:
			add("BATT", 2, "batt", false)
		case bt < 35:
			add("BATT", 1, "batt", false)
		}
	}
	return out
}

// proximity: 0 (centre of normal) to 1 (at the advisory threshold).
// Gives a continuous score inside the green band.
func proximity(cs *crewState, s *model.Sample) float64 {
	if s == nil {
		return 0
	}
	p := 0.0
	clamp := func(x float64) float64 { return math.Max(0, math.Min(1, x)) }
	if hr, ok := val(s.HR); ok {
		base := cs.b.HRRest + cs.cal.HR
		if base <= 0 {
			base = 65
		}
		p = math.Max(p, clamp((hr-expectedHR(base, effortOf(s)))/18))
	}
	if v, ok := val(s.SpO2); ok {
		p = math.Max(p, clamp((97.5-v)/3.5))
	}
	if t, ok := val(s.Temp); ok {
		p = math.Max(p, clamp((t-cs.cal.TempC-37.0)/0.6))
	}
	if c, ok := val(s.CO2); ok {
		p = math.Max(p, clamp((c-3)/2))
	}
	return p
}

/* ---------- Text: labels, contextual diagnosis, protocol, voice ---------- */

var codePriority = map[string]int{
	"CREW_HELP": 0, "NO_RESPONSE": 1, "SPO2": 2, "SUIT_P": 3, "CO2": 4, "O2_RES": 5, "HR_MAX": 6, "HR_REST": 7,
	"HR_LOW": 8, "TEMP_HIGH": 9, "TEMP_LOW": 10, "RR": 11, "RAD": 12, "LINK_LOST": 13, "HR_EFFORT": 14, "BATT": 15,
	"HRV": 16, "SENSOR_HR": 17, "SENSOR_SPO2": 18, "SENSOR_TEMP": 19,
}

func sortFindings(f []Finding) {
	sort.SliceStable(f, func(i, j int) bool {
		if f[i].Level != f[j].Level {
			return f[i].Level > f[j].Level
		}
		return codePriority[f[i].Code] < codePriority[f[j].Code]
	})
}

func fmtDur(ms int64) string {
	s := ms / 1000
	switch {
	case s < 60:
		return fmt.Sprintf("%d s", s)
	case s < 3600:
		return fmt.Sprintf("%d min", s/60)
	default:
		return fmt.Sprintf("%d h %02d min", s/3600, (s%3600)/60)
	}
}

func f0(p *float64) string {
	if v, ok := val(p); ok {
		return fmt.Sprintf("%.0f", v)
	}
	return "—"
}
func f1(p *float64) string {
	if v, ok := val(p); ok {
		return fmt.Sprintf("%.1f", v)
	}
	return "—"
}

func levelWord(l int) string {
	return []string{"Nominal", "Advisory", "Risk", "Emergency"}[clampInt(l, 0, 3)]
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// labelValue returns a short label and the formatted value of a finding.
func labelValue(code string, level int, s *model.Sample) (string, string) {
	if s == nil {
		s = &model.Sample{}
	}
	switch code {
	case "SPO2":
		return "SpO₂ low", f0(s.SpO2) + " %"
	case "HR_MAX":
		return "Heart rate near maximum", f0(s.HR) + " bpm"
	case "HR_REST":
		return "Heart rate high at rest", f0(s.HR) + " bpm"
	case "HR_EFFORT":
		return "Heart rate high for effort", f0(s.HR) + " bpm"
	case "HR_LOW":
		return "Heart rate low", f0(s.HR) + " bpm"
	case "TEMP_HIGH":
		return "Core temperature high", f1(s.Temp) + " °C"
	case "TEMP_LOW":
		return "Core temperature low", f1(s.Temp) + " °C"
	case "RR":
		return "Respiratory rate abnormal", f0(s.RR) + " rpm"
	case "HRV":
		return "HRV drop (acute stress)", f0(s.HRV) + " ms"
	case "RAD":
		return "Radiation dose rate high", fmt.Sprintf("%.2f mSv/h", deref(s.Rad))
	case "SUIT_P":
		return "Suit pressure out of range", fmt.Sprintf("%.2f psi", deref(s.SuitP))
	case "O2_RES":
		return "O₂ reserve low", f0(s.O2) + " min"
	case "CO2":
		return "Helmet CO₂ high", f1(s.CO2) + " mmHg"
	case "BATT":
		return "Suit battery low", f0(s.Batt) + " %"
	case "SENSOR_HR":
		return "Possible heart-rate sensor fault", f0(s.HR) + " bpm"
	case "SENSOR_SPO2":
		return "Possible SpO₂ sensor fault", f0(s.SpO2) + " %"
	case "SENSOR_TEMP":
		return "Possible temperature sensor fault", f1(s.Temp) + " °C"
	case "LINK_LOST":
		return "Telemetry lost", ""
	case "NO_RESPONSE":
		return "No response to alert", ""
	case "CREW_HELP":
		return "Crew member asked for help", ""
	}
	return code, ""
}

func deref(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

// diagnosis builds the contextual sentence shown to the operator,
// e.g. "Heart rate +38 % over baseline at rest for 6 min".
func diagnosis(cs *crewState, f Finding, now int64) string {
	s := cs.latest
	if s == nil {
		s = &model.Sample{}
	}
	b := cs.b
	dur := "now"
	if f.Since > 0 {
		dur = "for " + fmtDur(now-f.Since)
	}
	eff := effortOf(s)
	switch f.Code {
	case "SPO2":
		return fmt.Sprintf("SpO₂ %s %% (baseline %.0f %%) %s", f0(s.SpO2), b.SpO2, dur)
	case "HR_REST", "HR_LOW":
		base := b.HRRest + cs.cal.HR
		if hr, ok := val(s.HR); ok && base > 0 {
			return fmt.Sprintf("Heart rate %+.0f %% vs baseline (%.0f → %.0f bpm) at %s %s", (hr-base)/base*100, base, hr, effortWord[eff], dur)
		}
	case "HR_EFFORT":
		base := b.HRRest + cs.cal.HR
		if hr, ok := val(s.HR); ok {
			return fmt.Sprintf("Heart rate %.0f bpm, %+.0f over expected for %s %s", hr, hr-expectedHR(base, eff), effortWord[eff], dur)
		}
	case "HR_MAX":
		if hr, ok := val(s.HR); ok {
			return fmt.Sprintf("Heart rate %.0f bpm = %.0f %% of age-predicted max %s", hr, hr/hrMaxOf(cs.m.Age)*100, dur)
		}
	case "TEMP_HIGH", "TEMP_LOW":
		if t, ok := val(s.Temp); ok {
			return fmt.Sprintf("Core temperature %.1f °C (%+.1f vs baseline) %s", t, t-cs.cal.TempC-b.Temp, dur)
		}
	case "RR":
		return fmt.Sprintf("Respiratory rate %s rpm (baseline %.0f) at %s %s", f0(s.RR), b.RR, effortWord[eff], dur)
	case "HRV":
		if hv, ok := val(s.HRV); ok && b.HRV > 0 {
			return fmt.Sprintf("HRV %.0f ms, %.0f %% below baseline at rest %s", hv, (1-hv/b.HRV)*100, dur)
		}
	case "RAD":
		return fmt.Sprintf("Dose rate %.2f mSv/h · EVA dose %.2f mSv %s", deref(s.Rad), cs.dose, dur)
	case "SUIT_P":
		return fmt.Sprintf("Suit pressure %.2f psi (nominal 4.1–4.5) %s", deref(s.SuitP), dur)
	case "O2_RES":
		return fmt.Sprintf("O₂ reserve %s min left %s", f0(s.O2), dur)
	case "CO2":
		return fmt.Sprintf("Helmet CO₂ %s mmHg (limit 5) %s", f1(s.CO2), dur)
	case "BATT":
		return fmt.Sprintf("Suit battery %s %% %s", f0(s.Batt), dur)
	case "SENSOR_HR", "SENSOR_SPO2", "SENSOR_TEMP":
		return "Implausible reading (out of physical range, flat line or jump) — verify the sensor before acting"
	case "LINK_LOST":
		return fmt.Sprintf("No telemetry %s · last reading %s ago", dur, fmtDur(now-cs.lastT))
	case "NO_RESPONSE":
		return fmt.Sprintf("Alert not acknowledged by crew or operator %s · escalated to buddy", dur)
	case "CREW_HELP":
		return fmt.Sprintf("Crew member answered “Need help” %s", dur)
	}
	l, v := labelValue(f.Code, f.Level, s)
	return strings.TrimSpace(l + " " + v + " " + dur)
}

// protocol returns the suggested steps for the operator.
func protocol(code string, level int) []string {
	switch code {
	case "SPO2":
		if level >= 3 {
			return []string{"Order an immediate stop of all activity", "Check O₂ valve and suit flow; open purge if needed", "Return to airlock escorted by buddy", "Prepare cabin O₂ and medical kit", "Re-check SpO₂ every 30 s"}
		}
		return []string{"Reduce pace; rest 2–3 minutes", "Verify suit gas mix and O₂ flow", "Re-check SpO₂ in 1 minute", "Prepare return if below 90 %"}
	case "HR_MAX", "HR_REST":
		if level >= 3 {
			return []string{"Stop physical activity now", "Ask about chest pain, dizziness or shortness of breath", "Check for heat stress or dehydration", "Prepare return if HR is not below 120 bpm in 3 min"}
		}
		return []string{"Confirm crew status by radio", "Check suit temperature and hydration", "Guide slow breathing for 1 minute", "Re-assess in 3 minutes"}
	case "HR_EFFORT":
		return []string{"Order a 3-minute pause and water", "Lower task intensity", "Re-assess after the pause"}
	case "HR_LOW":
		return []string{"Confirm consciousness by radio", "Ask about dizziness or weakness", "Send buddy to assist if no clear answer"}
	case "TEMP_HIGH":
		if level >= 3 {
			return []string{"Set liquid cooling to maximum", "Abort task and return to airlock", "Watch for confusion or nausea", "Prepare cooling in cabin"}
		}
		return []string{"Increase liquid cooling flow", "Reduce effort and hydrate", "Re-check in 2 minutes"}
	case "TEMP_LOW":
		return []string{"Increase suit heating", "Check thermal garment and power", "Reduce time in shadow"}
	case "RR":
		return []string{"Stop activity; confirm breathing by radio", "Check CO₂ scrubbing and O₂ flow", "Prepare return to airlock"}
	case "HRV":
		return []string{"Acute stress marker: check in with the crew member", "Plan a short pause", "Watch heart rate trend"}
	case "RAD":
		if level >= 3 {
			return []string{"Seek shelter immediately (rover or habitat)", "Log position and time", "Track cumulative dose for the report"}
		}
		return []string{"Limit exposure; head toward shelter", "Track dose rate every minute"}
	case "SUIT_P":
		return []string{"Search for leaks: gloves, seals, valves", "Return to airlock", "Prepare airlock repress"}
	case "O2_RES":
		return []string{"Plan return with O₂ margin", "Return now if reserve < 20 min", "Reduce exertion to save O₂"}
	case "CO2":
		return []string{"Switch to backup CO₂ filter", "Reduce effort", "Return if CO₂ keeps rising"}
	case "BATT":
		return []string{"Reduce power use (lights, heaters)", "Head back to the airlock"}
	case "LINK_LOST":
		return []string{"Call by radio on all channels", "Ask buddy to check on the partner", "Verify suit antenna and phone"}
	case "NO_RESPONSE":
		return []string{"Call by radio on all channels", "Send buddy to assist", "Consider EVA abort"}
	case "CREW_HELP":
		return []string{"Talk to the crew member now", "Send buddy to assist", "Prepare rescue or EVA abort"}
	case "SENSOR_HR", "SENSOR_SPO2", "SENSOR_TEMP":
		return []string{"Ask crew to check sensor fit and contact", "Cross-check with the other vitals", "Do not act on this value alone"}
	}
	return []string{"Passive monitoring"}
}

// voice returns the spoken script (≤ 8 words, one direct action).
func voice(code string, level int, s *model.Sample) string {
	if s == nil {
		s = &model.Sample{}
	}
	switch code {
	case "SPO2":
		if level >= 3 {
			return "Oxygen at " + f0(s.SpO2) + ". Return to airlock now."
		}
		return "Oxygen at " + f0(s.SpO2) + ". Slow down, check flow."
	case "HR_MAX", "HR_REST":
		if level >= 3 {
			return "Heart rate " + f0(s.HR) + ". Stop and rest now."
		}
		return "Heart rate high at rest. Breathe slowly."
	case "HR_EFFORT":
		return "Heart rate high. Pause three minutes."
	case "HR_LOW":
		return "Heart rate low. Report your status."
	case "TEMP_HIGH":
		if level >= 3 {
			return "Temperature " + f1(s.Temp) + ". Max cooling, return now."
		}
		return "Temperature rising. Increase suit cooling."
	case "TEMP_LOW":
		return "Temperature low. Increase suit heating."
	case "RR":
		return "Breathing rate abnormal. Stop and report."
	case "HRV":
		return "Stress marker high. Take a pause."
	case "RAD":
		return "Radiation spike. Seek shelter now."
	case "SUIT_P":
		return "Suit pressure low. Return to airlock."
	case "O2_RES":
		return "Oxygen reserve low. Head back now."
	case "CO2":
		return "CO2 high. Switch to backup filter."
	case "BATT":
		return "Suit battery low. Head back now."
	case "SENSOR_HR", "SENSOR_SPO2", "SENSOR_TEMP":
		return "Sensor reading invalid. Check sensor fit."
	case "LINK_LOST":
		return "Signal lost. Check your radio."
	case "NO_RESPONSE":
		return "Please confirm your status now."
	case "CREW_HELP":
		return "Help is on the way."
	}
	return "Check your status."
}

// buddyVoice is spoken on the other explorer's phone on escalation.
func buddyVoice(role, code string) string {
	switch code {
	case "CREW_HELP":
		return role + " needs help. Assist now."
	case "LINK_LOST":
		return "Lost signal from " + role + ". Check partner."
	}
	return role + " not responding. Go assist now."
}

/* ---------- Surveillance Priority Index (IPV, 0–100) ---------- */

// ipvOf combines acute risk (live findings) and accumulated vulnerability.
//
//	90–100 EMERGENCY · 70–89 RISK · 40–69 VULNERABLE · 0–39 STABLE
func ipvOf(findings []Finding, vuln int, prox float64) (int, string, int) {
	var n [4]int
	for _, f := range findings {
		n[clampInt(f.Level, 0, 3)]++
	}
	var ipv int
	switch {
	case n[3] > 0:
		ipv = 92 + min(8, 3*(n[3]-1)+2*n[2])
	case n[2] > 0:
		ipv = 72 + min(17, 5*(n[2]-1)+2*n[1]+vuln/10)
	case n[1] > 0:
		ipv = 45 + min(24, 6*(n[1]-1)+vuln/4)
	case vuln >= 15:
		ipv = 40 + min(29, vuln-15)
	default:
		ipv = min(39, vuln+int(prox*20))
	}
	ipv = clampInt(ipv, 0, 100)
	switch {
	case ipv >= 90:
		return ipv, "EMERGENCY", 3
	case ipv >= 70:
		return ipv, "RISK", 2
	case ipv >= 40:
		return ipv, "VULNERABLE", 1
	}
	return ipv, "STABLE", 0
}
