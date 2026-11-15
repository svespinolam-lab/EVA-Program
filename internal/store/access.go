package store

// Data access for the phase-4 tables: console users and roles, security
// audit, paired devices, the phone outbox, protocols, mission crew, sensors,
// emergency contacts, alert responses and crew records. Secrets (user and
// device tokens) are never stored: only their SHA-256.

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"

	"eva/internal/model"
)

// ErrNotFound is returned when a record does not exist (or is not visible).
var ErrNotFound = errors.New("not found")

// NewSecret returns n random bytes as hex (tokens, ids).
func NewSecret(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand never fails on supported platforms
	}
	return hex.EncodeToString(b)
}

// HashToken is the stored form of a token.
func HashToken(t string) string {
	h := sha256.Sum256([]byte(t))
	return hex.EncodeToString(h[:])
}

func nullInt(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

/* ---------- roles and users ---------- */

// Roles returns role name -> permissions (from the roles table).
func (s *Store) Roles() (map[string][]string, error) {
	rows, err := s.R.Query(`SELECT name, permissions FROM roles`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var name, perms string
		if err := rows.Scan(&name, &perms); err != nil {
			return nil, err
		}
		out[name] = strings.Split(perms, ",")
	}
	return out, rows.Err()
}

// User is a console user (a person). The token is shown only once, at creation.
type User struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Role      string `json:"role"`
	CreatedAt int64  `json:"createdAt"`
	CreatedBy string `json:"createdBy"`
	Disabled  bool   `json:"disabled"`
}

// CreateUser adds a user and returns its one-time token.
func (s *Store) CreateUser(name, role, by string) (User, string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 80 {
		return User{}, "", errors.New("name must be 1-80 characters")
	}
	roles, err := s.Roles()
	if err != nil {
		return User{}, "", err
	}
	if _, ok := roles[role]; !ok {
		return User{}, "", errors.New("unknown role")
	}
	u := User{ID: "usr_" + NewSecret(6), Name: name, Role: role, CreatedAt: Now(), CreatedBy: by}
	token := "eva_" + NewSecret(20)
	_, err = s.W.Exec(`INSERT INTO users (id, name, role, token_hash, created_at, created_by) VALUES (?,?,?,?,?,?)`,
		u.ID, u.Name, u.Role, HashToken(token), u.CreatedAt, by)
	return u, token, err
}

// UserByToken finds an enabled user by token.
func (s *Store) UserByToken(token string) (User, error) {
	var u User
	var disabled sql.NullInt64
	err := s.R.QueryRow(`SELECT id, name, role, created_at, created_by, disabled_at FROM users WHERE token_hash = ?`, HashToken(token)).
		Scan(&u.ID, &u.Name, &u.Role, &u.CreatedAt, &u.CreatedBy, &disabled)
	if err != nil || disabled.Valid {
		return User{}, ErrNotFound
	}
	return u, nil
}

