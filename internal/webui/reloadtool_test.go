//go:build unix

package webui

import (
	"strings"
	"testing"
)

func TestReloadToolSkipsHeadlessSessions(t *testing.T) {
	reg := &fakeRegistrar{headless: true}
	if err := RegisterReloadTool(reg, func() string { return "" }); err != nil {
		t.Fatal(err)
	}
	if len(reg.tools) != 0 {
		t.Errorf("a headless session must not get the reload tool, got %v", reg.tools)
	}

	reg = &fakeRegistrar{}
	if err := RegisterReloadTool(reg, func() string { return "" }); err != nil {
		t.Fatal(err)
	}
	if len(reg.tools) != 1 || reg.tools[0].Name != "reload" {
		t.Errorf("an interactive session should get exactly the reload tool, got %v", reg.tools)
	}
}

// The queued reload applies once the turn ends, and its result — success or
// not — lands in the bell so it can never fail silently.
func TestQueuedReloadPostsItsResult(t *testing.T) {
	srv := newTestServer(t, "ok")
	applied := 0
	srv.applyReloadFn = func() (string, bool) {
		applied++
		return "✅ reloaded — test", true
	}

	if msg := srv.queueReload(); !strings.Contains(msg, "queued") {
		t.Errorf("the tool should tell the model the reload is queued, got %q", msg)
	}
	srv.runPendingReload()

	notes := srv.recentNotices()
	if len(notes) != 1 || notes[0].Title != "config reload" {
		t.Fatalf("one 'config reload' notification expected, got %+v", notes)
	}

	// Applied exactly once: the flag must clear.
	srv.runPendingReload()
	if applied != 1 {
		t.Errorf("reload applied %d times, want exactly once", applied)
	}
	if got := len(srv.recentNotices()); got != 1 {
		t.Errorf("a drained queue must not reload again, got %d notifications", got)
	}
}

// A turn that dies on a provider error while nobody is attached must leave
// durable evidence: an alert in the bell, tagged with the thread it belongs to.
func TestAlertTurnFailureLeavesEvidence(t *testing.T) {
	srv := newTestServer(t, "ok")
	srv.alertTurnFailure("t1", "llm: stream: 401 Unauthorized")

	notes := srv.recentNotices()
	if len(notes) != 1 {
		t.Fatalf("expected one notification, got %+v", notes)
	}
	n := notes[0]
	if n.Kind != "alert" || n.Title != "turn failed" {
		t.Errorf("kind/title = %q/%q, want alert/turn failed", n.Kind, n.Title)
	}
	if !strings.Contains(n.Body, "401 Unauthorized") {
		t.Errorf("the provider error must be in the body, got %q", n.Body)
	}
	if n.ThreadID != "t1" {
		t.Errorf("threadId = %q, want t1", n.ThreadID)
	}
}
