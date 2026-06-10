//go:build unix

package telegram

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestHandleMsg_IdleSendsReply(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "hello from agent") // helper in runtime_fake_test.go (Step 3)
	b := NewBot(fc, rt, sess, 42)

	ctx := context.Background()
	b.handleMsg(ctx, Msg{ChatID: 42, Text: "hi"})

	got := strings.Join(fc.sentTexts(), "\n")
	if !strings.Contains(got, "hello from agent") {
		t.Fatalf("expected agent reply, got: %q", got)
	}
}

func TestHandleMsg_WrongChatDropped(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "should not run")
	b := NewBot(fc, rt, sess, 42)

	b.handleMsg(context.Background(), Msg{ChatID: 999, Text: "hi"})

	if len(fc.sentTexts()) != 0 {
		t.Fatalf("expected no output for unauthorized chat, got %v", fc.sentTexts())
	}
}

func TestChunk_SplitsAt4096(t *testing.T) {
	long := strings.Repeat("a", 5000)
	chunks := chunk(long, 4096)
	if len(chunks) != 2 || len(chunks[0]) > 4096 {
		t.Fatalf("bad chunking: %d chunks, first len %d", len(chunks), len(chunks[0]))
	}
}

var _ = time.Second // keep import if unused after edits
