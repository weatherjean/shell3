//go:build unix

package telegram

import (
	"context"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/shell3"
)

func feed(b *Bot, evs ...shell3.Event) (string, bool) {
	ch := make(chan shell3.Event, len(evs))
	for _, ev := range evs {
		ch <- ev
	}
	close(ch)
	return tconv(b).drainTurnProgress(context.Background(), ch)
}

func TestProgressBubbleLifecycle(t *testing.T) {
	fc := newFakeClient()
	b := newBot(t, fc, storeRuntime(t, "unused"))

	reply, sawErr := feed(b,
		shell3.Event{Kind: shell3.ToolCall, ToolName: "bash", ToolInput: `{"command":"cd /x && python3 <<'PYEOF'\nimport json\nPYEOF"}`},
		shell3.Event{Kind: shell3.ToolResult, ToolName: "bash"},
		shell3.Event{Kind: shell3.Token, Text: "all done"},
		shell3.Event{Kind: shell3.Done},
	)
	if reply != "all done" || sawErr {
		t.Fatalf("reply=%q sawErr=%v", reply, sawErr)
	}
	sent := fc.sentTexts()
	if len(sent) == 0 || sent[0] != "⚙️ working…" {
		t.Fatalf("working marker was not posted immediately: %v", sent)
	}
	fc.mu.Lock()
	edits := append([]sentEdit{}, fc.edits...)
	fc.mu.Unlock()
	if len(edits) == 0 || !strings.Contains(edits[0].text, "⚙️ bash — cd /x && python3") {
		t.Fatalf("first tool was not added immediately: %+v", edits)
	}
	if strings.Contains(edits[0].text, "\nimport json") {
		t.Fatalf("bubble leaked a multi-line command body: %q", edits[0].text)
	}
	if !fc.lastSilent() {
		t.Fatal("the bubble must post silently")
	}
	if del := fc.deletedSnapshot(); len(del) != 1 {
		t.Fatalf("clean turn must delete its bubble, deleted=%v", del)
	}
}

func TestProgressBubbleKeptOnError(t *testing.T) {
	fc := newFakeClient()
	b := newBot(t, fc, storeRuntime(t, "unused"))

	_, sawErr := feed(b,
		shell3.Event{Kind: shell3.ToolCall, ToolName: "bash", ToolInput: `{"command":"boom"}`},
		shell3.Event{Kind: shell3.Error, Err: context.DeadlineExceeded},
	)
	if !sawErr {
		t.Fatal("error not surfaced")
	}
	if del := fc.deletedSnapshot(); len(del) != 0 {
		t.Fatalf("error turn must keep its bubble, deleted=%v", del)
	}
}

func TestProgressBubbleCoversPlainPostedTurns(t *testing.T) {
	fc := newFakeClient()
	b := newBot(t, fc, storeRuntime(t, "unused"))

	reply, _ := feed(b, shell3.Event{Kind: shell3.Token, Text: "hi"}, shell3.Event{Kind: shell3.Done})
	if reply != "hi" {
		t.Fatalf("reply=%q", reply)
	}
	if got := fc.sentTexts(); len(got) != 1 || got[0] != "⚙️ working…" {
		t.Fatalf("plain turn did not show the initial working marker: %v", got)
	}
	if len(fc.deletedSnapshot()) != 1 {
		t.Fatal("plain turn must delete its working marker when the reply lands")
	}
}
