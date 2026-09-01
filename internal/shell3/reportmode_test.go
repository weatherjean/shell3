package shell3

import (
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
	"github.com/weatherjean/shell3/internal/notify"
)

func lastMail(t *testing.T, h *fakeHost) Mail {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.mails) != 1 {
		t.Fatalf("mails = %d, want 1: %+v", len(h.mails), h.mails)
	}
	return h.mails[0]
}

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

func TestCancelledJobRoutesNoCompletion(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("x"))
	host := &fakeHost{wakeOK: true}
	rt.SetCompletionHost(host)
	parent, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	id, err := rt.jobs.startCommand(parent, "sleep 30", t.TempDir(),
		[]string{"sleep", "30"}, nil, notify.ReportAuto, "")
	if err != nil {
		t.Fatalf("startCommand: %v", err)
	}
	if err := rt.jobs.cancel(id, true); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	rt.jobs.wait()

	posts, wakes, fresh := host.snapshot()
	if len(posts) != 0 || len(wakes) != 0 || len(fresh) != 0 {
		t.Fatalf("a cancelled job must route nothing: posts=%v wakes=%v fresh=%v", posts, wakes, fresh)
	}
}

func TestCancelAfterFinishStillRoutes(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("x"))
	host := &fakeHost{wakeOK: true}
	rt.SetCompletionHost(host)
	parent, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	id, err := rt.jobs.startCommand(parent, "true", t.TempDir(),
		[]string{"true"}, nil, notify.ReportRaw, "")
	if err != nil {
		t.Fatalf("startCommand: %v", err)
	}
	rt.jobs.wait()
	waitFor(t, "raw post", func() bool { posts, _, _ := host.snapshot(); return len(posts) == 1 })
	if err := rt.jobs.cancel(id, true); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if posts, _, _ := host.snapshot(); len(posts) != 1 {
		t.Fatalf("a finished job's result survives a late cancel: posts=%v", posts)
	}
}
