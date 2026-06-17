package runs

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestAppendPointer_CreatesDirAndWrites covers the standalone AppendPointer used
// by a subagent reporting to its parent's inbox: it must create the inbox's
// parent directory if missing and write one well-formed line to the exact path.
func TestAppendPointer_CreatesDirAndWrites(t *testing.T) {
	inbox := filepath.Join(t.TempDir(), "parent", ".shell3_project", "inbox.jsonl")
	if err := AppendPointer(inbox, Pointer{RunID: "child", Kind: "agent_done", Summary: "done"}); err != nil {
		t.Fatalf("AppendPointer: %v", err)
	}
	b, err := os.ReadFile(inbox)
	if err != nil {
		t.Fatalf("read inbox: %v", err)
	}
	var p Pointer
	if err := json.Unmarshal(b, &p); err != nil { // json tolerates the trailing newline
		t.Fatalf("garbled line %q: %v", b, err)
	}
	if p.RunID != "child" {
		t.Fatalf("got RunID %q, want child", p.RunID)
	}
}

func TestAppendInboxConcurrent(t *testing.T) {
	root := t.TempDir() + "/.shell3_project"
	s, _ := Open(root)
	const n = 50
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = s.AppendInbox(Pointer{RunID: "r", Kind: "agent_done", Summary: "done"})
		}(i)
	}
	wg.Wait()

	f, err := os.Open(filepath.Join(root, "inbox.jsonl"))
	if err != nil {
		t.Fatalf("open inbox: %v", err)
	}
	defer f.Close()
	lines := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var p Pointer
		if err := json.Unmarshal(sc.Bytes(), &p); err != nil {
			t.Fatalf("interleaved/garbled line %q: %v", sc.Text(), err)
		}
		lines++
	}
	if lines != n {
		t.Fatalf("want %d intact lines, got %d", n, lines)
	}
}

func TestAppendInboxRejectsOversize(t *testing.T) {
	s, _ := Open(t.TempDir() + "/.shell3_project")
	big := make([]byte, 5000)
	for i := range big {
		big[i] = 'x'
	}
	if err := s.AppendInbox(Pointer{RunID: "r", Summary: string(big)}); err == nil {
		t.Fatal("expected oversize pointer to be rejected")
	}
}

func TestWatchDelivers(t *testing.T) {
	root := t.TempDir() + "/.shell3_project"
	s, _ := Open(root)
	_ = s.AppendInbox(Pointer{RunID: "pre"}) // exists before watch starts
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got := make(chan Pointer, 4)
	go func() { _ = s.Watch(ctx, func(p Pointer) { got <- p }) }()

	first := <-got // replayed
	if first.RunID != "pre" {
		t.Fatalf("want replayed pre, got %q", first.RunID)
	}
	_ = s.AppendInbox(Pointer{RunID: "post"})
	second := <-got // streamed
	if second.RunID != "post" {
		t.Fatalf("want streamed post, got %q", second.RunID)
	}
}

// TestWatch_NoReplayAfterRestart verifies that a pointer already delivered in a
// prior Watch session is NOT re-delivered when Watch is re-entered (restart
// resilience), but a pointer appended while the watcher was down IS delivered.
func TestWatch_NoReplayAfterRestart(t *testing.T) {
	root := t.TempDir() + "/.shell3_project"
	s, _ := Open(root)

	// --- first Watch session ---
	_ = s.AppendInbox(Pointer{RunID: "first"})

	ctx1, cancel1 := context.WithCancel(context.Background())
	got1 := make(chan Pointer, 4)
	done1 := make(chan struct{})
	go func() {
		_ = s.Watch(ctx1, func(p Pointer) { got1 <- p })
		close(done1)
	}()

	// Receive "first" — this must persist the offset past "first".
	select {
	case p := <-got1:
		if p.RunID != "first" {
			t.Fatalf("session1: want first, got %q", p.RunID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("session1: timed out waiting for 'first'")
	}

	// Shut down first session cleanly (offset is already persisted per-line).
	cancel1()
	select {
	case <-done1:
	case <-time.After(5 * time.Second):
		t.Fatal("session1: watcher did not exit")
	}

	// Append "second" while watcher is down — must be delivered on restart.
	_ = s.AppendInbox(Pointer{RunID: "second"})

	// --- second Watch session (simulated restart) ---
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	got2 := make(chan Pointer, 4)
	go func() { _ = s.Watch(ctx2, func(p Pointer) { got2 <- p }) }()

	// The first pointer delivered after restart must be "second", not "first".
	select {
	case p := <-got2:
		if p.RunID == "first" {
			t.Fatal("restart replay bug: 'first' was re-delivered after restart")
		}
		if p.RunID != "second" {
			t.Fatalf("session2: want second, got %q", p.RunID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("session2: timed out waiting for 'second'")
	}

	// Verify no additional unexpected deliveries come through.
	select {
	case p := <-got2:
		t.Fatalf("session2: unexpected extra delivery: %q", p.RunID)
	case <-time.After(100 * time.Millisecond):
		// good — no replay
	}
}
