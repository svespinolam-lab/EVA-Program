// Package model holds the data types shared by the store, the triage
// engine and the HTTP/WebSocket layer. JSON names match the EVA app.
//
// Time convention: every timestamp is Unix epoch milliseconds (UTC).
package model

// Effort levels reported by the suit / phone (physical activity context).
const (
	EffortRest     = 0
	EffortModerate = 1
	EffortHigh     = 2
)

// Calib is the silent calibration a device applies to its own wearer
// (for example luteal phase: +0.3 to +0.5 °C and +3 to +5 bpm). Only the
// offsets travel to the station, never the reason (privacy by design).
type Calib struct {
	TempC float64 `json:"temp,omitempty"`
	HR    float64 `json:"hr,omitempty"`
}

// Sample is one telemetry reading (1 Hz) from a phone or a virtual suit.
// Pointer fields are optional: nil means "sensor not connected".
type Sample struct {
	CrewID    string   `json:"crewId"`
	MissionID string   `json:"missionId,omitempty"`
	Seq       int64    `json:"seq,omitempty"`
	T         int64    `json:"t"`
	HR        *float64 `json:"hr,omitempty"`     // heart rate, bpm
	SpO2      *float64 `json:"spo2,omitempty"`   // oxygen saturation, %
	Temp      *float64 `json:"temp,omitempty"`   // core temperature, °C
	RR        *float64 `json:"rr,omitempty"`     // respiratory rate, rpm
	HRV       *float64 `json:"hrv,omitempty"`    // heart-rate variability RMSSD, ms
	Rad       *float64 `json:"rad,omitempty"`    // radiation dose rate, mSv/h
	Effort    *int     `json:"effort,omitempty"` // 0 rest · 1 moderate · 2 high
	SuitP     *float64 `json:"suitP,omitempty"`  // suit pressure, psi
	O2        *float64 `json:"o2,omitempty"`     // O2 reserve, minutes
	CO2       *float64 `json:"co2,omitempty"`    // helmet CO2, mmHg
	Batt      *float64 `json:"batt,omitempty"`   // suit battery, %
	BPSys     *float64 `json:"bpSys,omitempty"`  // last blood pressure, mmHg
	BPDia     *float64 `json:"bpDia,omitempty"`
	Cal       *Calib   `json:"cal,omitempty"`
	Source    string   `json:"source,omitempty"` // device | virtual | replay
}

// Crew is one astronaut. Role: EV1/EV2/EV3 go outside, IV stays inside.
type Crew struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Initials   string  `json:"initials"`
	Role       string  `json:"role"`
	Specialty  string  `json:"specialty"`
	Sex        string  `json:"sex"` // F | M (biological, used by clinical formulas)
	Age        int     `json:"age"`
	HeightCm   float64 `json:"heightCm"`
	WeightKg   float64 `json:"weightKg"`
	BloodType  string  `json:"bloodType"`
	Conditions string  `json:"conditions"`
	Allergies  string  `json:"allergies"`
	Medication string  `json:"medication"`
	RadBudget  float64 `json:"radBudget"` // career limit, mSv (600 by default)
	RadAccum   float64 `json:"radAccum"`  // career dose so far, mSv
}

// IsEVA reports whether the crew member goes outside (EV1, EV2…).
func (c Crew) IsEVA() bool { return len(c.Role) >= 2 && c.Role[:2] == "EV" }

// Baseline is the personal reference used by the triage engine.
// Phase ADAPTATION_14D: days 1–14 (mean ± SD); ROLLING_7D afterwards.
type Baseline struct {
	CrewID    string  `json:"crewId"`
	Phase     string  `json:"phase"`
	Days      int     `json:"days"`
	HRRest    float64 `json:"hrRest"`
	HRRestSD  float64 `json:"hrRestSd"`
	HRV       float64 `json:"hrv"`
	HRVSD     float64 `json:"hrvSd"`
	SpO2      float64 `json:"spo2"`
	SpO2SD    float64 `json:"spo2Sd"`
	Temp      float64 `json:"temp"`
	TempSD    float64 `json:"tempSd"`
	RR        float64 `json:"rr"`
	RRSD      float64 `json:"rrSd"`
	BPSys     float64 `json:"bpSys"`
	BPDia     float64 `json:"bpDia"`
	SleepH    float64 `json:"sleepH"`
	UpdatedAt int64   `json:"updatedAt"`
}

// Mission is one extravehicular activity (EVA).
type Mission struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Environment string  `json:"environment"`
	Site        string  `json:"site"`
	PlannedH    float64 `json:"plannedH"`
	Status      string  `json:"status"` // PLANNED ACTIVE COMPLETED ABORTED
	StartedAt   *int64  `json:"startedAt,omitempty"`
	EndedAt     *int64  `json:"endedAt,omitempty"`
}

// Readiness is the privacy-filtered daily verdict sent by the phone.
// The private diary (mood, notes, cycle…) never leaves the device.
type Readiness struct {
	CrewID        string   `json:"crewId"`
	Day           string   `json:"day"`    // YYYY-MM-DD (device local date)
	Status        string   `json:"status"` // GREEN | YELLOW | RED
	Vulnerability int      `json:"vulnerability"`
	Summary       string   `json:"summary"`
	Factors       []string `json:"factors"`
	SleepH        *float64 `json:"sleepH,omitempty"`
	Hydration     *int     `json:"hydration,omitempty"` // urine-colour scale 1–8
	FatigueHigh   bool     `json:"fatigueHigh"`
	HRRest        *float64 `json:"hrRest,omitempty"`
	HRVRest       *float64 `json:"hrvRest,omitempty"`
	ReceivedAt    int64    `json:"receivedAt"`
}

