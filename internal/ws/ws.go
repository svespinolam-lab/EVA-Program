// Package ws is a minimal, dependency-free WebSocket server (RFC 6455)
// with what EVA needs: text messages, fragmentation, ping/pong and close.
// Keeping it in-house avoids third-party code on the mission computer.
package ws

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const guid = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

const (
	opCont   = 0x0
	opText   = 0x1
	opBinary = 0x2
	opClose  = 0x8
	opPing   = 0x9
	opPong   = 0xA
)

// ErrTooBig is returned when a message exceeds Conn.MaxMessage.
var ErrTooBig = errors.New("ws: message too big")

// Conn is a server-side WebSocket connection. Reads must happen from one
// goroutine; writes are safe from several goroutines.
type Conn struct {
	c          net.Conn
	br         *bufio.Reader
	wmu        sync.Mutex
	MaxMessage int64
	closeOnce  sync.Once
}

func headerHas(h http.Header, name, token string) bool {
	for _, v := range h.Values(name) {
		for _, part := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(part), token) {
				return true
			}
		}
	}
	return false
}

// Upgrade performs the opening handshake and hijacks the HTTP connection.
func Upgrade(w http.ResponseWriter, r *http.Request) (*Conn, error) {
	if r.Method != http.MethodGet || !headerHas(r.Header, "Connection", "upgrade") || !headerHas(r.Header, "Upgrade", "websocket") {
		http.Error(w, "WebSocket upgrade required", http.StatusBadRequest)
		return nil, errors.New("ws: not a websocket request")
	}
	if r.Header.Get("Sec-WebSocket-Version") != "13" {
		w.Header().Set("Sec-WebSocket-Version", "13")
		http.Error(w, "unsupported WebSocket version", http.StatusUpgradeRequired)
		return nil, errors.New("ws: bad version")
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" {
		http.Error(w, "missing Sec-WebSocket-Key", http.StatusBadRequest)
		return nil, errors.New("ws: missing key")
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijacking not supported", http.StatusInternalServerError)
		return nil, errors.New("ws: cannot hijack")
	}
	conn, brw, err := hj.Hijack()
	if err != nil {
		return nil, err
	}
	sum := sha1.Sum([]byte(key + guid))
	accept := base64.StdEncoding.EncodeToString(sum[:])
	resp := "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " + accept + "\r\n\r\n"
	if _, err := brw.WriteString(resp); err != nil {
		conn.Close()
		return nil, err
	}
	if err := brw.Flush(); err != nil {
		conn.Close()
		return nil, err
	}
	if tc, ok := conn.(*net.TCPConn); ok {
		tc.SetNoDelay(true) // send small telemetry frames immediately
	}
	return &Conn{c: conn, br: brw.Reader, MaxMessage: 8 << 20}, nil
}

func (c *Conn) readFrame() (fin bool, op int, payload []byte, err error) {
	var h [2]byte
	if _, err = io.ReadFull(c.br, h[:]); err != nil {
		return
	}
	fin = h[0]&0x80 != 0
	op = int(h[0] & 0x0F)
	masked := h[1]&0x80 != 0
	n := int64(h[1] & 0x7F)
	switch n {
	case 126:
		var b [2]byte
		if _, err = io.ReadFull(c.br, b[:]); err != nil {
			return
		}
		n = int64(binary.BigEndian.Uint16(b[:]))
	case 127:
		var b [8]byte
		if _, err = io.ReadFull(c.br, b[:]); err != nil {
			return
		}
		n = int64(binary.BigEndian.Uint64(b[:]))
	}
	if n < 0 || n > c.MaxMessage {
		err = ErrTooBig
		return
	}
	var mask [4]byte
	if masked {
		if _, err = io.ReadFull(c.br, mask[:]); err != nil {
			return
		}
	}
	payload = make([]byte, n)
	if _, err = io.ReadFull(c.br, payload); err != nil {
		return
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i&3]
		}
	}
	return
}

// ReadMessage returns the next complete text or binary message.
// Pings are answered automatically; a close frame returns io.EOF.
func (c *Conn) ReadMessage() ([]byte, error) {
	var msg []byte
	started := false
	for {
		fin, op, payload, err := c.readFrame()
		if err != nil {
			return nil, err
		}
		switch op {
		case opPing:
			_ = c.writeFrame(opPong, payload)
			continue
		case opPong:
			continue
		case opClose:
			code := []byte{0x03, 0xE8} // 1000 normal closure
			_ = c.writeFrame(opClose, code)
			return nil, io.EOF
		case opText, opBinary:
			msg = payload
			started = true
		case opCont:
			if !started {
				return nil, errors.New("ws: unexpected continuation frame")
			}
			msg = append(msg, payload...)
		default:
			return nil, errors.New("ws: unknown opcode")
		}
		if int64(len(msg)) > c.MaxMessage {
			return nil, ErrTooBig
		}
		if fin {
			return msg, nil
		}
	}
}

func (c *Conn) writeFrame(op int, p []byte) error {
	n := len(p)
	buf := make([]byte, 0, n+10)
	buf = append(buf, 0x80|byte(op))
	switch {
	case n < 126:
		buf = append(buf, byte(n))
	case n <= 0xFFFF:
		buf = append(buf, 126, byte(n>>8), byte(n))
	default:
		buf = append(buf, 127)
		var l [8]byte
		binary.BigEndian.PutUint64(l[:], uint64(n))
		buf = append(buf, l[:]...)
	}
	buf = append(buf, p...)
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_ = c.c.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, err := c.c.Write(buf)
	return err
}

// WriteText sends one text message.
func (c *Conn) WriteText(p []byte) error { return c.writeFrame(opText, p) }

// Ping sends a ping control frame.
func (c *Conn) Ping() error { return c.writeFrame(opPing, nil) }

// SetReadDeadline sets the deadline for the next read.
func (c *Conn) SetReadDeadline(t time.Time) error { return c.c.SetReadDeadline(t) }

// RemoteAddr returns the peer address.
func (c *Conn) RemoteAddr() string { return c.c.RemoteAddr().String() }

// Close sends a close frame (best effort) and closes the connection.
func (c *Conn) Close() error {
	var err error
	c.closeOnce.Do(func() {
		_ = c.writeFrame(opClose, []byte{0x03, 0xE8})
		err = c.c.Close()
	})
	return err
}
