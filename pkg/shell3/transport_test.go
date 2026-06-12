package shell3

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
	"github.com/weatherjean/shell3/internal/notify"
	"github.com/weatherjean/shell3/internal/paths"
	"github.com/weatherjean/shell3/internal/socket"
	"github.com/weatherjean/shell3/internal/store"
)

func TestSockPath_Short(t *testing.T) {
	p := paths.SockPath("/wd", 7)
	if !strings.HasSuffix(p, "/.shell3/sock/7.sock") {
		t.Fatalf("unexpected sock path %q", p)
	}
}

// TestTransport_DeliversOverSocket is the end-to-end socket path: a producer
// Sends a bg_done notification to a session's socket; the listener injects a
// short POINTER (not the log contents) into the session inbox and Wakes the
// idle session. The Phase-5 analogue of TestSinkWatcher_DeliversBgDonePointer.
func TestTransport_DeliversOverSocket(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	rt := newTestRuntime(t, fakeCfgWithStore(st, fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "ok"}}}))
	// A SHORT workdir: macOS caps Unix-socket paths at ~104 bytes, so the deep
	// default t.TempDir() (under /var/folders/...) overflows. /tmp keeps the
	// socket path well under the limit.
	wd, err := os.MkdirTemp("/tmp", "s3sock")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(wd) })
	s, err := rt.Session(SessionOpts{Name: "tg:1", WorkDir: wd})
	if err != nil {
		t.Fatal(err)
	}
	id := s.sess.ID()
	if id == 0 {
		t.Fatal("session has no store id; transport is skipped")
	}

	// The producer sends to the same socket path startTransport listens on.
	exit := 0
	line, err := json.Marshal(notify.Notification{
		Kind: "bg_done", ID: "bg_9c", Exit: &exit, Log: "/tmp/x.log", Cmd: "npx tsc",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := socket.Send(paths.SockPath(wd, id), line); err != nil {
		t.Fatalf("socket send: %v", err)
	}

	// Idle session: the listener must Wake it.
	select {
	case ev := <-rt.Events():
		if ev.Kind != Wake || ev.Session != "tg:1" {
			t.Fatalf("got %+v, want Wake for tg:1", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("transport did not Wake the session")
	}

	// The pointer was Interjected into the inbox; the next turn drains it as a
	// system-reminder. It must name the log/cmd but NOT inline log contents.
	var reminder string
	for ev := range s.Send(context.Background(), "next") {
		if ev.Kind == SystemReminder && strings.Contains(ev.Text, "bg_9c") {
			reminder = ev.Text
		}
	}
	if reminder == "" {
		t.Fatal("bg_done pointer not delivered to inbox")
	}
	if !strings.Contains(reminder, "/tmp/x.log") || !strings.Contains(reminder, "npx tsc") {
		t.Fatalf("pointer missing log/cmd: %q", reminder)
	}
}
