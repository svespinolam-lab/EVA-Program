// Package store is the SQLite persistence layer of EVA Station.
//
// Performance choices (the station must not lag during an EVA):
//   - WAL journal + synchronous=NORMAL: readers never block the writer.
//   - One dedicated writer connection, a separate read pool.
//   - Telemetry goes through a batching writer: rows are grouped into one
//     transaction every 250 ms (or every 500 rows) with a prepared
//     statement, instead of one transaction per reading.
//   - Timestamps are INTEGER milliseconds and the hot queries are indexed.
package store

import (
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"eva/internal/model"

	_ "modernc.org/sqlite" // pure-Go SQLite driver (no C compiler needed)
)

//go:embed schema.sql
var schemaSQL string

// Store wraps the database handles.
type Store struct {
	W *sql.DB // single writer connection
	R *sql.DB // read pool

	chainMu  sync.Mutex
	lastHash string

	telCh    chan telRow
	flushReq chan chan struct{}
	done     chan struct{}

	// SchemaFrom / SchemaTo: schema version found on disk and after the
	// migrations run by Open (equal when nothing had to be migrated).
	SchemaFrom, SchemaTo int

	// OnCommit is called after each telemetry batch is committed with the
	// highest device sequence stored per crew member (store-and-forward ack).
	OnCommit func(maxSeq map[string]int64)
}

type telRow struct {
	s       model.Sample
	mission string
	ipv     int
	status  string
	source  string
}

// Now returns the current time in Unix milliseconds.
// It is a variable so tests can drive time deterministically (e.g. the 30 s
// escalation) without sleeping.
var Now = func() int64 { return time.Now().UnixMilli() }

// Open creates (if needed) and opens the database at path.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	dsn := "file:" + filepath.ToSlash(path) +
		"?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)" +
		"&_pragma=busy_timeout(5000)&_pragma=temp_store(MEMORY)&_pragma=cache_size(-16000)"
	w, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	w.SetMaxOpenConns(1)
	// Safety copy before changing the schema of an existing database.
	var cur, tables int
	_ = w.QueryRow(`PRAGMA user_version`).Scan(&cur)
	_ = w.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table'`).Scan(&tables)
	if tables > 0 && cur < LatestSchema {
		dir := filepath.Join(filepath.Dir(path), "backups")
		if err := os.MkdirAll(dir, 0o755); err == nil {
			snap := filepath.Join(dir, fmt.Sprintf("eva-pre-migration-v%d-%s.db", cur, time.Now().UTC().Format("20060102T150405Z")))
			if _, err := w.Exec(`VACUUM INTO ?`, snap); err != nil {
				w.Close()
				return nil, fmt.Errorf("pre-migration backup: %w", err)
			}
		}
	}
	from, to, err := migrate(w)
	if err != nil {
		w.Close()
		return nil, fmt.Errorf("schema: %w", err)
	}
	r, err := sql.Open("sqlite", dsn)
	if err != nil {
		w.Close()
		return nil, err
	}
	r.SetMaxOpenConns(4)

	s := &Store{W: w, R: r, telCh: make(chan telRow, 4096), flushReq: make(chan chan struct{}), done: make(chan struct{}),
		SchemaFrom: from, SchemaTo: to}
	s.lastHash = "GENESIS"
	_ = s.R.QueryRow(`SELECT hash FROM blackbox_chain ORDER BY id DESC LIMIT 1`).Scan(&s.lastHash)
	go s.runWriter()
	return s, nil
}

// Close flushes pending telemetry and closes the database.
func (s *Store) Close() error {
	close(s.telCh)
	<-s.done
	s.R.Close()
	return s.W.Close()
}

func nullStr(v string) any {
	if v == "" {
		return nil
	}
	return v
}

/* =====================================================================
   Telemetry batching writer
   ===================================================================== */

const insertTelemetry = `INSERT OR IGNORE INTO telemetry
 (mission_id, crew_id, ts, hr, spo2, temp, rr, hrv, rad_rate, effort, suit_p, o2_min, co2, batt, bp_sys, bp_dia, ipv, status, source, seq, event_id)
 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`

// AddTelemetry queues one evaluated sample for the batching writer.
func (s *Store) AddTelemetry(sm model.Sample, mission string, ipv int, status, source string) {
	s.telCh <- telRow{s: sm, mission: mission, ipv: ipv, status: status, source: source}
}

// Flush blocks until every queued telemetry row is committed.
func (s *Store) Flush() {
	done := make(chan struct{})
	s.flushReq <- done
	<-done
}