// Users lists every user (tokens are never returned).
func (s *Store) Users() ([]User, error) {
	rows, err := s.R.Query(`SELECT id, name, role, created_at, created_by, disabled_at FROM users ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		var u User
		var disabled sql.NullInt64
		if err := rows.Scan(&u.ID, &u.Name, &u.Role, &u.CreatedAt, &u.CreatedBy, &disabled); err != nil {
			return nil, err
		}
		u.Disabled = disabled.Valid
		out = append(out, u)
	}
	return out, rows.Err()
}

// DisableUser revokes a user's access (the record is kept for the audit).
func (s *Store) DisableUser(id string) error {
	res, err := s.W.Exec(`UPDATE users SET disabled_at = ? WHERE id = ? AND disabled_at IS NULL`, Now(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

/* ---------- security audit ---------- */

// AuditEntry is one security-relevant action.
type AuditEntry struct {
	ID     int64  `json:"id"`
	T      int64  `json:"t"`
	Actor  string `json:"actor"`
	Role   string `json:"role"`
	IP     string `json:"ip,omitempty"`
	Action string `json:"action"`
	Target string `json:"target,omitempty"`
	Result int    `json:"result"`
}

// Audit records an entry (errors are ignored: auditing must never block).
func (s *Store) Audit(e AuditEntry) {
	if e.T == 0 {
		e.T = Now()
	}
	_, _ = s.W.Exec(`INSERT INTO audit_log (ts, actor, role, ip, action, target, result) VALUES (?,?,?,?,?,?,?)`,
		e.T, e.Actor, e.Role, nullStr(e.IP), e.Action, nullStr(e.Target), e.Result)
}

// AuditLog returns the newest entries first.
func (s *Store) AuditLog(limit int) ([]AuditEntry, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := s.R.Query(`SELECT id, ts, actor, role, COALESCE(ip,''), action, COALESCE(target,''), result FROM audit_log ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuditEntry{}
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.T, &e.Actor, &e.Role, &e.IP, &e.Action, &e.Target, &e.Result); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

/* ---------- paired devices ---------- */

// Device is a paired phone.
type Device struct {
	ID       string `json:"id"`
	CrewID   string `json:"crewId"`
	Name     string `json:"name"`
	PairedAt int64  `json:"pairedAt"`
	PairedBy string `json:"pairedBy"`
	LastSeen int64  `json:"lastSeen,omitempty"`
	LastSeq  int64  `json:"lastSeq,omitempty"`
	Revoked  bool   `json:"revoked"`
}

// CreateDevice pairs a phone with a crew member and returns its token.
func (s *Store) CreateDevice(crewID, name, by string) (Device, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Phone"
	}
	if len([]rune(name)) > 60 {
		name = string([]rune(name)[:60])
	}
	d := Device{ID: "dev_" + NewSecret(6), CrewID: crewID, Name: name, PairedAt: Now(), PairedBy: by}
	token := "evd_" + NewSecret(20)
	_, err := s.W.Exec(`INSERT INTO devices (id, crew_id, name, token_hash, paired_at, paired_by) VALUES (?,?,?,?,?,?)`,
		d.ID, d.CrewID, d.Name, HashToken(token), d.PairedAt, by)
	return d, token, err
}

const deviceCols = `id, crew_id, name, paired_at, paired_by, COALESCE(last_seen,0), COALESCE(last_seq,0), revoked_at IS NOT NULL`

func scanDevice(sc interface{ Scan(...any) error }) (Device, error) {
	var d Device
	err := sc.Scan(&d.ID, &d.CrewID, &d.Name, &d.PairedAt, &d.PairedBy, &d.LastSeen, &d.LastSeq, &d.Revoked)
	return d, err
}

// DeviceByToken authenticates a phone: id and token must match and the
// device must not be revoked.
func (s *Store) DeviceByToken(id, token string) (Device, error) {
	d, err := scanDevice(s.R.QueryRow(`SELECT `+deviceCols+` FROM devices WHERE id = ? AND token_hash = ?`, id, HashToken(token)))
	if err != nil || d.Revoked {
		return Device{}, ErrNotFound
	}
	return d, nil
}

