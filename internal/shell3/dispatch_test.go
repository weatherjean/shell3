package shell3

import (
	"strings"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/notify"
)

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

func TestDispatchDirectWakesSession(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("subagent done"))
	defer rt.Close()
	sess, err := rt.Session(SessionOpts{Name: "disp"})
	if err != nil {
		t.Fatal(err)
	}
	id, err := sess.Dispatch("", "do the thing", DispatchOpts{Description: "direct:test", Report: notify.ReportRaw})
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
	waitFor(t, "agent mail", func() bool { _, _, fresh := host.snapshot(); return len(fresh) >= 1 })
	_, _, fresh := host.snapshot()
	if !strings.Contains(fresh[0], "cron job: test") {
		t.Fatalf("mail = %q, want cron origin", fresh[0])
	}
	if posts, _, _ := host.snapshot(); len(posts) != 0 {
		t.Fatalf("posts = %v, want none", posts)
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

func TestDispatch_CronJobReachesTheSessionRow(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("worker done"))
	defer rt.Close()
	parent, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatal(err)
	}
	id, err := parent.Dispatch("worker", "go", DispatchOpts{CronJob: "bookmarks-tick"})
	if err != nil {
		t.Fatal(err)
	}
	waitDispatchDone(t, parent, id)

	sessions, err := rt.store.ListSessions(100)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, m := range sessions {
		if m.CronJob == "bookmarks-tick" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("want 1 session attributed to bookmarks-tick, got %d", n)
	}
}

func TestResolveChildWorkDir(t *testing.T) {
	cases := []struct{ parent, override, root, want string }{
		{"", "", "/root", ""},
		{"/srv/bot", "", "/root", "/srv/bot"},
		{"/srv/bot", "notes", "/root", "/srv/bot/notes"},
		{"", "notes", "/root", "/root/notes"},
		{"/srv/bot", "/abs/dir", "/root", "/abs/dir"},
	}
	for _, c := range cases {
		if got := resolveChildWorkDir(c.parent, c.override, c.root); got != c.want {
			t.Errorf("resolveChildWorkDir(%q, %q, %q) = %q, want %q", c.parent, c.override, c.root, got, c.want)
		}
	}
}
