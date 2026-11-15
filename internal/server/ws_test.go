package server

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// wsClient is a minimal RFC 6455 client (masked text frames) for tests.
type wsClient struct {
	c  net.Conn
	br *bufio.Reader
}

func dialWS(t *testing.T, base, path string) *wsClient {
	t.Helper()
	u, _ := url.Parse(base)
	c, err := net.DialTimeout("tcp", u.Host, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	key := make([]byte, 16)
	_, _ = rand.Read(key)
	fmt.Fprintf(c, "GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n",
		path, u.Host, base64.StdEncoding.EncodeToString(key))
	br := bufio.NewReader(c)
	resp, err := http.ReadResponse(br, nil)
	if err != nil || resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("handshake: %v %v", resp, err)
	}
	return &wsClient{c: c, br: br}
}

func (w *wsClient) send(t *testing.T, v any) {
	t.Helper()
	b, _ := json.Marshal(v)
	n := len(b)
	hdr := []byte{0x81}
	switch {
	case n < 126:
		hdr = append(hdr, byte(0x80|n))
	case n < 65536:
		hdr = append(hdr, 0x80|126, byte(n>>8), byte(n))
	default:
		t.Fatal("frame too large for the test client")
	}
	mask := []byte{0x13, 0x37, 0xc0, 0xde}
	hdr = append(hdr, mask...)
	p := make([]byte, n)
	for i := range b {
		p[i] = b[i] ^ mask[i%4]
	}
	if _, err := w.c.Write(append(hdr, p...)); err != nil {
		t.Fatal(err)
	}
}

// next returns the next JSON message of the given type (skipping others).
func (w *wsClient) next(t *testing.T, typ string, timeout time.Duration) map[string]any {
	t.Helper()
	_ = w.c.SetReadDeadline(time.Now().Add(timeout))
	for {
		h := make([]byte, 2)
		if _, err := io.ReadFull(w.br, h); err != nil {
			t.Fatalf("waiting for %q: %v", typ, err)
		}
		op, n := h[0]&0x0f, uint64(h[1]&0x7f)
		switch n {
		case 126:
			b := make([]byte, 2)
			_, _ = io.ReadFull(w.br, b)
			n = uint64(binary.BigEndian.Uint16(b))
		case 127:
			b := make([]byte, 8)
			_, _ = io.ReadFull(w.br, b)
			n = binary.BigEndian.Uint64(b)
		}
		p := make([]byte, n)
		if _, err := io.ReadFull(w.br, p); err != nil {
			t.Fatal(err)
		}
		if op == 0x8 {
			t.Fatalf("server closed the link while waiting for %q", typ)
		}
		if op != 0x1 {
			continue // ping or pong
		}
		var m map[string]any
		if json.Unmarshal(p, &m) == nil && m["type"] == typ {
			return m
		}
	}
}

// A phone links, streams readings (including a replayed duplicate) and gets
// store-and-forward acknowledgements; each reading is stored exactly once and
// the welcome never carries the medical records of the other crew members.
func TestPhoneLinkTelemetryAckAndDedup(t *testing.T) {
	srv := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	ph := dialWS(t, ts.URL, "/ws/device")
	ph.send(t, pairedHello(t, srv, "AST-001"))
	welcome := ph.next(t, "welcome", 5*time.Second)
	if welcome["crewId"] != "AST-001" {
		t.Fatalf("welcome = %v", welcome)
	}
	if b, _ := json.Marshal(welcome); strings.Contains(string(b), "allergies") {
		t.Fatal("welcome carries medical records")
	}

	t0 := time.Now().UnixMilli() - 3000
	reading := func(seq int64, at int64) map[string]any {
		return map[string]any{"type": "telemetry", "seq": seq, "t": at, "hr": 74, "spo2": 98, "temp": 36.8, "rr": 15, "effort": 0,
			"suitP": 4.3, "o2": 360, "co2": 2.1, "batt": 80, "rad": 0.05}
	}
	ph.send(t, reading(1, t0))
	ph.send(t, reading(1, t0)) // duplicate (replay after a reconnect)
	ph.send(t, reading(2, t0+1000))

	deadline := time.Now().Add(5 * time.Second)
	for {
		ack := ph.next(t, "ack-seq", time.Until(deadline))
		if seq, _ := ack["seq"].(float64); seq >= 2 {
			break
		}
	}
	srv.St.Flush()
	var n int
	if err := srv.St.R.QueryRow(`SELECT COUNT(*) FROM telemetry WHERE crew_id = 'AST-001' AND ts IN (?, ?)`, t0, t0+1000).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("stored %d rows for 2 readings + 1 duplicate, want 2", n)
	}
}

// An unknown crew id is refused.
func TestPhoneLinkRejectsUnknownCrew(t *testing.T) {
	srv := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	ph := dialWS(t, ts.URL, "/ws/device")
	ph.send(t, map[string]any{"type": "hello", "crewId": "AST-999"})
	if m := ph.next(t, "error", 5*time.Second); !strings.Contains(fmt.Sprint(m["error"]), "unknown crew") {
		t.Fatalf("error = %v", m)
	}
}