// Alert is raised by the triage engine when a condition persists.
type Alert struct {
	ID          int64    `json:"id"`
	MissionID   string   `json:"missionId,omitempty"`
	CrewID      string   `json:"crewId"`
	T           int64    `json:"t"`
	Level       string   `json:"level"` // ADVISORY | RISK | EMERGENCY
	Code        string   `json:"code"`
	Metric      string   `json:"metric,omitempty"`
	Voice       string   `json:"voice"`  // ≤ 8 words
	Reason      string   `json:"reason"` // contextual diagnosis
	Protocol    []string `json:"protocol"`
	Response    string   `json:"response"` // PENDING COPIED IM_OK NEED_HELP NO_RESPONSE
	RespondedBy string   `json:"respondedBy,omitempty"`
	RespondedAt *int64   `json:"respondedAt,omitempty"`
	ResponseS   *float64 `json:"responseS,omitempty"`
	Escalated   bool     `json:"escalated"`
	EscalatedAt *int64   `json:"escalatedAt,omitempty"`
	ResolvedAt  *int64   `json:"resolvedAt,omitempty"`
}

// LevelNum maps an alert level to the 0–3 colour scale.
func (a Alert) LevelNum() int {
	switch a.Level {
	case "EMERGENCY":
		return 3
	case "RISK":
		return 2
	case "ADVISORY":
		return 1
	}
	return 0
}

// Incident is a black-box record: a frozen window T−30 min … T+15 min.
type Incident struct {
	ID             int64    `json:"id"`
	MissionID      string   `json:"missionId,omitempty"`
	CrewID         string   `json:"crewId"`
	AlertID        *int64   `json:"alertId,omitempty"`
	Trigger        string   `json:"trigger"` // EMERGENCY | ABORT | MANUAL
	EventT         int64    `json:"eventT"`
	WindowStart    int64    `json:"windowStart"`
	WindowEnd      int64    `json:"windowEnd"`
	Status         string   `json:"status"` // RECORDING | SEALED
	Classification string   `json:"classification,omitempty"`
	FirstDeviation *int64   `json:"firstDeviation,omitempty"`
	DetectionS     *float64 `json:"detectionS,omitempty"`
	ResponseS      *float64 `json:"responseS,omitempty"`
	SamplesCount   int      `json:"samplesCount"`
	SHA256         string   `json:"sha256,omitempty"`
	SealedAt       *int64   `json:"sealedAt,omitempty"`
}

// OpLog is one operator/system action (the "voice recorder" side of the
// black box): instructions, acknowledgements, aborts, links…
type OpLog struct {
	ID        int64  `json:"id"`
	MissionID string `json:"missionId,omitempty"`
	T         int64  `json:"t"`
	Actor     string `json:"actor"`
	CrewID    string `json:"crewId,omitempty"`
	Action    string `json:"action"`
	Detail    string `json:"detail"`
}

// ChainEntry is one link of the tamper-evident black-box hash chain.
type ChainEntry struct {
	ID       int64  `json:"id"`
	T        int64  `json:"t"`
	Kind     string `json:"kind"`
	CrewID   string `json:"crewId,omitempty"`
	Ref      string `json:"ref,omitempty"`
	Payload  string `json:"payload"`
	PrevHash string `json:"prevHash"`
	Hash     string `json:"hash"`
}

// Monthly is the periodic clinical panel of one astronaut.
type Monthly struct {
	CrewID           string   `json:"crewId"`
	Month            string   `json:"month"` // YYYY-MM
	AvgHR            *float64 `json:"avgHr"`
	AvgSpO2          *float64 `json:"avgSpo2"`
	AvgSleepH        *float64 `json:"avgSleepH"`
	AvgHRV           *float64 `json:"avgHrv"`
	Hemoglobin       *float64 `json:"hemoglobin"`
	WBC              *float64 `json:"wbc"`
	Platelets        *float64 `json:"platelets"`
	Glucose          *float64 `json:"glucose"`
	OrthostaticDrop  *float64 `json:"orthostaticDrop"`
	WeightKg         *float64 `json:"weightKg"`
	WeightChangePct  *float64 `json:"weightChangePct"`
	ExercisePct      *float64 `json:"exercisePct"`
	RadiationMonthMS *float64 `json:"radiationMonth"`
	MentalIndex      *int     `json:"mentalIndex"`
}

// EVASummary is the post-EVA physiological load of one astronaut. It feeds
// the next day's recovery estimate.
type EVASummary struct {
	MissionID string  `json:"missionId"`
	CrewID    string  `json:"crewId"`
	DurationS int64   `json:"durationS"`
	Samples   int     `json:"samples"`
	HRMean    float64 `json:"hrMean"`
	HRMax     float64 `json:"hrMax"`
	SpO2Min   float64 `json:"spo2Min"`
	TempMax   float64 `json:"tempMax"`
	TRIMP     float64 `json:"trimp"`
	Kcal      float64 `json:"kcal"`
	DoseMSv   float64 `json:"doseMsv"`
	PeakIPV   int     `json:"peakIpv"`
	Alerts    int     `json:"alerts"`
	RiskS     int64   `json:"riskS"`
	RecoveryH float64 `json:"recoveryH"`
	EndedAt   int64   `json:"endedAt"`
}

// F returns a pointer to v (helper for optional fields).
func F(v float64) *float64 { return &v }

// I returns a pointer to v.
func I(v int) *int { return &v }

// I64 returns a pointer to v.
func I64(v int64) *int64 { return &v }
