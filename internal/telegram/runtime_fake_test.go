//go:build unix

package telegram

import (
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/runs"
	"github.com/weatherjean/shell3/internal/shell3"
	"github.com/weatherjean/shell3/internal/shell3/shell3test"
)

// newFakeRuntime builds a real Runtime backed by a fakellm that always replies
// with replyText, plus one convenience chat session. Most bot tests want only
// the runtime and build their own sessions (fresh-turn model); handler-level
// tests take the session too.
func newFakeRuntime(t *testing.T, replyText string) (*shell3.Runtime, *shell3.Session) {
	t.Helper()
	rt := shell3test.NewRuntimeForTest(t, replyText)
	sess, err := rt.Session(shell3.SessionOpts{Name: "telegram", Agent: "code"})
	if err != nil {
		t.Fatal(err)
	}
	return rt, sess
}

// mkThreads builds a throwaway persistent thread index under t.TempDir.
func mkThreads(t *testing.T) *ThreadIndex {
	t.Helper()
	st, err := runs.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return NewThreadIndex(func() *runs.Store { return st }, "telegram")
}

// newBot builds a Bot over rt with a throwaway thread index (chat 42) — the
// fresh-turn Bot holds no session of its own.
func newBot(t *testing.T, fc *fakeClient, rt *shell3.Runtime) *Bot {
	t.Helper()
	b := NewBot(fc, rt, 42, mkThreads(t))
	b.debounce = time.Millisecond // tests don't wait out the real burst window
	return b
}

// decoratedSession creates a main chat session on rt and registers the bot's
// host tools on it, mirroring what the runtime session decorator does in
// production. Handler-level tests use it to exercise a tool the way a live turn
// would see it.
func decoratedSession(t *testing.T, b *Bot, rt *shell3.Runtime) *shell3.Session {
	t.Helper()
	sess, err := rt.Session(shell3.SessionOpts{Agent: "code"})
	if err != nil {
		t.Fatal(err)
	}
	b.DecorateChatSession(sess)
	return sess
}
