//go:build unix

package telegram

import (
	"context"
	"strings"
	"testing"
)

// A reply longer than replyMaxChunks bubbles posts its first chunk plus the
// FULL text as a RENDERED HTML document — never a machine-gun of chat
// messages, and never raw markdown source, which only relocated the wall of
// unformatted text into a file. Both message ids are recorded so the thread
// anchor lands on the document.
func TestPostReplyCapsChunksWithDocumentOverflow(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)

	long := strings.Repeat("line of reply text\n", 900) // ≫ 2·4096 bytes
	tconv(b).postReply(context.Background(), sess, "7", long)

	if got := len(fc.replies); got != 1 {
		t.Fatalf("want exactly 1 chat bubble, got %d", got)
	}
	doc, ok := fc.lastDoc()
	if !ok || doc.filename != "reply.html" {
		t.Fatalf("want a reply.html document, got %+v ok=%v", doc, ok)
	}
	body := string(doc.data)
	if !strings.HasPrefix(body, "<!doctype html>") {
		t.Fatalf("the attachment must be a rendered page, got: %.80s", body)
	}
	// Every line of the reply survives into the page: the attachment is the
	// FULL answer, not a preview of it.
	if n := strings.Count(body, "line of reply text"); n != 900 {
		t.Fatalf("page carries %d of 900 lines — the full reply must survive", n)
	}
	c := tconv(b)
	c.mu.Lock()
	anchor := c.mainAnchor
	c.mu.Unlock()
	if anchor == "" {
		t.Fatal("expected the conversation anchor recorded")
	}
}

// A short reply (within the cap) posts as plain bubbles, no document.
func TestPostReplyShortStaysInline(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)

	tconv(b).postReply(context.Background(), sess, "7", "short reply")
	if len(fc.replies) != 1 {
		t.Fatalf("want 1 bubble, got %d", len(fc.replies))
	}
	if _, ok := fc.lastDoc(); ok {
		t.Fatal("short reply must not become a document")
	}
}

// If the overflow document fails to send, the remaining chunks post as
// bubbles — degraded, never lost.
func TestPostReplyDocFailureFallsBackToChunks(t *testing.T) {
	fc := newFakeClient()
	fc.failDoc = errFakeHTML
	rt, sess := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)

	long := strings.Repeat("line of reply text\n", 900)
	tconv(b).postReply(context.Background(), sess, "7", long)

	all := ""
	for _, r := range fc.replies {
		all += r.text
	}
	if len(fc.replies) < 3 {
		t.Fatalf("doc failed: want every chunk posted as a bubble, got %d", len(fc.replies))
	}
	if !strings.Contains(all, "line of reply text") || len(all) < len(long)-100 {
		t.Fatalf("doc failed: chunks must carry the full reply, got %d of %d bytes", len(all), len(long))
	}
}

func TestWithReplyContext(t *testing.T) {
	got := withReplyContext("what does this do?", "func foo() {\n  return 1\n}")
	want := "> func foo() {\n>   return 1\n> }\n\nwhat does this do?"
	if got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}

	if withReplyContext("hi", "") != "hi" {
		t.Fatal("no reply text should pass through unchanged")
	}
	if withReplyContext("hi", "   ") != "hi" {
		t.Fatal("blank reply text should pass through unchanged")
	}
}

// The overflow page renders structure Telegram cannot: mdhtml parses no
// tables at all, so a long comparison posted inline arrives as literal pipe
// characters. In the attachment it must be a real table.
func TestPostReplyOverflowRendersTables(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)

	long := "| theme | n |\n|---|---|\n| schema | 21 |\n\n" + strings.Repeat("filler line\n", 900)
	tconv(b).postReply(context.Background(), sess, "7", long)

	doc, ok := fc.lastDoc()
	if !ok {
		t.Fatal("expected the overflow document")
	}
	if !strings.Contains(string(doc.data), "<td>schema</td>") {
		t.Fatal("the page must render a real table, not the source pipes")
	}
}
