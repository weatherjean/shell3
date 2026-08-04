//go:build unix

package webui

// Browser sessions, kept server-side. The cookie carries an opaque random
// token; this file holds only its hash, so a copy of the file is a list of
// records rather than a set of working credentials.
//
// Server-side rather than a signed stateless cookie for one reason: revocation.
// A logged-out — or stolen — session has to stop working without logging out
// every other device, and that needs state to delete.

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	sessionsFile = "web_sessions.json"

	// sessionTTL is short for a browser session because a login here is a
	// shell: a cookie lifted from a device that stopped being yours expires on
	// its own. Use renews it, so an active browser never sees a login screen.
	sessionTTL = 7 * 24 * time.Hour

	// renewWithin is how much remaining life makes a session worth extending
	// on use. Extending on every request would rewrite the file constantly.
	renewWithin = sessionTTL / 2
)

// sessionRecord is one logged-in browser.
type sessionRecord struct {
	// Hash is the SHA-256 of the token, hex. The token itself is never stored.
	Hash    string `json:"hash"`
	Created string `json:"created"`
	Expires string `json:"expires"`
	// Label is the user-agent that logged in, so a notification can say which
	// device it was.
	Label string `json:"label,omitempty"`
	// Password is a fingerprint of the password this session was created
	// under. Changing the password changes the fingerprint, which invalidates
	// every session — otherwise changing it after a suspected breach would
	// evict nobody.
	Password string `json:"password"`
}

type sessionStore struct {
	path string

	mu      sync.Mutex
	records map[string]sessionRecord // token hash → record
}

// newSessionStore loads the stored sessions, dropping any that have expired.
func newSessionStore(stateDir string) (*sessionStore, error) {
	s := &sessionStore{
		path:    filepath.Join(stateDir, sessionsFile),
		records: map[string]sessionRecord{},
	}
	data, err := os.ReadFile(s.path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return s, nil
	case err != nil:
		return nil, err
	}

	var stored []sessionRecord
	if err := json.Unmarshal(data, &stored); err != nil {
		// A corrupt file logs everyone out, which is the safe direction.
		return s, nil
	}
	now := time.Now()
	pruned := false
	for _, rec := range stored {
		if expiryOf(rec).After(now) {
			s.records[rec.Hash] = rec
			continue
		}
		pruned = true
	}
	if pruned {
		if err := s.save(); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// issue mints a session for a browser that has just proved the password (and
// the second factor, when one is configured). The token it returns is the only
// copy — the store keeps a hash.
func (s *sessionStore) issue(passwordFingerprint, label string) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	now := time.Now()

	s.mu.Lock()
	s.records[hashToken(token)] = sessionRecord{
		Hash:     hashToken(token),
		Created:  now.UTC().Format(time.RFC3339),
		Expires:  now.Add(sessionTTL).UTC().Format(time.RFC3339),
		Label:    label,
		Password: passwordFingerprint,
	}
	err := s.save()
	s.mu.Unlock()
	if err != nil {
		return "", err
	}
	return token, nil
}

// valid reports whether a cookie names a live session created under the current
// password, renewing it when it is more than halfway through its life.
func (s *sessionStore) valid(token, passwordFingerprint string) bool {
	if token == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, ok := s.records[hashToken(token)]
	if !ok || rec.Password != passwordFingerprint {
		return false
	}
	if !expiryOf(rec).After(time.Now()) {
		delete(s.records, rec.Hash)
		_ = s.save()
		return false
	}
	// Sliding expiry, extended in place rather than rotating the token: two
	// requests in flight together would otherwise race, one rotating the token
	// the other is still using.
	if time.Until(expiryOf(rec)) < renewWithin {
		rec.Expires = time.Now().Add(sessionTTL).UTC().Format(time.RFC3339)
		s.records[rec.Hash] = rec
		_ = s.save()
	}
	return true
}

// revoke forgets one session: the logout button.
func (s *sessionStore) revoke(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.records, hashToken(token))
	_ = s.save()
}

// count is how many sessions are on file.
func (s *sessionStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.records)
}

// save writes the file owner-only, replacing it atomically so a crash mid-write
// cannot leave a truncated file that logs everyone out.
func (s *sessionStore) save() error {
	records := make([]sessionRecord, 0, len(s.records))
	for _, rec := range s.records {
		records = append(records, rec)
	}
	data, err := json.Marshal(records)
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// fingerprintPassword identifies a password without storing it. Sessions carry
// this so a password change invalidates them; it is never used to verify a
// login, which compares the password itself in constant time.
func fingerprintPassword(password string) string {
	sum := sha256.Sum256([]byte("shell3-web-password\x00" + password))
	return hex.EncodeToString(sum[:16])
}

// expiryOf reads a record's expiry, treating an unparseable one as expired.
func expiryOf(rec sessionRecord) time.Time {
	at, err := time.Parse(time.RFC3339, rec.Expires)
	if err != nil {
		return time.Time{}
	}
	return at
}
