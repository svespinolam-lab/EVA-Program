package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const (
	phone = "192.0.2.10:51000" // another device on the mission Wi-Fi (TEST-NET-1)
	local = "127.0.0.1:51000"  // the station PC itself
)

func call(h http.Handler, method, target, remote string, hdr map[string]string, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	r.RemoteAddr = remote
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestPhonesCannotUseOperatorRoutes(t *testing.T) {
	h := newTestServer(t).Handler()
	for _, target := range []string{"/api/state", "/api/export/telemetry", "/api/blackbox", "/reports/monthly/AST-001", "/ws/console", "/"} {
		if w := call(h, "GET", target, phone, nil, ""); w.Code != http.StatusUnauthorized {
			t.Errorf("phone GET %s = %d, want 401", target, w.Code)
		}
	}
	if w := call(h, "POST", "/api/mission/abort", phone, nil, "{}"); w.Code != http.StatusUnauthorized {
		t.Errorf("phone POST /api/mission/abort = %d, want 401", w.Code)
	}
	if w := call(h, "GET", "/api/state", phone, map[string]string{"Authorization": "Bearer wrong-token-000000000"}, ""); w.Code != http.StatusUnauthorized {
		t.Errorf("wrong bearer = %d, want 401", w.Code)
	}
}

func TestPhonesKeepTheirPublicRoutes(t *testing.T) {
	h := newTestServer(t).Handler()
	for _, target := range []string{"/app/", "/shared/tokens.css", "/api/health"} {
		if w := call(h, "GET", target, phone, nil, ""); w.Code != http.StatusOK {
			t.Errorf("phone GET %s = %d, want 200", target, w.Code)
		}
	}
	w := call(h, "GET", "/api/health", phone, nil, "")
	if strings.Contains(w.Body.String(), "AST-") {
		t.Error("public health probe exposes crew data")
	}
}

func TestOperatorAccess(t *testing.T) {
	h := newTestServer(t).Handler()
	if w := call(h, "GET", "/api/state", local, nil, ""); w.Code != http.StatusOK {
		t.Fatalf("station PC GET /api/state = %d, want 200", w.Code)
	}
	if w := call(h, "GET", "/api/state", phone, map[string]string{"Authorization": "Bearer " + testToken}, ""); w.Code != http.StatusOK {
		t.Fatalf("bearer token GET /api/state = %d, want 200", w.Code)
	}
	// Operator link: /?operator=TOKEN sets an HttpOnly cookie, then the console opens.
	if w := call(h, "GET", "/?operator=wrong", phone, nil, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong operator link = %d, want 401", w.Code)
	}
	w := call(h, "GET", "/?operator="+testToken, phone, nil, "")
	if w.Code != http.StatusSeeOther {
		t.Fatalf("operator link = %d, want 303", w.Code)
	}
	c := w.Result().Cookies()
	if len(c) != 1 || c[0].Name != operatorCookie || !c[0].HttpOnly || c[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("operator cookie = %+v", c)
	}
	w = call(h, "GET", "/", phone, map[string]string{"Cookie": c[0].Name + "=" + c[0].Value}, "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "eva console") {
		t.Fatalf("console with cookie = %d %q", w.Code, w.Body.String())
	}
}

func TestCrossSiteRequestsAreBlocked(t *testing.T) {
	h := newTestServer(t).Handler()
	body := `{"crewId":"AST-002"}`
	if w := call(h, "POST", "/api/sim/clear", local, map[string]string{"Origin": "http://evil.example"}, body); w.Code != http.StatusForbidden {
		t.Errorf("cross-origin POST = %d, want 403", w.Code)
	}
	if w := call(h, "POST", "/api/sim/clear", local, map[string]string{"Sec-Fetch-Site": "cross-site"}, body); w.Code != http.StatusForbidden {
		t.Errorf("cross-site POST = %d, want 403", w.Code)
	}
	if w := call(h, "GET", "/ws/console", local, map[string]string{"Upgrade": "websocket", "Origin": "http://evil.example"}, ""); w.Code != http.StatusForbidden {
		t.Errorf("cross-origin console WebSocket = %d, want 403", w.Code)
	}
	if w := call(h, "POST", "/api/sim/clear", local, map[string]string{"Origin": "http://example.com"}, body); w.Code != http.StatusOK {
		t.Errorf("same-origin POST = %d, want 200 (%s)", w.Code, w.Body.String())
	}
}

func TestSecurityHeadersAndNoWildcardCORS(t *testing.T) {
	h := newTestServer(t).Handler()
	w := call(h, "GET", "/api/health", phone, map[string]string{"Origin": "http://evil.example"}, "")
	if v := w.Header().Get("Access-Control-Allow-Origin"); v != "" {
		t.Errorf("Access-Control-Allow-Origin = %q, want none", v)
	}
	for k, want := range map[string]string{"X-Content-Type-Options": "nosniff", "X-Frame-Options": "DENY", "Referrer-Policy": "no-referrer"} {
		if got := w.Header().Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
}

func TestOversizedBodiesAreRejected(t *testing.T) {
	h := newTestServer(t).Handler()
	big := `{"crewId":"AST-001","text":"` + strings.Repeat("x", 2<<20) + `"}`
	if w := call(h, "POST", "/api/instruction", local, nil, big); w.Code == http.StatusOK {
		t.Fatal("2 MiB instruction accepted")
	}
}
