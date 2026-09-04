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
// writes come off the bot's turn goroutines.
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

func TestConsoleDrivesBotLoop(t *testing.T) {
	rt, _ := newFakeRuntime(t, "hello from agent")
	pr, pw := io.Pipe()
	out := &syncBuffer{}
	cc := NewConsoleClient(pr, out, ConsoleChatID)
	b := NewBot(cc, rt, ConsoleChatID, mkSessionIndex(t))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { b.Run(ctx); close(done) }()

	if _, err := io.WriteString(pw, "hi\n"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		return strings.Contains(out.String(), "hello from agent") &&
			strings.Contains(out.String(), "↩#1")
	})

	if _, err := io.WriteString(pw, "@2 more please\n"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		return strings.Count(out.String(), "hello from agent") >= 2
	})
	if strings.Contains(out.String(), "can't continue") {
		t.Fatalf("thread continuation hit the can't-continue notice:\n%s", out.String())
	}

	_ = pw.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Bot.Run did not return after EOF")
	}
}
