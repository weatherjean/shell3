package shell3

import (
	"strings"
	"testing"
	"time"
)

// waitDispatchDone drains the session's JobEvents until the given job reports
// Done (or the deadline passes).
func waitDispatchDone(t *testing.T, s *Session, id string) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case p := <-s.JobEvents():
			if p.JobID == id && p.Done {
				return
			}
		case <-deadline:
			t.Fatalf("job %s never finished", id)
		}
	}
}

// Dispatch fires a host-initiated subagent job on the normal job runtime: it
// gets a subN id, and with Direct:true its completion notice queues into the
// session (Wake path) so the host can RunQueued a narrating turn.
func TestDispatchDirectWakesSession(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("subagent done"))
	defer rt.Close()
	sess, err := rt.Session(SessionOpts{Name: "disp"})
	if err != nil {
		t.Fatal(err)
	}
	id, err := sess.Dispatch("", "do the thing", DispatchOpts{Description: "direct:test", Direct: true})
	if err != nil {
		t.Fatal(err)
	}
	if id == "" {
		t.Fatal("no job id")
	}
	waitForWake(t, rt, sess)
	if !sess.HasQueuedInput() {
		t.Fatal("Direct:true must queue a completion notice for the session")
	}
	found := false
	for _, j := range sess.Jobs() {
		if j.ID == id && j.Kind == JobSubagent {
			found = true
		}
	}
	if !found {
		t.Fatalf("dispatched job %s missing from Jobs()", id)
	}
}

// A default (non-direct) cron dispatch routes through the CompletionHost — in
// degraded mode (no notifier.md) as a raw post carrying its cron origin — and
// never wakes the dispatch parent.
func TestDispatchCronRoutesToHost(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("cron result"))
	defer rt.Close()
	host := &fakeHost{}
	rt.SetCompletionHost(host)
	sess, err := rt.Session(SessionOpts{Name: "cron", Headless: true})
	if err != nil {
		t.Fatal(err)
	}
	id, err := sess.Dispatch("", "do the thing", DispatchOpts{Description: "cron:test", CronJob: "test"})
	if err != nil {
		t.Fatal(err)
	}
	waitDispatchDone(t, sess, id)
	waitFor(t, "host post", func() bool { posts, _, _ := host.snapshot(); return len(posts) >= 1 })
	posts, _, _ := host.snapshot()
	if !strings.Contains(posts[0], "cron=test") {
		t.Fatalf("post = %q, want cron origin", posts[0])
	}
	select {
	case ev := <-rt.Events():
		if ev.Kind == Wake && ev.Session == sess.ID() {
			t.Fatalf("cron dispatch woke its pinned parent: %+v", ev)
		}
	case <-time.After(300 * time.Millisecond):
	}
	if sess.HasQueuedInput() {
		t.Fatal("cron dispatch must not queue a notice on the pinned parent")
	}
}

// A relative dispatch workdir joins onto the parent's effective base (the old
// Dispatch contract): parent workdir when set, else the runtime root — never
// the process CWD.
func TestResolveChildWorkDir(t *testing.T) {
	cases := []struct{ parent, override, root, want string }{
		{"", "", "/root", ""},                            // inherit: "" stays "" (→ root downstream)
		{"/srv/bot", "", "/root", "/srv/bot"},            // inherit parent's exact value
		{"/srv/bot", "notes", "/root", "/srv/bot/notes"}, // relative joins parent
		{"", "notes", "/root", "/root/notes"},            // relative joins root when parent unset
		{"/srv/bot", "/abs/dir", "/root", "/abs/dir"},    // absolute wins
	}
	for _, c := range cases {
		if got := resolveChildWorkDir(c.parent, c.override, c.root); got != c.want {
			t.Errorf("resolveChildWorkDir(%q, %q, %q) = %q, want %q", c.parent, c.override, c.root, got, c.want)
		}
	}
}
