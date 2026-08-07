//go:build unix

package webui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testFingerprint = "fp-of-the-password"

// expireForTest rewrites one session's expiry, standing in for the passage of
// time. Kept here rather than on sessionStore: production code has no business
// being able to backdate a session.
func (s *sessionStore) expireForTest(token string, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec := s.records[hashToken(token)]
	rec.Expires = at.UTC().Format(time.RFC3339)
	s.records[rec.Hash] = rec
	if err := s.save(); err != nil {
		panic(err)
	}
}

func (s *sessionStore) expiryForTest(token string) time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return expiryOf(s.records[hashToken(token)])
}

func newTestSessions(t *testing.T, dir string) *sessionStore {
	t.Helper()
	s, err := newSessionStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSessionIssuedTokenIsValid(t *testing.T) {
	s := newTestSessions(t, t.TempDir())

	token, err := s.issue(testFingerprint, "Firefox on Linux")
	if err != nil {
		t.Fatal(err)
	}
	if !s.valid(token, testFingerprint) {
		t.Error("the token just issued was rejected")
	}
}

func TestSessionRejectsAnUnknownToken(t *testing.T) {
	s := newTestSessions(t, t.TempDir())
	if _, err := s.issue(testFingerprint, "browser"); err != nil {
		t.Fatal(err)
	}

	if s.valid("not-a-token-anyone-issued", testFingerprint) {
		t.Error("an invented token was accepted")
	}
	if s.valid("", testFingerprint) {
		t.Error("an empty token was accepted")
	}
}

// The point of storing sessions in a file: restarting serve must not log
// everyone out, the way regenerating an in-memory signing key would.
func TestSessionSurvivesAReopen(t *testing.T) {
	dir := t.TempDir()
	token, err := newTestSessions(t, dir).issue(testFingerprint, "browser")
	if err != nil {
		t.Fatal(err)
	}

	if !newTestSessions(t, dir).valid(token, testFingerprint) {
		t.Error("a session did not survive reopening the store")
	}
}

// "I think I was breached, let me change the password" has to actually evict
// whoever is in. Each session remembers the password it was created under.
func TestSessionDiesWhenThePasswordChanges(t *testing.T) {
	s := newTestSessions(t, t.TempDir())
	token, err := s.issue(testFingerprint, "browser")
	if err != nil {
		t.Fatal(err)
	}

	if s.valid(token, "fp-of-a-new-password") {
		t.Error("changing the password left an existing session valid")
	}
}

func TestSessionExpires(t *testing.T) {
	s := newTestSessions(t, t.TempDir())
	token, err := s.issue(testFingerprint, "browser")
	if err != nil {
		t.Fatal(err)
	}
	s.expireForTest(token, time.Now().Add(-time.Minute))

	if s.valid(token, testFingerprint) {
		t.Error("an expired session was accepted")
	}
}

// An expired session is not just refused, it is forgotten — otherwise the file
// grows forever with entries nobody can use.
func TestSessionPrunesExpiredEntriesOnReopen(t *testing.T) {
	dir := t.TempDir()
	s := newTestSessions(t, dir)
	stale, err := s.issue(testFingerprint, "old browser")
	if err != nil {
		t.Fatal(err)
	}
	live, err := s.issue(testFingerprint, "current browser")
	if err != nil {
		t.Fatal(err)
	}
	s.expireForTest(stale, time.Now().Add(-time.Minute))

	reopened := newTestSessions(t, dir)
	if got := reopened.count(); got != 1 {
		t.Errorf("sessions after reopen = %d, want 1: the expired one should be pruned", got)
	}
	if !reopened.valid(live, testFingerprint) {
		t.Error("pruning took the live session with it")
	}
}

func TestSessionRevokeDropsOnlyThatSession(t *testing.T) {
	s := newTestSessions(t, t.TempDir())
	phone, err := s.issue(testFingerprint, "phone")
	if err != nil {
		t.Fatal(err)
	}
	laptop, err := s.issue(testFingerprint, "laptop")
	if err != nil {
		t.Fatal(err)
	}

	s.revoke(phone)

	if s.valid(phone, testFingerprint) {
		t.Error("the revoked session still works")
	}
	if !s.valid(laptop, testFingerprint) {
		t.Error("revoking one session logged out another")
	}
}

// Active use must never hit a login screen, so a session in use has its expiry
// pushed out rather than being allowed to lapse on a fixed schedule.
func TestSessionUseExtendsTheExpiry(t *testing.T) {
	s := newTestSessions(t, t.TempDir())
	token, err := s.issue(testFingerprint, "browser")
	if err != nil {
		t.Fatal(err)
	}
	// Most of the way through its life: past halfway, so use should renew it.
	s.expireForTest(token, time.Now().Add(time.Hour))

	if !s.valid(token, testFingerprint) {
		t.Fatal("a live session was rejected")
	}

	if left := time.Until(s.expiryForTest(token)); left < sessionTTL-time.Minute {
		t.Errorf("expiry is %v away, want it renewed to about %v", left, sessionTTL)
	}
}

// The file is a list of session records, not a list of usable credentials: a
// stolen copy must not let anyone log in. And it holds no secret in a
// world-readable file.
func TestSessionFileStoresHashesAndIsOwnerOnly(t *testing.T) {
	dir := t.TempDir()
	s := newTestSessions(t, dir)
	token, err := s.issue(testFingerprint, "browser")
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(dir, sessionsFile)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %v, want 0600", perm)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), token) {
		t.Error("the session file contains the token itself; it must store only a hash")
	}
	_ = s
}
