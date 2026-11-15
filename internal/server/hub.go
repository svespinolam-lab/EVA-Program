package server

import (
	"encoding/json"
	"sync"
	"time"

	"eva/internal/ws"
)

// client is one WebSocket connection with its own writer goroutine and a
// bounded queue: a slow console never blocks the engine.
type client struct {
	conn   *ws.Conn
	out    chan []byte
	quit   chan struct{}
	once   sync.Once
	crewID string // phones only
	name   string
}

func newClient(conn *ws.Conn) *client {
	c := &client{conn: conn, out: make(chan []byte, 512), quit: make(chan struct{})}
	go c.writeLoop()
	return c
}

func (c *client) send(b []byte) {
	select {
	case c.out <- b:
	case <-c.quit:
	default: // queue full: drop (the next 1 Hz update supersedes it)
	}
}

func (c *client) writeLoop() {
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case b := <-c.out:
			if err := c.conn.WriteText(b); err != nil {
				c.close()
				return
			}
		case <-ping.C:
			if err := c.conn.Ping(); err != nil {
				c.close()
				return
			}
		case <-c.quit:
			return
		}
	}
}

func (c *client) close() {
	c.once.Do(func() {
		close(c.quit)
		c.conn.Close()
	})
}

// Hub routes messages to consoles (PC) and phones (one per crew member).
// It implements engine.Emitter.
type Hub struct {
	mu       sync.Mutex
	consoles map[*client]bool
	devices  map[string]*client
}

// NewHub creates an empty hub.
func NewHub() *Hub { return &Hub{consoles: map[*client]bool{}, devices: map[string]*client{}} }

func marshal(msg any) []byte {
	if b, ok := msg.([]byte); ok {
		return b
	}
	b, err := json.Marshal(msg)
	if err != nil {
		return []byte(`{"type":"error","error":"encode"}`)
	}
	return b
}

// Console sends a message to every PC console.
func (h *Hub) Console(msg any) {
	b := marshal(msg)
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.consoles {
		c.send(b)
	}
}

// Device sends a message to the phone of one crew member.
func (h *Hub) Device(crewID string, msg any) bool {
	h.mu.Lock()
	c := h.devices[crewID]
	h.mu.Unlock()
	if c == nil {
		return false
	}
	c.send(marshal(msg))
	return true
}

// DevicesExcept sends a message to every phone except crewID ("" = all).
func (h *Hub) DevicesExcept(crewID string, msg any) {
	b := marshal(msg)
	h.mu.Lock()
	defer h.mu.Unlock()
	for id, c := range h.devices {
		if id != crewID {
			c.send(b)
		}
	}
}

// Online reports whether a phone is linked for a crew member.
func (h *Hub) Online(crewID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.devices[crewID] != nil
}

// Counts returns the number of consoles and phones connected.
func (h *Hub) Counts() (int, int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.consoles), len(h.devices)
}

func (h *Hub) addConsole(c *client) {
	h.mu.Lock()
	h.consoles[c] = true
	h.mu.Unlock()
}

func (h *Hub) removeConsole(c *client) {
	h.mu.Lock()
	delete(h.consoles, c)
	h.mu.Unlock()
}

// addDevice registers a phone; a previous phone of the same crew member is closed.
func (h *Hub) addDevice(c *client) {
	h.mu.Lock()
	old := h.devices[c.crewID]
	h.devices[c.crewID] = c
	h.mu.Unlock()
	if old != nil && old != c {
		old.send(marshal(map[string]any{"type": "replaced", "reason": "Another phone linked as this crew member"}))
		time.AfterFunc(300*time.Millisecond, old.close)
	}
}

// removeDevice unregisters a phone; returns true if it was the active one.
func (h *Hub) removeDevice(c *client) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.devices[c.crewID] == c {
		delete(h.devices, c.crewID)
		return true
	}
	return false
}