func (s *Store) runWriter() {
	defer close(s.done)
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	buf := make([]telRow, 0, 512)

	flush := func() {
		if len(buf) == 0 {
			return
		}
		maxSeq := map[string]int64{}
		tx, err := s.W.Begin()
		if err == nil {
			stmt, err2 := tx.Prepare(insertTelemetry)
			if err2 == nil {
				for _, r := range buf {
					m := r.s
					if _, err := stmt.Exec(nullStr(r.mission), m.CrewID, m.T, m.HR, m.SpO2, m.Temp, m.RR, m.HRV, m.Rad, m.Effort,
						m.SuitP, m.O2, m.CO2, m.Batt, m.BPSys, m.BPDia, r.ipv, r.status, r.source, nullSeq(m.Seq), nullStr(m.EventID)); err != nil {
						fmt.Fprintln(os.Stderr, "telemetry insert:", err)
					}
					if m.Seq > maxSeq[m.CrewID] {
						maxSeq[m.CrewID] = m.Seq
					}
				}
				stmt.Close()
				err = tx.Commit()
			} else {
				tx.Rollback()
				err = err2
			}
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "telemetry batch:", err)
		}
		buf = buf[:0]
		if err == nil && s.OnCommit != nil && len(maxSeq) > 0 {
			s.OnCommit(maxSeq)
		}
	}

	for {
		select {
		case r, ok := <-s.telCh:
			if !ok {
				flush()
				return
			}
			buf = append(buf, r)
			if len(buf) >= 500 {
				flush()
			}
		case <-tick.C:
			flush()
		case done := <-s.flushReq:
			// drain what is already queued, then commit
			for more := true; more; {
				select {
				case r, ok := <-s.telCh:
					if !ok {
						more = false
						break
					}
					buf = append(buf, r)
				default:
					more = false
				}
			}
			flush()
			close(done)
		}
	}
}

func nullSeq(v int64) any {
	if v <= 0 {
		return nil
	}
	return v
}

// TelRow is one stored reading (used by black-box windows and reports).
type TelRow struct {
	T      int64    `json:"t"`
	HR     *float64 `json:"hr"`
	SpO2   *float64 `json:"spo2"`
	Temp   *float64 `json:"temp"`
	RR     *float64 `json:"rr"`
	HRV    *float64 `json:"hrv"`
	Rad    *float64 `json:"rad"`
	Effort *int     `json:"effort"`
	SuitP  *float64 `json:"suitP"`
	O2     *float64 `json:"o2"`
	CO2    *float64 `json:"co2"`
	Batt   *float64 `json:"batt"`
	IPV    int      `json:"ipv"`
	Status string   `json:"status"`
}

