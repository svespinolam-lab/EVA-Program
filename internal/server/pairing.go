package server

// Phone pairing.
//
// A phone proves which crew member it belongs to with a per-device token.
// The token is issued once, when the phone presents a one-time 6-digit code
// that a console user created for that crew member (valid 5 minutes, single
// use). After 20 wrong codes every pending code is cancelled, so codes
// cannot be brute-forced. Only the SHA-256 of device tokens is stored.

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"sync"

	"eva/internal/store"
)

const (
	pairCodeTTL  = 5 * 60 * 1000 // ms
	maxPairFails = 20
)

type pendingCode struct {
	crewID  string
	by      string
	expires int64
}

type pairingBook struct {
	mu    sync.Mutex
	codes map[string]pendingCode
	fails int
}

func newPairingBook() *pairingBook { return &pairingBook{codes: map[string]pendingCode{}} }

func (b *pairingBook) gc(now int64) {
	for k, p := range b.codes {
		if now > p.expires {
			delete(b.codes, k)
		}
	}
}

// issue creates a fresh code for a crew member.
func (b *pairingBook) issue(crewID, by string) (string, int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := store.Now()
	b.gc(now)
	var code string
	for {
		n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
		if err != nil {
			panic(err)
		}
		code = fmt.Sprintf("%06d", n.Int64())
		if _, dup := b.codes[code]; !dup {
			break
		}
	}
	p := pendingCode{crewID: crewID, by: by, expires: now + pairCodeTTL}
	b.codes[code] = p
	return code, p.expires
}

// take consumes a valid code (for crewID, if given).
func (b *pairingBook) take(code, crewID string) (pendingCode, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.gc(store.Now())
	p, ok := b.codes[code]
	if !ok || (crewID != "" && crewID != p.crewID) {
		b.fails++
		if b.fails >= maxPairFails {
			b.codes = map[string]pendingCode{}
			b.fails = 0
		}
		return pendingCode{}, false
	}
	delete(b.codes, code)
	return p, true
}
