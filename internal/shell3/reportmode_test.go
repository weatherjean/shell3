package shell3

import (
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
	"github.com/weatherjean/shell3/internal/notify"
)

// lastMail returns the single Mail the host saw, failing when the count is
// not exactly one — every test here is about one completion.
func lastMail(t *testing.T, h *fakeHost) Mail {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.mails) != 1 {
		t.Fatalf("mails = %d, want 1: %+v", len(h.mails), h.mails)
	}
	return h.mails[0]
}

// report:"always" binds the report turn: the mail carries Required plus the
// raw text the front-end posts if that turn says nothing. Without the
// fallback the bind would have nothing to fall back TO, and enforcement would
// have to ask the model a second time.
func TestReportAlwaysMailsWithFallback(t *testing.T) {
	rt := newTestRuntime(t, func() chat.Config { return chat.Config{LLM: fakellm.New()} })
	host := &fakeHost{wakeOK: true}
	rt.SetCompletionHost(host)
	ev := cleanEvent()
	ev.OwnerID, ev.Report = "sess-1", notify.ReportAlways
	rt.jobs.dispatchCompletion(ev)

	if posts, _, fresh := host.snapshot(); len(posts) != 0 || len(fresh) != 0 {
		t.Fatalf("posts=%v fresh=%v, want the agent mailed instead", posts, fresh)
	}
	m := lastMail(t, host)
	if !m.Required {
		t.Fatal("report:always must mark the mail Required")
	}
	if !strings.Contains(m.Fallback, "hi") || !strings.Contains(m.Post.Text, "hi") {
		t.Fatalf("fallback = %q, post = %q, want the job's own output", m.Fallback, m.Post.Text)
	}
	if m.Post.JobID != "bg1" {
		t.Fatalf("fallback post lost its provenance: %+v", m.Post)
	}
	if strings.Contains(m.Note, "NO_REPLY is not available") == false {
		t.Fatalf("the mail must tell the model the bind exists, got %q", m.Note)
	}
}

// The default mode is unchanged: no bind, and the mail still offers NO_REPLY.
func TestReportAutoMailsUnbound(t *testing.T) {
	rt := newTestRuntime(t, func() chat.Config { return chat.Config{LLM: fakellm.New()} })
	host := &fakeHost{wakeOK: true}
	rt.SetCompletionHost(host)
	ev := cleanEvent()
	ev.OwnerID = "sess-1"
	rt.jobs.dispatchCompletion(ev)

	m := lastMail(t, host)
	if m.Required || m.Fallback != "" {
		t.Fatalf("report:auto must not bind the turn: %+v", m)
	}
}

// A job whose own output happens to BE the sentinel is dropped unmailed under
// auto — the cost valve for idempotent ticks — but never under always: the
// spawner said the user is waiting, and the job's output does not get to
// overrule that.
func TestReportAlwaysSurvivesNoReplyTail(t *testing.T) {
	for _, tc := range []struct {
		name     string
		mode     notify.ReportMode
		wantMail bool
	}{
		{"auto drops it", notify.ReportAuto, false},
		{"always keeps it", notify.ReportAlways, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := newTestRuntime(t, func() chat.Config { return chat.Config{LLM: fakellm.New()} })
			host := &fakeHost{wakeOK: true}
			rt.SetCompletionHost(host)
			ev := cleanEvent()
			ev.OwnerID, ev.Report, ev.Tail = "sess-1", tc.mode, "NO_REPLY"
			rt.jobs.dispatchCompletion(ev)

			_, wakes, _ := host.snapshot()
			if got := len(wakes) == 1; got != tc.wantMail {
				t.Fatalf("mailed = %v, want %v (wakes=%v)", got, tc.wantMail, wakes)
			}
		})
	}
}

// A FAILED report:"always" job drops the bind: the ⚠️ floor post already told
// the user, so binding the turn to speak would only duplicate it.
func TestReportAlwaysFailureDropsTheBind(t *testing.T) {
	rt := newTestRuntime(t, func() chat.Config { return chat.Config{LLM: fakellm.New()} })
	host := &fakeHost{wakeOK: true}
	rt.SetCompletionHost(host)
	ev := failedEvent()
	ev.OwnerID, ev.Report = "sess-1", notify.ReportAlways
	rt.jobs.dispatchCompletion(ev)

	posts, _, _ := host.snapshot()
	if len(posts) != 1 || !strings.Contains(posts[0], "⚠️") {
		t.Fatalf("posts = %v, want one failure floor", posts)
	}
	if m := lastMail(t, host); m.Required || m.Fallback != "" {
		t.Fatalf("a floored failure must not also bind the turn: %+v", m)
	}
}

// report:"raw" is unchanged by the rename: the result posts itself and no
// agent turn is spent.
func TestReportRawPostsAndDoesNotMail(t *testing.T) {
	rt := newTestRuntime(t, func() chat.Config { return chat.Config{LLM: fakellm.New()} })
	host := &fakeHost{wakeOK: true}
	rt.SetCompletionHost(host)
	ev := cleanEvent()
	ev.OwnerID, ev.Report = "sess-1", notify.ReportRaw
	rt.jobs.dispatchCompletion(ev)

	posts, wakes, fresh := host.snapshot()
	if len(posts) != 1 || !strings.Contains(posts[0], "hi") {
		t.Fatalf("posts = %v, want the raw result", posts)
	}
	if len(wakes) != 0 || len(fresh) != 0 {
		t.Fatalf("report:raw must spend no agent turn: wakes=%v fresh=%v", wakes, fresh)
	}
}