// Window returns the stored readings of one crew member in [from, to].
func (s *Store) Window(crewID string, from, to int64) ([]TelRow, error) {
	rows, err := s.R.Query(`SELECT ts, hr, spo2, temp, rr, hrv, rad_rate, effort, suit_p, o2_min, co2, batt, ipv, status
		FROM telemetry WHERE crew_id = ? AND ts BETWEEN ? AND ? ORDER BY ts`, crewID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TelRow
	for rows.Next() {
		var r TelRow
		if err := rows.Scan(&r.T, &r.HR, &r.SpO2, &r.Temp, &r.RR, &r.HRV, &r.Rad, &r.Effort, &r.SuitP, &r.O2, &r.CO2, &r.Batt, &r.IPV, &r.Status); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// MissionTelemetry returns the readings of one crew member during a mission.
func (s *Store) MissionTelemetry(missionID, crewID string) ([]TelRow, error) {
	rows, err := s.R.Query(`SELECT ts, hr, spo2, temp, rr, hrv, rad_rate, effort, suit_p, o2_min, co2, batt, ipv, status
		FROM telemetry WHERE mission_id = ? AND crew_id = ? ORDER BY ts`, missionID, crewID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TelRow
	for rows.Next() {
		var r TelRow
		if err := rows.Scan(&r.T, &r.HR, &r.SpO2, &r.Temp, &r.RR, &r.HRV, &r.Rad, &r.Effort, &r.SuitP, &r.O2, &r.CO2, &r.Batt, &r.IPV, &r.Status); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

/* =====================================================================
   Black-box hash chain (tamper evidence, like a flight data recorder)
   ===================================================================== */

func chainHash(prev string, ts int64, kind, crewID, ref, payload string) string {
	h := sha256.Sum256([]byte(prev + "|" + strconv.FormatInt(ts, 10) + "|" + kind + "|" + crewID + "|" + ref + "|" + payload))
	return hex.EncodeToString(h[:])
}

// Chain appends an event to the hash chain. payload is stored as JSON.
func (s *Store) Chain(kind, crewID, ref string, payload any) {
	b, err := json.Marshal(payload)
	if err != nil {
		b = []byte(`{}`)
	}
	s.chainMu.Lock()
	defer s.chainMu.Unlock()
	ts := Now()
	hash := chainHash(s.lastHash, ts, kind, crewID, ref, string(b))
	if _, err := s.W.Exec(`INSERT INTO blackbox_chain (ts, kind, crew_id, ref, payload, prev_hash, hash) VALUES (?,?,?,?,?,?,?)`,
		ts, kind, nullStr(crewID), nullStr(ref), string(b), s.lastHash, hash); err != nil {
		fmt.Fprintln(os.Stderr, "chain:", err)
		return
	}
	s.lastHash = hash
}

// ChainReport is the result of a full integrity check.
type ChainReport struct {
	OK       bool   `json:"ok"`
	Entries  int    `json:"entries"`
	BrokenAt int64  `json:"brokenAt,omitempty"`
	LastHash string `json:"lastHash"`
	Checked  int64  `json:"checked"`
}

// VerifyChain recomputes every link of the chain.
func (s *Store) VerifyChain() (ChainReport, error) {
	rows, err := s.R.Query(`SELECT id, ts, kind, COALESCE(crew_id,''), COALESCE(ref,''), payload, prev_hash, hash FROM blackbox_chain ORDER BY id`)
	if err != nil {
		return ChainReport{}, err
	}
	defer rows.Close()
	rep := ChainReport{OK: true, LastHash: "GENESIS", Checked: Now()}
	prev := "GENESIS"
	for rows.Next() {
		var e model.ChainEntry
		if err := rows.Scan(&e.ID, &e.T, &e.Kind, &e.CrewID, &e.Ref, &e.Payload, &e.PrevHash, &e.Hash); err != nil {
			return rep, err
		}
		rep.Entries++
		if e.PrevHash != prev || chainHash(prev, e.T, e.Kind, e.CrewID, e.Ref, e.Payload) != e.Hash {
			if rep.OK {
				rep.OK = false
				rep.BrokenAt = e.ID
			}
		}
		prev = e.Hash
	}
	rep.LastHash = prev
	return rep, rows.Err()
}

// ChainList returns the most recent chain entries (newest first).
func (s *Store) ChainList(limit int) ([]model.ChainEntry, error) {
	rows, err := s.R.Query(`SELECT id, ts, kind, COALESCE(crew_id,''), COALESCE(ref,''), payload, prev_hash, hash
		FROM blackbox_chain ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.ChainEntry
	for rows.Next() {
		var e model.ChainEntry
		if err := rows.Scan(&e.ID, &e.T, &e.Kind, &e.CrewID, &e.Ref, &e.Payload, &e.PrevHash, &e.Hash); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

/* =====================================================================
   Crew, baseline, missions
   ===================================================================== */

const crewCols = `id, name, initials, role, specialty, sex, age, COALESCE(height_cm,0), COALESCE(weight_kg,0),
 COALESCE(blood_type,''), COALESCE(conditions,''), COALESCE(allergies,''), COALESCE(medication,''),
 radiation_budget_msv, radiation_accum_msv`

func scanCrew(sc interface{ Scan(...any) error }) (model.Crew, error) {
	var c model.Crew
	err := sc.Scan(&c.ID, &c.Name, &c.Initials, &c.Role, &c.Specialty, &c.Sex, &c.Age, &c.HeightCm, &c.WeightKg,
		&c.BloodType, &c.Conditions, &c.Allergies, &c.Medication, &c.RadBudget, &c.RadAccum)
	return c, err
}

// ListCrew returns every astronaut ordered by role.
func (s *Store) ListCrew() ([]model.Crew, error) {
	rows, err := s.R.Query(`SELECT ` + crewCols + ` FROM crew ORDER BY role`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Crew
	for rows.Next() {
		c, err := scanCrew(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// AddRadiation adds an EVA dose to the career total.
func (s *Store) AddRadiation(crewID string, msv float64) error {
	_, err := s.W.Exec(`UPDATE crew SET radiation_accum_msv = radiation_accum_msv + ? WHERE id = ?`, msv, crewID)
	return err
}

// Baselines returns every personal baseline keyed by crew id.
func (s *Store) Baselines() (map[string]model.Baseline, error) {
	rows, err := s.R.Query(`SELECT crew_id, phase, days, hr_rest_mean, hr_rest_sd, hrv_mean, hrv_sd, spo2_mean, spo2_sd,
		temp_mean, temp_sd, rr_mean, rr_sd, bp_sys, bp_dia, sleep_h, updated_at FROM baseline`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]model.Baseline{}
	for rows.Next() {
		var b model.Baseline
		if err := rows.Scan(&b.CrewID, &b.Phase, &b.Days, &b.HRRest, &b.HRRestSD, &b.HRV, &b.HRVSD, &b.SpO2, &b.SpO2SD,
			&b.Temp, &b.TempSD, &b.RR, &b.RRSD, &b.BPSys, &b.BPDia, &b.SleepH, &b.UpdatedAt); err != nil {
			return nil, err
		}
		out[b.CrewID] = b
	}
	return out, rows.Err()
}

// SaveBaseline inserts or replaces a baseline.
func (s *Store) SaveBaseline(b model.Baseline) error {
	_, err := s.W.Exec(`INSERT OR REPLACE INTO baseline (crew_id, phase, days, hr_rest_mean, hr_rest_sd, hrv_mean, hrv_sd,
		spo2_mean, spo2_sd, temp_mean, temp_sd, rr_mean, rr_sd, bp_sys, bp_dia, sleep_h, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		b.CrewID, b.Phase, b.Days, b.HRRest, b.HRRestSD, b.HRV, b.HRVSD, b.SpO2, b.SpO2SD, b.Temp, b.TempSD,
		b.RR, b.RRSD, b.BPSys, b.BPDia, b.SleepH, b.UpdatedAt)
	return err
}

const missionCols = `id, name, environment, site, planned_h, status, started_at, ended_at`

func scanMission(sc interface{ Scan(...any) error }) (model.Mission, error) {
	var m model.Mission
	err := sc.Scan(&m.ID, &m.Name, &m.Environment, &m.Site, &m.PlannedH, &m.Status, &m.StartedAt, &m.EndedAt)
	return m, err
}

// Missions returns the most recent missions (newest first).
func (s *Store) Missions(limit int) ([]model.Mission, error) {
	rows, err := s.R.Query(`SELECT `+missionCols+` FROM missions ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Mission
	for rows.Next() {
		m, err := scanMission(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Mission returns one mission.
func (s *Store) Mission(id string) (model.Mission, error) {
	return scanMission(s.R.QueryRow(`SELECT `+missionCols+` FROM missions WHERE id = ?`, id))
}

// CurrentMission returns the ACTIVE mission, else the newest PLANNED one.
func (s *Store) CurrentMission() (model.Mission, error) {
	m, err := scanMission(s.R.QueryRow(`SELECT ` + missionCols + ` FROM missions WHERE status = 'ACTIVE' ORDER BY started_at DESC LIMIT 1`))
	if err == nil {
		return m, nil
	}
	return scanMission(s.R.QueryRow(`SELECT ` + missionCols + ` FROM missions WHERE status = 'PLANNED' ORDER BY created_at DESC LIMIT 1`))
}

// InsertMission creates a mission.
func (s *Store) InsertMission(m model.Mission) error {
	_, err := s.W.Exec(`INSERT INTO missions (id, name, environment, site, planned_h, status, started_at, ended_at, created_at)
		VALUES (?,?,?,?,?,?,?,?,?)`, m.ID, m.Name, m.Environment, m.Site, m.PlannedH, m.Status, m.StartedAt, m.EndedAt, Now())
	return err
}

// SetMissionStatus updates the status and the start/end times.
func (s *Store) SetMissionStatus(id, status string, started, ended *int64) error {
	_, err := s.W.Exec(`UPDATE missions SET status = ?, started_at = COALESCE(?, started_at), ended_at = COALESCE(?, ended_at) WHERE id = ?`,
		status, started, ended, id)
	return err
}

/* =====================================================================
   Readiness (privacy-filtered daily verdict)
   ===================================================================== */

// SaveReadiness inserts or replaces the verdict of one day.
func (s *Store) SaveReadiness(r model.Readiness) error {
	f, _ := json.Marshal(r.Factors)
	if r.Factors == nil {
		f = []byte("[]")
	}
	fat := 0
	if r.FatigueHigh {
		fat = 1
	}
	_, err := s.W.Exec(`INSERT OR REPLACE INTO readiness (crew_id, day, status, vulnerability, summary, factors, sleep_h, hydration,
		fatigue_high, hr_rest, hrv_rest, received_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.CrewID, r.Day, r.Status, r.Vulnerability, r.Summary, string(f), r.SleepH, r.Hydration, fat, r.HRRest, r.HRVRest, r.ReceivedAt)
	return err
}

// Readiness returns the verdicts of one crew member, newest first, limited to n days.
func (s *Store) Readiness(crewID string, n int) ([]model.Readiness, error) {
	rows, err := s.R.Query(`SELECT crew_id, day, status, vulnerability, summary, factors, sleep_h, hydration, fatigue_high, hr_rest, hrv_rest, received_at
		FROM readiness WHERE crew_id = ? ORDER BY day DESC LIMIT ?`, crewID, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Readiness
	for rows.Next() {
		var r model.Readiness
		var f string
		var fat int
		if err := rows.Scan(&r.CrewID, &r.Day, &r.Status, &r.Vulnerability, &r.Summary, &f, &r.SleepH, &r.Hydration, &fat, &r.HRRest, &r.HRVRest, &r.ReceivedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(f), &r.Factors)
		r.FatigueHigh = fat == 1
		out = append(out, r)
	}
	return out, rows.Err()
}

/* =====================================================================
   Alerts
   ===================================================================== */

const alertCols = `id, COALESCE(mission_id,''), crew_id, ts, level, code, COALESCE(metric,''), voice, reason, protocol, response,
 COALESCE(responded_by,''), responded_at, response_s, escalated, escalated_at, resolved_at`

func scanAlert(sc interface{ Scan(...any) error }) (model.Alert, error) {
	var a model.Alert
	var proto string
	var esc int
	err := sc.Scan(&a.ID, &a.MissionID, &a.CrewID, &a.T, &a.Level, &a.Code, &a.Metric, &a.Voice, &a.Reason, &proto, &a.Response,
		&a.RespondedBy, &a.RespondedAt, &a.ResponseS, &esc, &a.EscalatedAt, &a.ResolvedAt)
	_ = json.Unmarshal([]byte(proto), &a.Protocol)
	a.Escalated = esc == 1
	return a, err
}

// InsertAlert stores a new alert and returns its id.
func (s *Store) InsertAlert(a model.Alert) (int64, error) {
	p, _ := json.Marshal(a.Protocol)
	res, err := s.W.Exec(`INSERT INTO alerts (mission_id, crew_id, ts, level, code, metric, voice, reason, protocol, response)
		VALUES (?,?,?,?,?,?,?,?,?,?)`, nullStr(a.MissionID), a.CrewID, a.T, a.Level, a.Code, nullStr(a.Metric), a.Voice, a.Reason, string(p), a.Response)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateAlert writes the mutable fields of an alert.
func (s *Store) UpdateAlert(a model.Alert) error {
	p, _ := json.Marshal(a.Protocol)
	esc := 0
	if a.Escalated {
		esc = 1
	}
	_, err := s.W.Exec(`UPDATE alerts SET level = ?, voice = ?, reason = ?, protocol = ?, response = ?, responded_by = ?, responded_at = ?,
		response_s = ?, escalated = ?, escalated_at = ?, resolved_at = ? WHERE id = ?`,
		a.Level, a.Voice, a.Reason, string(p), a.Response, nullStr(a.RespondedBy), a.RespondedAt, a.ResponseS, esc, a.EscalatedAt, a.ResolvedAt, a.ID)
	return err
}

// Alert returns one alert.
func (s *Store) Alert(id int64) (model.Alert, error) {
	return scanAlert(s.R.QueryRow(`SELECT `+alertCols+` FROM alerts WHERE id = ?`, id))
}

// Alerts lists alerts (newest first). missionID "" = any mission; openOnly = not resolved.
func (s *Store) Alerts(missionID string, openOnly bool, limit int) ([]model.Alert, error) {
	q := `SELECT ` + alertCols + ` FROM alerts WHERE 1=1`
	var args []any
	if missionID != "" {
		q += ` AND mission_id = ?`
		args = append(args, missionID)
	}
	if openOnly {
		q += ` AND resolved_at IS NULL`
	}
	q += ` ORDER BY ts DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.R.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Alert
	for rows.Next() {
		a, err := scanAlert(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ResolveOpenAlerts closes every open alert (used at start-up).
func (s *Store) ResolveOpenAlerts(at int64) error {
	_, err := s.W.Exec(`UPDATE alerts SET resolved_at = ? WHERE resolved_at IS NULL`, at)
	return err
}

/* =====================================================================
   Incidents
   ===================================================================== */

const incidentCols = `id, COALESCE(mission_id,''), crew_id, alert_id, trigger, event_ts, window_start, window_end, status,
 COALESCE(classification,''), first_deviation_ts, detection_s, response_s, samples_count, COALESCE(sha256,''), sealed_at`

func scanIncident(sc interface{ Scan(...any) error }) (model.Incident, error) {
	var i model.Incident
	err := sc.Scan(&i.ID, &i.MissionID, &i.CrewID, &i.AlertID, &i.Trigger, &i.EventT, &i.WindowStart, &i.WindowEnd, &i.Status,
		&i.Classification, &i.FirstDeviation, &i.DetectionS, &i.ResponseS, &i.SamplesCount, &i.SHA256, &i.SealedAt)
	return i, err
}

// InsertIncident opens a black-box incident (status RECORDING).
func (s *Store) InsertIncident(i model.Incident) (int64, error) {
	res, err := s.W.Exec(`INSERT INTO incidents (mission_id, crew_id, alert_id, trigger, event_ts, window_start, window_end, status)
		VALUES (?,?,?,?,?,?,?, 'RECORDING')`, nullStr(i.MissionID), i.CrewID, i.AlertID, i.Trigger, i.EventT, i.WindowStart, i.WindowEnd)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// SealIncident stores the frozen data of an incident.
func (s *Store) SealIncident(i model.Incident, context7d, samples, timeline string) error {
	_, err := s.W.Exec(`UPDATE incidents SET status = 'SEALED', classification = ?, first_deviation_ts = ?, detection_s = ?, response_s = ?,
		context_7d = ?, samples = ?, timeline = ?, samples_count = ?, sha256 = ?, sealed_at = ? WHERE id = ?`,
		nullStr(i.Classification), i.FirstDeviation, i.DetectionS, i.ResponseS, context7d, samples, timeline, i.SamplesCount, i.SHA256, i.SealedAt, i.ID)
	return err
}

// Incident returns one incident with its frozen JSON blobs.
func (s *Store) Incident(id int64) (model.Incident, string, string, string, error) {
	var ctx, samples, timeline sql.NullString
	row := s.R.QueryRow(`SELECT `+incidentCols+`, context_7d, samples, timeline FROM incidents WHERE id = ?`, id)
	var i model.Incident
	err := row.Scan(&i.ID, &i.MissionID, &i.CrewID, &i.AlertID, &i.Trigger, &i.EventT, &i.WindowStart, &i.WindowEnd, &i.Status,
		&i.Classification, &i.FirstDeviation, &i.DetectionS, &i.ResponseS, &i.SamplesCount, &i.SHA256, &i.SealedAt, &ctx, &samples, &timeline)
	return i, ctx.String, samples.String, timeline.String, err
}

// Incidents lists incidents (newest first). status "" = any.
func (s *Store) Incidents(status string, limit int) ([]model.Incident, error) {
	q := `SELECT ` + incidentCols + ` FROM incidents`
	var args []any
	if status != "" {
		q += ` WHERE status = ?`
		args = append(args, status)
	}
	q += ` ORDER BY event_ts DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.R.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Incident
	for rows.Next() {
		i, err := scanIncident(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

/* =====================================================================
   Operator log
   ===================================================================== */

// Log appends an operator/system action.
func (s *Store) Log(missionID, actor, crewID, action, detail string) (model.OpLog, error) {
	e := model.OpLog{MissionID: missionID, T: Now(), Actor: actor, CrewID: crewID, Action: action, Detail: detail}
	res, err := s.W.Exec(`INSERT INTO oplog (mission_id, ts, actor, crew_id, action, detail) VALUES (?,?,?,?,?,?)`,
		nullStr(missionID), e.T, actor, nullStr(crewID), action, detail)
	if err == nil {
		e.ID, _ = res.LastInsertId()
	}
	return e, err
}

// OpLogs lists actions (newest first). crewID "" = all; from/to 0 = no bound.
func (s *Store) OpLogs(crewID string, from, to int64, limit int) ([]model.OpLog, error) {
	q := `SELECT id, COALESCE(mission_id,''), ts, actor, COALESCE(crew_id,''), action, detail FROM oplog WHERE 1=1`
	var args []any
	if crewID != "" {
		q += ` AND (crew_id = ? OR crew_id IS NULL)`
		args = append(args, crewID)
	}
	if from > 0 {
		q += ` AND ts >= ?`
		args = append(args, from)
	}
	if to > 0 {
		q += ` AND ts <= ?`
		args = append(args, to)
	}
	q += ` ORDER BY ts DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.R.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.OpLog
	for rows.Next() {
		var e model.OpLog
		if err := rows.Scan(&e.ID, &e.MissionID, &e.T, &e.Actor, &e.CrewID, &e.Action, &e.Detail); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

/* =====================================================================
   EVA summaries and monthly panel
   ===================================================================== */

// SaveSummary stores the post-EVA load of one astronaut.
func (s *Store) SaveSummary(x model.EVASummary) error {
	_, err := s.W.Exec(`INSERT OR REPLACE INTO eva_summary (mission_id, crew_id, duration_s, samples, hr_mean, hr_max, spo2_min, temp_max,
		trimp, kcal, dose_msv, peak_ipv, alerts, risk_s, recovery_h, ended_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		x.MissionID, x.CrewID, x.DurationS, x.Samples, x.HRMean, x.HRMax, x.SpO2Min, x.TempMax, x.TRIMP, x.Kcal, x.DoseMSv,
		x.PeakIPV, x.Alerts, x.RiskS, x.RecoveryH, x.EndedAt)
	return err
}

// Summaries returns post-EVA summaries of one crew member since a time (newest first).
func (s *Store) Summaries(crewID string, since int64) ([]model.EVASummary, error) {
	rows, err := s.R.Query(`SELECT mission_id, crew_id, duration_s, samples, hr_mean, hr_max, spo2_min, temp_max, trimp, kcal, dose_msv,
		peak_ipv, alerts, risk_s, recovery_h, ended_at FROM eva_summary WHERE crew_id = ? AND ended_at >= ? ORDER BY ended_at DESC`, crewID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.EVASummary
	for rows.Next() {
		var x model.EVASummary
		if err := rows.Scan(&x.MissionID, &x.CrewID, &x.DurationS, &x.Samples, &x.HRMean, &x.HRMax, &x.SpO2Min, &x.TempMax, &x.TRIMP,
			&x.Kcal, &x.DoseMSv, &x.PeakIPV, &x.Alerts, &x.RiskS, &x.RecoveryH, &x.EndedAt); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// SaveMonthly upserts only the non-psychological monthly clinical panel.
func (s *Store) SaveMonthly(m model.Monthly) error {
	if m.CrewID == "" {
		return errors.New("crew ID is required")
	}
	if _, err := time.Parse("2006-01", m.Month); err != nil {
		return errors.New("month must use YYYY-MM")
	}
	s.Flush()
	_, err := s.W.Exec(`INSERT INTO monthly_health
		(crew_id, month, avg_hr, avg_spo2, avg_sleep_h, avg_hrv, hemoglobin, wbc, platelets,
		 glucose_fasting, orthostatic_drop_sys, weight_kg, weight_change_pct, exercise_pct,
		 radiation_month_msv, mental_index)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(crew_id, month) DO UPDATE SET
		 avg_hr=COALESCE(excluded.avg_hr, monthly_health.avg_hr),
		 avg_spo2=COALESCE(excluded.avg_spo2, monthly_health.avg_spo2),
		 avg_sleep_h=COALESCE(excluded.avg_sleep_h, monthly_health.avg_sleep_h),
		 avg_hrv=COALESCE(excluded.avg_hrv, monthly_health.avg_hrv),
		 hemoglobin=COALESCE(excluded.hemoglobin, monthly_health.hemoglobin),
		 wbc=COALESCE(excluded.wbc, monthly_health.wbc),
		 platelets=COALESCE(excluded.platelets, monthly_health.platelets),
		 glucose_fasting=COALESCE(excluded.glucose_fasting, monthly_health.glucose_fasting),
		 orthostatic_drop_sys=COALESCE(excluded.orthostatic_drop_sys, monthly_health.orthostatic_drop_sys),
		 weight_kg=COALESCE(excluded.weight_kg, monthly_health.weight_kg),
		 weight_change_pct=COALESCE(excluded.weight_change_pct, monthly_health.weight_change_pct),
		 exercise_pct=COALESCE(excluded.exercise_pct, monthly_health.exercise_pct),
		 radiation_month_msv=COALESCE(excluded.radiation_month_msv, monthly_health.radiation_month_msv)`,
		m.CrewID, m.Month, m.AvgHR, m.AvgSpO2, m.AvgSleepH, m.AvgHRV, m.Hemoglobin, m.WBC, m.Platelets,
		m.Glucose, m.OrthostaticDrop, m.WeightKg, m.WeightChangePct, m.ExercisePct, m.RadiationMonthMS, nil)
	return err
}

// Monthly returns the clinical panel of a month (sql.ErrNoRows if none).
func (s *Store) Monthly(crewID, month string) (model.Monthly, error) {
	var m model.Monthly
	err := s.R.QueryRow(`SELECT crew_id, month, avg_hr, avg_spo2, avg_sleep_h, avg_hrv, hemoglobin, wbc, platelets, glucose_fasting,
		orthostatic_drop_sys, weight_kg, weight_change_pct, exercise_pct, radiation_month_msv, mental_index
		FROM monthly_health WHERE crew_id = ? AND month = ?`, crewID, month).Scan(&m.CrewID, &m.Month, &m.AvgHR, &m.AvgSpO2, &m.AvgSleepH,
		&m.AvgHRV, &m.Hemoglobin, &m.WBC, &m.Platelets, &m.Glucose, &m.OrthostaticDrop, &m.WeightKg, &m.WeightChangePct, &m.ExercisePct,
		&m.RadiationMonthMS, &m.MentalIndex)
	return m, err
}

// Months lists the months that have a clinical panel for a crew member (newest first).
func (s *Store) Months(crewID string) ([]string, error) {
	rows, err := s.R.Query(`SELECT month FROM monthly_health WHERE crew_id = ? ORDER BY month DESC`, crewID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var m string
		if err := rows.Scan(&m); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// TelemetryDailyMeans returns per-day means of hr, spo2 and hrv in [from, to].
func (s *Store) TelemetryDailyMeans(crewID string, from, to int64) (map[string][3]float64, error) {
	rows, err := s.R.Query(`SELECT date(ts / 1000, 'unixepoch', 'localtime') AS d, AVG(hr), AVG(spo2), AVG(hrv)
		FROM telemetry WHERE crew_id = ? AND ts BETWEEN ? AND ? GROUP BY d`, crewID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][3]float64{}
	for rows.Next() {
		var d string
		var hr, sp, hv sql.NullFloat64
		if err := rows.Scan(&d, &hr, &sp, &hv); err != nil {
			return nil, err
		}
		out[d] = [3]float64{hr.Float64, sp.Float64, hv.Float64}
	}
	return out, rows.Err()
}

/* =====================================================================
   CSV export
   ===================================================================== */

// Exportable tables (whitelist: the name is never taken from user input as SQL).
var Exportable = map[string]string{
	"telemetry":      `SELECT mission_id, crew_id, ts, datetime(ts/1000,'unixepoch','localtime') AS local_time, hr, spo2, temp, rr, hrv, rad_rate, effort, suit_p, o2_min, co2, batt, bp_sys, bp_dia, ipv, status, source, seq FROM telemetry ORDER BY ts`,
	"alerts":         `SELECT id, mission_id, crew_id, ts, datetime(ts/1000,'unixepoch','localtime') AS local_time, level, code, metric, voice, reason, protocol, response, responded_by, response_s, escalated, resolved_at FROM alerts ORDER BY ts`,
	"incidents":      `SELECT id, mission_id, crew_id, alert_id, trigger, event_ts, window_start, window_end, status, classification, first_deviation_ts, detection_s, response_s, samples_count, sha256, sealed_at FROM incidents ORDER BY event_ts`,
	"readiness":      `SELECT crew_id, day, status, vulnerability, summary, factors, sleep_h, hydration, fatigue_high, hr_rest, hrv_rest, received_at FROM readiness ORDER BY day`,
	"oplog":          `SELECT id, mission_id, ts, datetime(ts/1000,'unixepoch','localtime') AS local_time, actor, crew_id, action, detail FROM oplog ORDER BY ts`,
	"missions":       `SELECT id, name, environment, site, planned_h, status, started_at, ended_at FROM missions ORDER BY created_at`,
	"crew":           `SELECT id, name, role, specialty, sex, age, height_cm, weight_kg, blood_type, radiation_budget_msv, radiation_accum_msv FROM crew ORDER BY id`,
	"eva_summary":    `SELECT * FROM eva_summary ORDER BY ended_at`,
	"monthly_health": `SELECT * FROM monthly_health ORDER BY crew_id, month`,
	"blackbox_chain": `SELECT id, ts, kind, crew_id, ref, payload, prev_hash, hash FROM blackbox_chain ORDER BY id`,
}

// ExportRows runs an exportable query and returns header + rows as strings.
func (s *Store) ExportRows(table string) ([]string, [][]string, error) {
	q, ok := Exportable[table]
	if !ok {
		return nil, nil, errors.New("unknown table")
	}
	s.Flush()
	rows, err := s.R.Query(q)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	var out [][]string
	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return nil, nil, err
		}
		rec := make([]string, len(cols))
		for i, v := range vals {
			switch x := v.(type) {
			case nil:
				rec[i] = ""
			case []byte:
				rec[i] = string(x)
			case float64:
				rec[i] = strconv.FormatFloat(x, 'f', -1, 64)
			default:
				rec[i] = strings.TrimSpace(fmt.Sprint(x))
			}
		}
		out = append(out, rec)
	}
	return cols, out, rows.Err()
}
