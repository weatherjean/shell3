//go:build unix

package telegram

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// syncBuffer is a goroutine-safe io.Writer sink for the console client, whose
// writes come off the bot's turn/wake goroutines.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestConsoleDrivesBotLoop wires the REAL Bot loop over the console transport
// and drives it like a script: a plain stdin line runs a turn whose reply prints
// to stdout with the "[#id ↩#reply]" framing, a "@<id>" line continues that
// thread (no can't-continue notice), and EOF cleanly stops Bot.Run.
func TestConsoleDrivesBotLoop(t *testing.T) {
	rt, _ := newFakeRuntime(t, "hello from agent")
	pr, pw := io.Pipe()
	out := &syncBuffer{}
	cc := NewConsoleClient(pr, out, ConsoleChatID)
	b := NewBot(cc, rt, ConsoleChatID, mkThreads(t))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { b.Run(ctx); close(done) }()

	// Fresh message → a threaded reply "[#2 ↩#1] hello from agent" (inbound "hi"
	// is id 1, the reply is id 2).
	if _, err := io.WriteString(pw, "hi\n"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		return strings.Contains(out.String(), "hello from agent") &&
			strings.Contains(out.String(), "↩#1")
	})

	// Reply to the bot's message #2 → the thread resumes (a second turn runs),
	// never the "can't continue" notice.
	if _, err := io.WriteString(pw, "@2 more please\n"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		return strings.Count(out.String(), "hello from agent") >= 2
	})
	if strings.Contains(out.String(), "can't continue") {
		t.Fatalf("thread continuation hit the can't-continue notice:\n%s", out.String())
	}

	// EOF stops the loop cleanly.
	_ = pw.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Bot.Run did not return after EOF")
	}
}

// TestConsoleCompletionPosts exercises the completion-post path over the
// console transport: a cron completion posts "⏰ <job>: <result>" to stdout,
// a non-cron completion posts with the 🔔 prefix.
func TestConsoleCompletionPosts(t *testing.T) {
	rt, _ := newFakeRuntime(t, "unused")
	out := &syncBuffer{}
	cc := NewConsoleClient(strings.NewReader(""), out, ConsoleChatID)
	b := NewBot(cc, rt, ConsoleChatID, mkThreads(t))

	b.PostCompletion("nightly", "", "backup complete")
	waitFor(t, func() bool { return strings.Contains(out.String(), "⏰ nightly: backup complete") })

	b.PostCompletion("", "", "fetch finished")
	waitFor(t, func() bool { return strings.Contains(out.String(), "🔔 fetch finished") })
}
