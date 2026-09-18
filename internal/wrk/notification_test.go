//go:build unix

package wrk

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/inbox"
)

func TestTerminalFailureRouting(t *testing.T) {
	for _, tc := range []struct {
		name, command, failure, destination, event string
		cancel, expire                             bool
	}{
		{name: "success", command: "true", failure: "main", destination: "quiet", event: "wrk.completed"},
		{name: "failure", command: "false", failure: "main", destination: "main", event: "wrk.failed"},
		{name: "legacy", command: "false", destination: "quiet", event: "wrk.failed"},
		{name: "same destination", command: "false", failure: "quiet", destination: "quiet", event: "wrk.failed"},
		{name: "cancel", command: "true", failure: "main", destination: "main", event: "wrk.cancelled", cancel: true},
		{name: "timeout", command: "true", failure: "main", destination: "main", event: "wrk.failed", expire: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			_, dir := startCommandRunWithOptions(t, `(task "route" (command work (run "`+tc.command+`")))`, func(o *StartOptions) {
				o.NotifyTo, o.NotifyFailureTo, o.NotifyState = "quiet", tc.failure, root
			})
			if tc.expire {
				var m Manifest
				if err := readJSON(filepath.Join(dir, "run.json"), &m); err != nil {
					t.Fatal(err)
				}
				m.Deadline = time.Now().Add(-time.Second)
				if err := writeJSON(filepath.Join(dir, "run.json"), m); err != nil {
					t.Fatal(err)
				}
			}
			if tc.cancel {
				if err := Cancel(dir); err != nil {
					t.Fatal(err)
				}
			}
			// Repeated beats model restart/reconciliation after terminal state.
			for range 2 {
				_, _ = Beat(t.Context(), dir)
			}
			persisted, err := TerminalNoticePersisted(dir)
			if err != nil || !persisted {
				t.Fatalf("persisted=%v err=%v", persisted, err)
			}
			for _, destination := range []string{"quiet", "main"} {
				notices, total, err := (inbox.Store{Root: root}).List(destination, inbox.StatusAll, 0, 10)
				want := 0
				if destination == tc.destination {
					want = 1
				}
				if err != nil || total != want {
					t.Fatalf("%s: count=%d err=%v", destination, total, err)
				}
				if want == 1 && notices[0].Message.Event != tc.event {
					t.Fatalf("notice = %+v", notices[0])
				}
			}
		})
	}
}

func TestFailureNoticeRetriesAfterPersistenceError(t *testing.T) {
	root := t.TempDir()
	_, dir := startCommandRunWithOptions(t, `(task "retry" (command work (run "false")))`, func(o *StartOptions) {
		o.NotifyFailureTo, o.NotifyState = "main", root
	})
	if err := os.MkdirAll(filepath.Join(root, "inbox"), 0700); err != nil {
		t.Fatal(err)
	}
	blocked := filepath.Join(root, "inbox", "bWFpbg")
	if err := os.WriteFile(blocked, []byte("blocked"), 0600); err != nil {
		t.Fatal(err)
	}
	_, _ = Beat(t.Context(), dir)
	if persisted, err := TerminalNoticePersisted(dir); err != nil || persisted {
		t.Fatalf("persisted=%v err=%v", persisted, err)
	}
	if err := os.Remove(blocked); err != nil {
		t.Fatal(err)
	}
	if _, err := Beat(t.Context(), dir); err != nil {
		t.Fatal(err)
	}
	if persisted, err := TerminalNoticePersisted(dir); err != nil || !persisted {
		t.Fatalf("persisted=%v err=%v", persisted, err)
	}
}
