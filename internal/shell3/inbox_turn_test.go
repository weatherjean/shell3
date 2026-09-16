package shell3

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/inbox"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
)

func TestInboxCompletionCancelsRedundantJobCheck(t *testing.T) {
	rt, s := pollTestSession(t)
	id, err := rt.jobs.startCommand(s, "exit 0", t.TempDir(), []string{"sh", "-c", "exit 0"}, nil, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	rt.jobs.wait()
	mail := inbox.Store{Root: t.TempDir()}
	if _, err := mail.Notify(inbox.Request{To: "main", Source: "bash_bg:" + id, Event: "bash_bg.completed", Body: "finished"}); err != nil {
		t.Fatal(err)
	}
	batch, err := mail.PrepareBatch()
	if err != nil {
		t.Fatal(err)
	}
	for ev := range s.SendInbox(t.Context(), batch) {
		if ev.Kind == Error {
			t.Fatal(ev.Err)
		}
	}
	if due := s.TakeDueJobPolls(time.Now().Add(time.Hour)); len(due) != 0 {
		t.Fatal("completion check repeated after inbox delivery")
	}
}

func TestInboxClearsOnlyAfterSuccessfulSavedTurn(t *testing.T) {
	for _, mode := range []string{"success", "model-error", "history-error", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			script := fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "read"}}}
			if mode == "model-error" {
				script.Err = errors.New("provider failed")
			}
			rt := newTestRuntime(t, func() chat.Config { return chat.Config{LLM: fakellm.New(script)} })
			s, err := rt.Session(SessionOpts{})
			if err != nil {
				t.Fatal(err)
			}
			mail := inbox.Store{Root: t.TempDir()}
			r, err := mail.Notify(inbox.Request{To: "main", Source: "test", Event: "done", Body: "complete contents"})
			if err != nil {
				t.Fatal(err)
			}
			batch, err := mail.PrepareBatch()
			if err != nil {
				t.Fatal(err)
			}
			prompt := batch.Prompt()
			if mode == "history-error" {
				if err := rt.Store().Close(); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if mode == "cancelled" {
				cancel()
			}
			for range s.SendInbox(ctx, batch) {
			}
			n, err := mail.Read("main", r.ID)
			if err != nil {
				t.Fatal(err)
			}
			if (n.Status == inbox.StatusArchived) != (mode == "success") {
				t.Fatalf("mode %s: status=%s", mode, n.Status)
			}
			if mode == "success" {
				msgs, err := rt.Store().LoadMessages(s.ID())
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, m := range msgs {
					found = found || m.Content == prompt
				}
				if !found {
					t.Fatal("cleared before saving notice input")
				}
			}
			if mode != "success" {
				retry, err := mail.PrepareBatch()
				if err != nil || retry == nil {
					t.Fatal("failure not retryable")
				}
				retry.Close()
			}
		})
	}
}
