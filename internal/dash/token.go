// Package dash is the read-only web dashboard: a localhost HTTP server over
// the runs store and live runtime state, gated by short-lived bearer tokens
// carried in the query string. It renders views; it never mutates anything.
package dash

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"sync"
	"time"
)

// TokenTTL is how long a minted token opens the dash. Long enough to read a
// dashboard, short enough that a leaked URL goes stale over lunch.
const TokenTTL = time.Hour

// TokenStore mints and checks dash access tokens. Tokens are memory-only by
// design: a restart invalidates every open tab, and /dash is one tap away.
type TokenStore struct {
	now func() time.Time

	mu     sync.Mutex
	tokens map[string]time.Time // token -> expiry
}

// NewTokenStore returns a store using now as its clock (nil = time.Now).
func NewTokenStore(now func() time.Time) *TokenStore {
	if now == nil {
		now = time.Now
	}
	return &TokenStore{now: now, tokens: make(map[string]time.Time)}
}

// Mint issues a fresh token valid for TokenTTL. Earlier tokens stay valid
// until their own expiry — a tab opened from the last /dash keeps working.
func (t *TokenStore) Mint() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failing means the platform's entropy source is broken;
		// there is no safe fallback token to hand out.
		panic("dash: crypto/rand failed: " + err.Error())
	}
	tok := hex.EncodeToString(b)
	now := t.now()
	t.mu.Lock()
	defer t.mu.Unlock()
	for k, exp := range t.tokens {
		if !now.Before(exp) {
			delete(t.tokens, k)
		}
	}
	t.tokens[tok] = now.Add(TokenTTL)
	return tok
}

// Valid reports whether tok is a live token. Every stored token is compared
// in constant time — a map lookup keyed on the secret would leak matching
// prefixes through timing.
func (t *TokenStore) Valid(tok string) bool {
	if tok == "" {
		return false
	}
	now := t.now()
	t.mu.Lock()
	defer t.mu.Unlock()
	ok := false
	for k, exp := range t.tokens {
		if subtle.ConstantTimeCompare([]byte(tok), []byte(k)) == 1 && now.Before(exp) {
			ok = true
		}
	}
	return ok
}

// count reports live entries (tests only; prune happens on Mint).
func (t *TokenStore) count() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.tokens)
}
