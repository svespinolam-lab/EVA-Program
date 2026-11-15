package server

// Access control: who is calling, what their role allows, and the audit log.
//
// EVA Station serves two audiences on the same port:
//
//   - explorers' phones: /app/, /shared/, /ws/device (paired devices only,
//     see pairing.go) and the public liveness probe /api/health;
//   - people at the console: the PC program (/), /ws/console, /api/... and
//     /reports/....
//
// A console request is authorised as:
//
//   - the station PC itself (loopback or one of its own addresses): built-in
//     operator, so the cabin console needs no password;
//   - the bootstrap operator token (data/operator.token, printed at start);
//   - a personal user token (users table) with that user's role.
//
// Tokens arrive once as /?operator=TOKEN (stored in an HttpOnly,
// SameSite=Strict cookie) or as "Authorization: Bearer TOKEN".
// Permissions: view, medical, respond, command, admin (roles table).
// State-changing requests and the console WebSocket must be same-origin
// (CSRF / WebSocket hijacking guard). Changes and clinical reads are audited.

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"html"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"eva/internal/store"
)

const (
	operatorCookie = "eva_operator"
	maxBodyBytes   = 1 << 20 // 1 MiB per API request
)

// Permissions.
const (
	permView    = "view"    // live triage, black box, mission state
	permMedical = "medical" // clinical records, exports, reports
	permRespond = "respond" // acknowledge alerts, instructions, incidents
	permCommand = "command" // mission control, simulator, crew and mission records
	permAdmin   = "admin"   // users, devices, backups, audit log
)

var allPerms = map[string]bool{permView: true, permMedical: true, permRespond: true, permCommand: true, permAdmin: true}

// LoadOperatorToken returns the bootstrap operator token, in order of
// preference: the explicit value (flag), EVA_OPERATOR_TOKEN, or
// <dataDir>/operator.token — created with a random 128-bit value on first
// start so it stays stable across restarts.
func LoadOperatorToken(dataDir, explicit string) (string, error) {
	if t := strings.TrimSpace(explicit); t != "" {
		return t, nil
	}
	if t := strings.TrimSpace(os.Getenv("EVA_OPERATOR_TOKEN")); t != "" {
		return t, nil
	}
	path := filepath.Join(dataDir, "operator.token")
	if b, err := os.ReadFile(path); err == nil {
		if t := strings.TrimSpace(string(b)); len(t) >= 16 {
			return t, nil
		}
	}
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	t := hex.EncodeToString(raw)
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(t+"\n"), 0o600); err != nil {
		return "", err
	}
	return t, nil
}

// isLocal reports whether the request comes from this computer.
func isLocal(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	if ip.IsLoopback() {
		return true
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.Equal(ip) {
			return true
		}
	}
	return false
}

func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// principal is the authenticated caller of a console request.
type principal struct {
	Actor string          `json:"actor"` // "station", "operator-token" or a user id
	Name  string          `json:"name"`
	Role  string          `json:"role"`
	Local bool            `json:"local"`
	perms map[string]bool `json:"-"`
}

func (p principal) can(perm string) bool { return p.perms[perm] }

// Permissions returns the sorted permission names (for /api/auth/me).
func (p principal) Permissions() []string {
	out := []string{}
	for _, k := range []string{permView, permMedical, permRespond, permCommand, permAdmin} {
		if p.perms[k] {
			out = append(out, k)
		}
	}
	return out
}

func (s *Server) validToken(t string) bool {
	return s.OperatorToken != "" && t != "" &&
		subtle.ConstantTimeCompare([]byte(t), []byte(s.OperatorToken)) == 1
}

// rolePerms returns the permission set of a role (roles table, cached).
func (s *Server) rolePerms(role string) map[string]bool {
	s.rolesOnce.Do(func() {
		s.roles = map[string]map[string]bool{}
		if rs, err := s.St.Roles(); err == nil {
			for name, ps := range rs {
				m := map[string]bool{}
				for _, p := range ps {
					m[strings.TrimSpace(p)] = true
				}
				s.roles[name] = m
			}
		}
	})
	return s.roles[role]
}

func requestToken(r *http.Request) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(h[7:])
	}
	if c, err := r.Cookie(operatorCookie); err == nil {
		return c.Value
	}
	return ""
}

// authenticate resolves the caller from a token (no local shortcut).
func (s *Server) authenticate(token string) (principal, bool) {
	if token == "" {
		return principal{}, false
	}
	if s.validToken(token) {
		return principal{Actor: "operator-token", Name: "Operator (station token)", Role: "operator", perms: allPerms}, true
	}
	if s.St != nil {
		if u, err := s.St.UserByToken(token); err == nil {
			return principal{Actor: u.ID, Name: u.Name, Role: u.Role, perms: s.rolePerms(u.Role)}, true
		}
	}
	return principal{}, false
}

// principalOf resolves the caller of a console request.
func (s *Server) principalOf(r *http.Request) (principal, bool) {
	if isLocal(r) {
		return principal{Actor: "station", Name: "Station PC", Role: "operator", Local: true, perms: allPerms}, true
	}
	return s.authenticate(requestToken(r))
}

type ctxKey struct{}

