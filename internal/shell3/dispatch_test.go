package shell3

import (
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
// gets a subN id, and with Notify:true its completion notice queues into the
// session (Wake path) so the host can RunQueued a narrating turn.
func TestDispatchRunsSubagentJobAndWakes(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("subagent done"))
	defer rt.Close()
	sess, err := rt.Session(SessionOpts{Name: "disp"})
	if err != nil {
		t.Fatal(err)
	}
	id, err := sess.Dispatch("", "do the thing", DispatchOpts{Description: "cron:test", Notify: true})
	if err != nil {
		t.Fatal(err)
	}
	if id == "" {
		t.Fatal("no job id")
	}
	waitForWake(t, rt, sess)
	if !sess.HasQueuedInput() {
		t.Fatal("Notify:true must queue a completion notice for the session")
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

// Notify:false must deliver the notice quietly: the result is queued for the
// agent's next turn but no Wake fires, so an idle chat stays silent.
func TestDispatchQuietDoesNotWake(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("quiet result"))
	defer rt.Close()
	sess, err := rt.Session(SessionOpts{Name: "quiet"})
	if err != nil {
		t.Fatal(err)
	}
	id, err := sess.Dispatch("", "do the thing", DispatchOpts{Notify: false})
	if err != nil {
		t.Fatal(err)
	}
	waitDispatchDone(t, sess, id)
	select {
	case ev := <-rt.Events():
		t.Fatalf("unexpected host event for quiet dispatch: %+v", ev)
	case <-time.After(300 * time.Millisecond):
	}
	if !sess.HasQueuedInput() {
		t.Fatal("quiet dispatch must still queue the notice for the next turn")
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
