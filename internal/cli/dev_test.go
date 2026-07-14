package cli

import (
	"errors"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/shell3"
)

// renderDevEvents must surface every event kind verbosely: the tool call with
// its args, the full tool result, reasoning, the reply, and token usage. A
// stripped (ANSI-free) render is asserted so the check is style-independent.
func TestRenderDevEvents(t *testing.T) {
	ch := make(chan shell3.Event, 8)
	ch <- shell3.Event{Kind: shell3.Reasoning, Text: "let me check"}
	ch <- shell3.Event{Kind: shell3.ToolCall, ToolName: "bash", ToolInput: `{"command":"echo hi"}`}
	ch <- shell3.Event{Kind: shell3.ToolResult, ToolName: "bash", ToolOutput: "hi\n"}
	ch <- shell3.Event{Kind: shell3.Token, Text: "done."}
	ch <- shell3.Event{Kind: shell3.Done, PromptTokens: 10, CompletionTokens: 3, TotalTokens: 13}
	close(ch)

	var b strings.Builder
	if err := renderDevEvents(&b, ch); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := strip(b.String())
	for _, want := range []string{"thinking", "let me check", "bash", "echo hi", "hi", "done.", "13 tokens"} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q; got:\n%s", want, out)
		}
	}
}

// An Error event makes renderDevEvents report the turn as failed.
func TestRenderDevEvents_Error(t *testing.T) {
	ch := make(chan shell3.Event, 2)
	ch <- shell3.Event{Kind: shell3.Error, Err: errors.New("boom")}
	close(ch)

	var b strings.Builder
	if err := renderDevEvents(&b, ch); err == nil {
		t.Fatal("expected errDevTurnFailed for an Error event")
	}
	if !strings.Contains(strip(b.String()), "boom") {
		t.Errorf("error text not rendered; got:\n%s", b.String())
	}
}

// strip removes ANSI SGR sequences so assertions are style-independent.
func strip(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