// who returns the principal attached by need (station if absent, e.g. tests).
func who(r *http.Request) principal {
	if p, ok := r.Context().Value(ctxKey{}).(principal); ok {
		return p
	}
	return principal{Actor: "station", Role: "operator", perms: allPerms}
}

// sameOrigin rejects browser requests coming from another site. Clients that
// send neither Origin nor Sec-Fetch-Site (curl, scripts, tests) are allowed;
// they still need to be authorised.
func sameOrigin(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "", "same-origin", "none":
	default:
		return false
	}
	o := r.Header.Get("Origin")
	if o == "" {
		return true
	}
	u, err := url.Parse(o)
	if err != nil || u.Host == "" {
		return false // includes the opaque "null" origin
	}
	return strings.EqualFold(u.Host, r.Host)
}

func isUpgrade(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
}

// need guards a console handler with one permission. Changes and clinical
// or administrative reads are written to the audit log.
func (s *Server) need(perm string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := s.principalOf(r)
		if !ok {
			s.audit(r, principal{Actor: "anonymous", Role: "-"}, http.StatusUnauthorized)
			s.deny(w, r)
			return
		}
		mutating := r.Method != http.MethodGet && r.Method != http.MethodHead
		if (mutating || isUpgrade(r)) && !sameOrigin(r) {
			s.audit(r, p, http.StatusForbidden)
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "cross-origin request blocked"})
			return
		}
		if !p.can(perm) {
			s.audit(r, p, http.StatusForbidden)
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "your role (" + p.Role + ") does not allow this action"})
			return
		}
		if mutating && r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		}
		r = r.WithContext(context.WithValue(r.Context(), ctxKey{}, p))
		if !mutating && perm != permMedical && perm != permAdmin {
			h(w, r) // routine reads (live triage, state) are not audited
			return
		}
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		h(rec, r)
		s.audit(r, p, rec.status)
	}
}

func (s *Server) audit(r *http.Request, p principal, status int) {
	if s.St == nil {
		return
	}
	s.St.Audit(store.AuditEntry{Actor: p.Actor, Role: p.Role, IP: remoteIP(r), Action: r.Method + " " + r.URL.Path, Result: status})
}

// statusRecorder captures the response status for the audit log.
type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (w *statusRecorder) WriteHeader(code int) {
	if !w.wrote {
		w.status, w.wrote = code, true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusRecorder) Write(b []byte) (int, error) {
	w.wrote = true
	return w.ResponseWriter.Write(b)
}

func (w *statusRecorder) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := w.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, errors.New("hijack not supported")
}

// operatorPage guards the PC program (static files under /). Opening
// /?operator=TOKEN (or ?token=) with any valid console token stores it in a
// cookie and reloads /.
func (s *Server) operatorPage(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t := r.URL.Query().Get("operator")
		if t == "" {
			t = r.URL.Query().Get("token")
		}
		if t != "" {
			if p, ok := s.authenticate(t); ok {
				http.SetCookie(w, &http.Cookie{Name: operatorCookie, Value: t, Path: "/", HttpOnly: true,
					SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil, MaxAge: 12 * 3600})
				s.audit(r, p, http.StatusSeeOther)
				http.Redirect(w, r, "/", http.StatusSeeOther)
				return
			}
			s.audit(r, principal{Actor: "anonymous", Role: "-"}, http.StatusUnauthorized)
		}
		p, ok := s.principalOf(r)
		if !ok || !p.can(permView) {
			s.deny(w, r)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// logout clears the console cookie.
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: operatorCookie, Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	writeJSON(w, 200, map[string]any{"ok": true})
}

// deny answers JSON to API / WebSocket clients and a short page to browsers.
func (s *Server) deny(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/ws/") {
		writeJSON(w, http.StatusUnauthorized, map[string]any{
			"error": "operator access required: use the station PC or the operator link shown in the EVA Station window"})
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`<!doctype html><html lang="en" translate="no"><head><meta charset="utf-8">` +
		`<meta name="viewport" content="width=device-width, initial-scale=1"><title>EVA Station · Operator access</title>` +
		`<style>body{font:16px/1.5 system-ui,sans-serif;max-width:34rem;margin:12vh auto;padding:0 16px;color:#061A36;background:#F2F4F7}` +
		`h1{font-size:1.4rem;color:#0B3D91}a{color:#0B3D91;font-weight:600}</style></head><body>` +
		`<h1>EVA Station · operator access only</h1>` +
		`<p>This console controls the mission and holds medical records. Open it on the station PC, ` +
		`or use the operator link shown in the EVA Station window (or your personal access link).</p>` +
		`<p>Explorers: open the <a href="/app/">EVA app</a>.</p>` +
		`<p style="color:#5B6573;font-size:.85rem">` + html.EscapeString(r.URL.Path) + `</p></body></html>`))
}

// securityHeaders applies to every response.
func securityHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hd := w.Header()
		hd.Set("X-Content-Type-Options", "nosniff")
		hd.Set("Referrer-Policy", "no-referrer") // never leak ?operator=… through Referer
		hd.Set("X-Frame-Options", "DENY")
		h.ServeHTTP(w, r)
	})
}
