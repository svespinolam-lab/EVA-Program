package server

// Operator access control.
//
// EVA Station serves two audiences on the same port:
//
//   - explorers' phones (any device on the mission Wi-Fi): /app/, /shared/,
//     /ws/device and the public liveness probe /api/health;
//   - the cabin operator: the PC program (/), /ws/console, /api/... and
//     /reports/... (mission control, medical records, exports).
//
// Operator routes are trusted only from this computer (loopback or one of its
// own addresses). Any other device must present the operator token: open
// /?operator=TOKEN once (sets an HttpOnly, SameSite=Strict cookie) or send
// "Authorization: Bearer TOKEN". State-changing requests and the console
// WebSocket must also be same-origin, so a page from another site cannot drive
// the station through the operator's own browser (CSRF / WebSocket hijacking).

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"html"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

const (
	operatorCookie = "eva_operator"
	maxBodyBytes   = 1 << 20 // 1 MiB per API request
)

// LoadOperatorToken returns the operator token, in order of preference: the
// explicit value (flag), EVA_OPERATOR_TOKEN, or <dataDir>/operator.token —
// created with a random 128-bit value on first start so it stays stable
// across restarts.
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

func (s *Server) validToken(t string) bool {
	return s.OperatorToken != "" && t != "" &&
		subtle.ConstantTimeCompare([]byte(t), []byte(s.OperatorToken)) == 1
}

// isOperator: this PC, a valid bearer token, or a valid operator cookie.
func (s *Server) isOperator(r *http.Request) bool {
	if isLocal(r) {
		return true
	}
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") && s.validToken(strings.TrimSpace(h[7:])) {
		return true
	}
	if c, err := r.Cookie(operatorCookie); err == nil && s.validToken(c.Value) {
		return true
	}
	return false
}

// sameOrigin rejects browser requests coming from another site. Clients that
// send neither Origin nor Sec-Fetch-Site (curl, scripts, tests) are allowed;
// they still need operator access.
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

// operator guards an operator-only handler.
func (s *Server) operator(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.isOperator(r) {
			s.deny(w, r)
			return
		}
		if (r.Method != http.MethodGet && r.Method != http.MethodHead) || isUpgrade(r) {
			if !sameOrigin(r) {
				writeJSON(w, http.StatusForbidden, map[string]any{"error": "cross-origin request blocked"})
				return
			}
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		}
		h(w, r)
	}
}

// operatorPage guards the PC program (static files under /). Opening
// /?operator=TOKEN with a valid token stores it in a cookie and reloads /.
func (s *Server) operatorPage(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if t := r.URL.Query().Get("operator"); t != "" && s.validToken(t) {
			http.SetCookie(w, &http.Cookie{Name: operatorCookie, Value: t, Path: "/", HttpOnly: true,
				SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil, MaxAge: 12 * 3600})
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		if !s.isOperator(r) {
			s.deny(w, r)
			return
		}
		h.ServeHTTP(w, r)
	})
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
		`or use the operator link shown in the EVA Station window.</p>` +
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
