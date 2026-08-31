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
	if len(sent) == 0 || !strings.Contains(sent[0], "⚙️ bash — cd /x && python3") {
		t.Fatalf("bubble not posted or unreadable: %v", sent)
	}
	if strings.Contains(sent[0], "\nimport json") {
		t.Fatalf("bubble leaked a multi-line command body: %q", sent[0])
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

func TestProgressBubbleAbsentForPlainReplies(t *testing.T) {
	fc := newFakeClient()
	b := newBot(t, fc, storeRuntime(t, "unused"))

	reply, _ := feed(b, shell3.Event{Kind: shell3.Token, Text: "hi"}, shell3.Event{Kind: shell3.Done})
	if reply != "hi" {
		t.Fatalf("reply=%q", reply)
	}
	if len(fc.sentTexts()) != 0 || len(fc.deletedSnapshot()) != 0 {
		t.Fatal("no-tool turn must not post or delete anything")
	}
}
