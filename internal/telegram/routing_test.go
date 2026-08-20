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
	rt, _ := newFakeRuntime(t, "hello from agent")
	b := newBot(t, fc, rt)

	ctx := context.Background()
	b.handleMsg(ctx, Msg{ChatID: 42, SenderID: 42, ID: "1", Text: "hi"})

	if !waitForReply(t, fc, "hello from agent") {
		t.Fatalf("expected agent reply, got: %q", strings.Join(fc.sentTexts(), "\n"))
	}
}

// waitFor polls cond until it returns true or a 5s deadline passes, failing the
// test on timeout. Shared helper for async (goroutine-driven) assertions. The
// deadline is generous because CI runners under -race schedule goroutines far
// more slowly than a dev machine; a passing test still returns in milliseconds.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("waitFor: condition not met within 5s")
}

// waitIdle waits for the bot's turn slot to free. A turn's reply posts
// BEFORE turnActive clears, so a test that acts right after waitForReply
// races the teardown (a /new refuses mid-turn, a manual flag set is
// clobbered, a queued message is drained early). Wait like a user's next
// tap naturally would.
func waitIdle(t *testing.T, b *Bot) {
	t.Helper()
	waitFor(t, func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		return !b.turnActive
	})
}

// waitForReply polls fc.sentTexts() until one contains want or the deadline
// passes. The turn runs on its own goroutine, so replies arrive asynchronously.
func waitForReply(t *testing.T, fc *fakeClient, want string) bool {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
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
	rt, _ := newFakeRuntime(t, "got your file")
	b := newBot(t, fc, rt)

	// A media-only message (no text) must still run a turn — the attachment is
	// transformed into a note, not dropped.
	b.handleMsg(context.Background(), Msg{ChatID: 42, SenderID: 42, Media: []Media{
		{Bytes: []byte("\xff\xd8\xff"), MIME: "image/jpeg", Filename: "photo.jpg"},
	}})

	if !waitForReply(t, fc, "got your file") {
		t.Fatalf("expected the agent to run on a media-only message, got %v", fc.sentTexts())
	}
}

func TestHandleMsg_WrongChatDropped(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "should not run")
	b := newBot(t, fc, rt)

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
	// 3 bytes each but ONE UTF-16 unit each: 3000 units fits a single
	// message even at 9000 bytes — Telegram bills units, not bytes. A longer
	// run must split on rune boundaries, never mid-rune.
	long := strings.Repeat("字", 4500)
	for i, c := range chunk(long) {
		if !utf8.ValidString(c) {
			t.Fatalf("chunk %d is invalid UTF-8 (split mid-rune)", i)
		}
		if utf16Len(c) > tgMaxMessage {
			t.Fatalf("chunk %d exceeds max: %d UTF-16 units", i, utf16Len(c))
		}
	}
	// No content may be lost across the split.
	if got := strings.Join(chunk(long), ""); got != long {
		t.Fatalf("chunking lost content: %d bytes in, %d bytes out", len(long), len(got))
	}
}
