//go:build unix

package telegram

import (
	"context"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestHandleMsg_IdleSendsReply(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "hello from agent") // helper in runtime_fake_test.go (Step 3)
	b := NewBot(fc, rt, sess, 42)

	ctx := context.Background()
	b.handleMsg(ctx, Msg{ChatID: 42, Text: "hi"})

	if !waitForReply(t, fc, "hello from agent") {
		t.Fatalf("expected agent reply, got: %q", strings.Join(fc.sentTexts(), "\n"))
	}
}

// waitFor polls cond until it returns true or a 1s deadline passes, failing the
// test on timeout. Shared helper for async (goroutine-driven) assertions.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("waitFor: condition not met within 1s")
}

// waitForReply polls fc.sentTexts() until one contains want or the deadline
// passes. The turn runs on its own goroutine, so replies arrive asynchronously.
func waitForReply(t *testing.T, fc *fakeClient, want string) bool {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(strings.Join(fc.sentTexts(), "\n"), want) {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

func TestHandleMsg_MediaRunsTurnWithNote(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "got your file")
	b := NewBot(fc, rt, sess, 42)

	// A media-only message (no text) must still run a turn — the attachment is
	// transformed into a note, not dropped.
	b.handleMsg(context.Background(), Msg{ChatID: 42, Media: []Media{
		{Bytes: []byte("\xff\xd8\xff"), MIME: "image/jpeg", Filename: "photo.jpg"},
	}})

	if !waitForReply(t, fc, "got your file") {
		t.Fatalf("expected the agent to run on a media-only message, got %v", fc.sentTexts())
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
	chunks := chunk(long)
	if len(chunks) != 2 || len(chunks[0]) > 4096 {
		t.Fatalf("bad chunking: %d chunks, first len %d", len(chunks), len(chunks[0]))
	}
}

// A long reply with no newline near the cut must not be split mid-UTF-8-rune:
// Telegram rejects invalid UTF-8 with a 400, silently losing the chunk.
func TestChunk_NeverSplitsARune(t *testing.T) {
	long := strings.Repeat("字", 3000) // 3 bytes each: 9000 bytes, no newlines; 4096 % 3 != 0 → naive cut lands mid-rune
	for i, c := range chunk(long) {
		if !utf8.ValidString(c) {
			t.Fatalf("chunk %d is invalid UTF-8 (split mid-rune)", i)
		}
		if len(c) > 4096 {
			t.Fatalf("chunk %d exceeds max: %d bytes", i, len(c))
		}
	}
	// No content may be lost across the split.
	if got := strings.Join(chunk(long), ""); got != long {
		t.Fatalf("chunking lost content: %d bytes in, %d bytes out", len(long), len(got))
	}
}
