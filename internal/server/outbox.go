package server

// Durable phone messages.
//
// Instructions, buddy relays and EVA aborts must reach the phone even if it
// is offline when they are sent. The durable emitter wraps the Hub as the
// engine's Emitter: such messages are stored in device_outbox with a msgId,
// sent if the phone is online, replayed when it reconnects, and kept until
// the phone answers {"type":"msg-ack","msgId":N}. The phone ignores a msgId
// it has already handled, so a replay never repeats an alert twice.

import (
	"encoding/json"

	"eva/internal/store"
)

// durableTTL: how long each kind of message stays worth delivering (ms).
var durableTTL = map[string]int64{
	"instruction": 6 * 3600 * 1000,
	"abort":       6 * 3600 * 1000,
	"buddy":       10 * 60 * 1000, // a stale buddy alarm would mislead
}

type durable struct {
	hub *Hub
	st  *store.Store
}

// Emitter returns the engine emitter (hub + durable outbox).
func (s *Server) Emitter() *durable { return &durable{hub: s.Hub, st: s.St} }

func (d *durable) Console(msg any) { d.hub.Console(msg) }

func durableType(msg any) (map[string]any, int64, bool) {
	m, ok := msg.(map[string]any)
	if !ok {
		return nil, 0, false
	}
	t, _ := m["type"].(string)
	ttl, ok := durableTTL[t]
	return m, ttl, ok
}

// stamp stores the message for crewID and returns it with its msgId.
func (d *durable) stamp(crewID string, m map[string]any, ttl int64) map[string]any {
	c := make(map[string]any, len(m)+1)
	for k, v := range m {
		c[k] = v
	}
	t, _ := m["type"].(string)
	id, err := d.st.EnqueueOutbox(crewID, t, "{}", ttl)
	if err != nil {
		return c // still delivered live; just not durable
	}
	c["msgId"] = id
	b, _ := json.Marshal(c)
	d.st.SetOutboxPayload(id, string(b))
	return c
}

func (d *durable) Device(crewID string, msg any) bool {
	if m, ttl, ok := durableType(msg); ok {
		return d.hub.Device(crewID, d.stamp(crewID, m, ttl))
	}
	return d.hub.Device(crewID, msg)
}

func (d *durable) DevicesExcept(except string, msg any) {
	m, ttl, ok := durableType(msg)
	if !ok {
		d.hub.DevicesExcept(except, msg)
		return
	}
	crew, err := d.st.ListCrew() // store read: never takes the engine lock
	if err != nil {
		d.hub.DevicesExcept(except, msg)
		return
	}
	for _, c := range crew {
		if c.ID != except {
			d.hub.Device(c.ID, d.stamp(c.ID, m, ttl))
		}
	}
}

// replayOutbox sends a reconnecting phone everything it has not confirmed.
func (s *Server) replayOutbox(c *client) {
	msgs, err := s.St.PendingOutbox(c.crewID)
	if err != nil {
		return
	}
	for _, m := range msgs {
		c.send([]byte(m.Payload))
	}
}