// Devices lists every paired phone, newest first.
func (s *Store) Devices() ([]Device, error) {
	rows, err := s.R.Query(`SELECT ` + deviceCols + ` FROM devices ORDER BY paired_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Device{}
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// RevokeDevice withdraws a phone's access; it returns the crew it belonged to.
func (s *Store) RevokeDevice(id string) (string, error) {
	var crew string
	if err := s.R.QueryRow(`SELECT crew_id FROM devices WHERE id = ? AND revoked_at IS NULL`, id).Scan(&crew); err != nil {
		return "", ErrNotFound
	}
	_, err := s.W.Exec(`UPDATE devices SET revoked_at = ? WHERE id = ?`, Now(), id)
	return crew, err
}

// TouchDevice records when a phone was last seen and, if seq > 0, the
// highest telemetry sequence stored for it (sync state).
func (s *Store) TouchDevice(id string, seen, seq int64) {
	_, _ = s.W.Exec(`UPDATE devices SET last_seen = MAX(COALESCE(last_seen,0), ?), last_seq = CASE WHEN ? > COALESCE(last_seq,0) THEN ? ELSE last_seq END WHERE id = ?`,
		seen, seq, seq, id)
}

/* ---------- phone outbox ---------- */

// OutboxMsg is a message waiting for a phone to confirm it.
type OutboxMsg struct {
	ID      int64  `json:"id"`
	CrewID  string `json:"crewId"`
	Type    string `json:"type"`
	Payload string `json:"payload"`
	T       int64  `json:"t"`
	Expires int64  `json:"expires"`
}

// EnqueueOutbox stores a message for a crew member's phone.
func (s *Store) EnqueueOutbox(crewID, typ, payload string, ttlMs int64) (int64, error) {
	now := Now()
	res, err := s.W.Exec(`INSERT INTO device_outbox (crew_id, type, payload, created_at, expires_at) VALUES (?,?,?,?,?)`,
		crewID, typ, payload, now, now+ttlMs)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// SetOutboxPayload rewrites the stored payload (once the message id is known).
func (s *Store) SetOutboxPayload(id int64, payload string) {
	_, _ = s.W.Exec(`UPDATE device_outbox SET payload = ? WHERE id = ?`, payload, id)
}

// PendingOutbox returns undelivered, unexpired messages for a crew member, oldest first.
func (s *Store) PendingOutbox(crewID string) ([]OutboxMsg, error) {
	rows, err := s.R.Query(`SELECT id, crew_id, type, payload, created_at, expires_at FROM device_outbox
		WHERE crew_id = ? AND delivered_at IS NULL AND expires_at > ? ORDER BY id`, crewID, Now())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []OutboxMsg{}
	for rows.Next() {
		var m OutboxMsg
		if err := rows.Scan(&m.ID, &m.CrewID, &m.Type, &m.Payload, &m.T, &m.Expires); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// MarkDelivered confirms a message; only the addressee's phone can do it.
func (s *Store) MarkDelivered(crewID string, id int64) bool {
	res, err := s.W.Exec(`UPDATE device_outbox SET delivered_at = ? WHERE id = ? AND crew_id = ? AND delivered_at IS NULL`, Now(), id, crewID)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n == 1
}

// OutboxPendingCount counts undelivered, unexpired messages per crew member.
func (s *Store) OutboxPendingCount() (map[string]int, error) {
	rows, err := s.R.Query(`SELECT crew_id, COUNT(*) FROM device_outbox WHERE delivered_at IS NULL AND expires_at > ? GROUP BY crew_id`, Now())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var c string
		var n int
		if err := rows.Scan(&c, &n); err != nil {
			return nil, err
		}
		out[c] = n
	}
	return out, rows.Err()
}

/* ---------- protocols ---------- */

// UpsertBuiltinProtocol mirrors a rule-based protocol; rows edited by the
// medical team (source 'medical') are never overwritten.
func (s *Store) UpsertBuiltinProtocol(code string, level int, stepsJSON string) {
	_, _ = s.W.Exec(`INSERT INTO protocols (code, level, steps, source, updated_at) VALUES (?,?,?,'builtin',?)
		ON CONFLICT(code, level) DO UPDATE SET steps = excluded.steps, updated_at = excluded.updated_at WHERE protocols.source = 'builtin'`,
		code, level, stepsJSON, Now())
}

// Protocol is one catalogue entry.
type Protocol struct {
	Code   string `json:"code"`
	Level  int    `json:"level"`
	Steps  string `json:"steps"` // JSON array
	Source string `json:"source"`
}

// Protocols returns the catalogue.
func (s *Store) Protocols() ([]Protocol, error) {
	rows, err := s.R.Query(`SELECT code, level, steps, source FROM protocols ORDER BY code, level`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Protocol{}
	for rows.Next() {
		var p Protocol
		if err := rows.Scan(&p.Code, &p.Level, &p.Steps, &p.Source); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

/* ---------- alert responses ---------- */

// AlertResponse is one answer to an alert.
type AlertResponse struct {
	AlertID  int64  `json:"alertId"`
	T        int64  `json:"t"`
	Response string `json:"response"`
	By       string `json:"by"`
	Actor    string `json:"actor,omitempty"`
}

// AddAlertResponse appends to the response history.
func (s *Store) AddAlertResponse(r AlertResponse) {
	_, _ = s.W.Exec(`INSERT INTO alert_responses (alert_id, ts, response, by_kind, actor) VALUES (?,?,?,?,?)`,
		r.AlertID, r.T, r.Response, r.By, nullStr(r.Actor))
}

// AlertResponses returns the history of one alert, oldest first.
func (s *Store) AlertResponses(alertID int64) ([]AlertResponse, error) {
	rows, err := s.R.Query(`SELECT alert_id, ts, response, by_kind, COALESCE(actor,'') FROM alert_responses WHERE alert_id = ? ORDER BY id`, alertID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AlertResponse{}
	for rows.Next() {
		var r AlertResponse
		if err := rows.Scan(&r.AlertID, &r.T, &r.Response, &r.By, &r.Actor); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

/* ---------- crew records ---------- */

// ValidateCrew checks a crew record before it is stored.
func ValidateCrew(c model.Crew) error {
	switch {
	case !strings.HasPrefix(c.ID, "AST-") || len(c.ID) < 5 || len(c.ID) > 12:
		return errors.New("id must look like AST-004")
	case strings.TrimSpace(c.Name) == "" || len([]rune(c.Name)) > 80:
		return errors.New("name must be 1-80 characters")
	case c.Role != "EV1" && c.Role != "EV2" && c.Role != "EV3" && c.Role != "IV":
		return errors.New("role must be EV1, EV2, EV3 or IV")
	case c.Sex != "F" && c.Sex != "M":
		return errors.New("sex (biological, used by clinical formulas) must be F or M")
	case c.Age < 18 || c.Age > 80:
		return errors.New("age must be 18-80")
	case c.HeightCm != 0 && (c.HeightCm < 120 || c.HeightCm > 230):
		return errors.New("height must be 120-230 cm")
	case c.WeightKg != 0 && (c.WeightKg < 35 || c.WeightKg > 200):
		return errors.New("weight must be 35-200 kg")
	}
	return nil
}

// UpsertCrew inserts or updates a crew record (radiation history is kept).
func (s *Store) UpsertCrew(c model.Crew) error {
	if err := ValidateCrew(c); err != nil {
		return err
	}
	if c.Initials == "" {
		for _, w := range strings.Fields(c.Name) {
			if len(c.Initials) < 2 {
				c.Initials += strings.ToUpper(string([]rune(w)[0]))
			}
		}
	}
	if c.RadBudget <= 0 {
		c.RadBudget = 600
	}
	_, err := s.W.Exec(`INSERT INTO crew (id, name, initials, role, specialty, sex, age, height_cm, weight_kg, blood_type, conditions, allergies, medication, radiation_budget_msv, radiation_accum_msv, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,0,?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name, initials=excluded.initials, role=excluded.role, specialty=excluded.specialty, sex=excluded.sex,
		  age=excluded.age, height_cm=excluded.height_cm, weight_kg=excluded.weight_kg, blood_type=excluded.blood_type, conditions=excluded.conditions,
		  allergies=excluded.allergies, medication=excluded.medication, radiation_budget_msv=excluded.radiation_budget_msv`,
		c.ID, strings.TrimSpace(c.Name), c.Initials, c.Role, c.Specialty, c.Sex, c.Age, c.HeightCm, c.WeightKg, c.BloodType, c.Conditions,
		c.Allergies, c.Medication, c.RadBudget, Now())
	return err
}

// Contact is an emergency contact of a crew member.
type Contact struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Relation string `json:"relation,omitempty"`
	Channel  string `json:"channel,omitempty"`
}

// Contacts lists a crew member's emergency contacts.
func (s *Store) Contacts(crewID string) ([]Contact, error) {
	rows, err := s.R.Query(`SELECT id, name, COALESCE(relation,''), COALESCE(channel,'') FROM emergency_contacts WHERE crew_id = ? ORDER BY id`, crewID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Contact{}
	for rows.Next() {
		var c Contact
		if err := rows.Scan(&c.ID, &c.Name, &c.Relation, &c.Channel); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SetContacts replaces a crew member's emergency contacts.
func (s *Store) SetContacts(crewID string, cs []Contact) error {
	if len(cs) > 10 {
		return errors.New("at most 10 contacts")
	}
	tx, err := s.W.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM emergency_contacts WHERE crew_id = ?`, crewID); err != nil {
		_ = tx.Rollback()
		return err
	}
	for _, c := range cs {
		if strings.TrimSpace(c.Name) == "" {
			_ = tx.Rollback()
			return errors.New("every contact needs a name")
		}
		if _, err := tx.Exec(`INSERT INTO emergency_contacts (crew_id, name, relation, channel) VALUES (?,?,?,?)`,
			crewID, strings.TrimSpace(c.Name), c.Relation, c.Channel); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

/* ---------- mission crew ---------- */

// Assignment is a crew member's role in one mission.
type Assignment struct {
	CrewID string `json:"crewId"`
	Role   string `json:"role"`
}

// SetMissionCrew replaces the crew of a mission.
func (s *Store) SetMissionCrew(missionID string, as []Assignment) error {
	seen := map[string]bool{}
	for _, a := range as {
		if a.Role != "EV1" && a.Role != "EV2" && a.Role != "EV3" && a.Role != "IV" {
			return errors.New("role must be EV1, EV2, EV3 or IV") // same set as the crew table
		}
		if seen[a.CrewID] {
			return errors.New("crew member assigned twice")
		}
		seen[a.CrewID] = true
	}
	tx, err := s.W.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM mission_crew WHERE mission_id = ?`, missionID); err != nil {
		_ = tx.Rollback()
		return err
	}
	for _, a := range as {
		if _, err := tx.Exec(`INSERT INTO mission_crew (mission_id, crew_id, role) VALUES (?,?,?)`, missionID, a.CrewID, a.Role); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// MissionCrew returns the crew assigned to a mission.
func (s *Store) MissionCrew(missionID string) ([]Assignment, error) {
	rows, err := s.R.Query(`SELECT crew_id, role FROM mission_crew WHERE mission_id = ? ORDER BY role`, missionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Assignment{}
	for rows.Next() {
		var a Assignment
		if err := rows.Scan(&a.CrewID, &a.Role); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

/* ---------- sensors ---------- */

// Sensor is a registered measuring device of a crew member.
type Sensor struct {
	ID        string   `json:"id"`
	CrewID    string   `json:"crewId"`
	Kind      string   `json:"kind"`
	Model     string   `json:"model,omitempty"`
	Interface string   `json:"interface"`
	Profile   string   `json:"profile,omitempty"`
	Status    string   `json:"status"`
	Battery   *float64 `json:"batteryPct,omitempty"`
	LastSeen  int64    `json:"lastSeen,omitempty"`
}

// AddSensor registers a sensor (the CHECK constraints validate kind, interface and status).
func (s *Store) AddSensor(x Sensor) (Sensor, error) {
	x.ID = "sen_" + NewSecret(6)
	if x.Status == "" {
		x.Status = "registered"
	}
	_, err := s.W.Exec(`INSERT INTO sensors (id, crew_id, kind, model, interface, profile, status, battery_pct, created_at) VALUES (?,?,?,?,?,?,?,?,?)`,
		x.ID, x.CrewID, x.Kind, nullStr(x.Model), x.Interface, nullStr(x.Profile), x.Status, x.Battery, Now())
	return x, err
}

// UpdateSensor changes status and/or battery and marks it seen now.
func (s *Store) UpdateSensor(id, status string, battery *float64) error {
	res, err := s.W.Exec(`UPDATE sensors SET status = COALESCE(NULLIF(?,''), status), battery_pct = COALESCE(?, battery_pct), last_seen = ? WHERE id = ?`,
		status, battery, Now(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Sensors lists sensors, optionally for one crew member.
func (s *Store) Sensors(crewID string) ([]Sensor, error) {
	rows, err := s.R.Query(`SELECT id, crew_id, kind, COALESCE(model,''), interface, COALESCE(profile,''), status, battery_pct, COALESCE(last_seen,0)
		FROM sensors WHERE (? = '' OR crew_id = ?) ORDER BY crew_id, kind`, crewID, crewID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Sensor{}
	for rows.Next() {
		var x Sensor
		var b sql.NullFloat64
		if err := rows.Scan(&x.ID, &x.CrewID, &x.Kind, &x.Model, &x.Interface, &x.Profile, &x.Status, &b, &x.LastSeen); err != nil {
			return nil, err
		}
		if b.Valid {
			v := b.Float64
			x.Battery = &v
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

/* ---------- black-box search ---------- */

// ChainFilter narrows a black-box listing.
type ChainFilter struct {
	CrewID string
	Kind   string
	From   int64
	To     int64
	Limit  int
}

// ChainSearch returns matching black-box entries, newest first.
func (s *Store) ChainSearch(f ChainFilter) ([]model.ChainEntry, error) {
	if f.Limit <= 0 || f.Limit > 1000 {
		f.Limit = 150
	}
	if f.To <= 0 {
		f.To = 1<<62 - 1
	}
	rows, err := s.R.Query(`SELECT id, ts, kind, COALESCE(crew_id,''), COALESCE(ref,''), payload, prev_hash, hash FROM blackbox_chain
		WHERE (? = '' OR crew_id = ?) AND (? = '' OR kind = ?) AND ts BETWEEN ? AND ? ORDER BY id DESC LIMIT ?`,
		f.CrewID, f.CrewID, f.Kind, f.Kind, f.From, f.To, f.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.ChainEntry{}
	for rows.Next() {
		var e model.ChainEntry
		if err := rows.Scan(&e.ID, &e.T, &e.Kind, &e.CrewID, &e.Ref, &e.Payload, &e.PrevHash, &e.Hash); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

var _ = nullInt // reserved for optional integer columns
