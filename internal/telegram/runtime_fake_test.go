//go:build unix

package telegram

import (
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/runs"
	"github.com/weatherjean/shell3/internal/shell3"
	"github.com/weatherjean/shell3/internal/shell3/shell3test"
)

func newFakeRuntime(t *testing.T, replyText string) (*shell3.Runtime, *shell3.Session) {
	t.Helper()
	rt := shell3test.NewRuntimeForTest(t, replyText)
	sess, err := rt.Session(shell3.SessionOpts{Name: "telegram"})
	if err != nil {
		t.Fatal(err)
	}
	return rt, sess
}

// mkSessionIndex builds a throwaway persistent session index under t.TempDir.
func mkSessionIndex(t *testing.T) *SessionIndex {
	t.Helper()
	st, err := runs.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return NewSessionIndex(func() *runs.Store { return st }, "telegram")
}

func newBot(t *testing.T, fc *fakeClient, rt *shell3.Runtime) *Bot {
	t.Helper()
	b := NewBot(fc, rt, 42, mkSessionIndex(t))
	b.debounce = time.Millisecond
	return b
}

func decoratedSession(t *testing.T, b *Bot, rt *shell3.Runtime) *shell3.Session {
	t.Helper()
	sess, err := rt.Session(shell3.SessionOpts{})
	if err != nil {
		t.Fatal(err)
	}
	b.DecorateOrchestratorSession(sess)
	return sess
}
