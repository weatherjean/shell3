//go:build unix

package telegram

import (
	"context"
	"testing"
)

func TestSendReplyConvertsMarkdownToHTML(t *testing.T) {
	f := newFakeClient()
	b := &Bot{client: f, chatID: 1}

	b.sendReply(context.Background(), "**hi** and `code`")

	got := f.htmlTexts()
	if len(got) != 1 || got[0] != "<b>hi</b> and <code>code</code>" {
		t.Fatalf("html sends = %q, want one HTML message", got)
	}
	if len(f.plainTexts()) != 0 {
		t.Fatalf("expected no plain sends on success, got %q", f.plainTexts())
	}
}

func TestSendReplyFallsBackToPlainOnHTMLError(t *testing.T) {
	f := newFakeClient()
	f.failHTML = true
	b := &Bot{client: f, chatID: 1}

	b.sendReply(context.Background(), "**hi**")

	if plain := f.plainTexts(); len(plain) != 1 || plain[0] != "**hi**" {
		t.Fatalf("plain fallback = %q, want original markdown", plain)
	}
}
