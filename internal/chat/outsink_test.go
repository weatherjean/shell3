package chat

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/patchapp"
)

func TestSink_StartEndBracket(t *testing.T) {
	var buf bytes.Buffer
	s := newOutSink(&buf, time.Unix(0, 0).UTC())
	s.WriteStart("hi", "default", "gpt-5", "/tmp/out", true)
	s.WriteEnd("ok")
	out := buf.String()
	if !strings.Contains(out, `"kind":"start"`) || !strings.Contains(out, `"input":"hi"`) {
		t.Fatalf("missing start: %q", out)
	}
	if !strings.Contains(out, `"kind":"end"`) || !strings.Contains(out, `"status":"ok"`) {
		t.Fatalf("missing end: %q", out)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
}

func TestSink_StripsANSIFromText(t *testing.T) {
	var buf bytes.Buffer
	s := newOutSink(&buf, time.Unix(0, 0).UTC())
	s.WriteEvent(patchapp.ChunkEvent{Text: "\x1b[31mhello\x1b[0m world"})
	s.WriteEnd("ok") // flush
	if strings.Contains(buf.String(), "\x1b[") {
		t.Fatalf("ANSI not stripped: %q", buf.String())
	}
	if !strings.Contains(buf.String(), `"text":"hello world"`) {
		t.Fatalf("expected stripped text, got %q", buf.String())
	}
}

func TestSink_AccumulatesTextUntilBoundary(t *testing.T) {
	var buf bytes.Buffer
	s := newOutSink(&buf, time.Unix(0, 0).UTC())
	// Stream 4 text chunks with no boundary — nothing should be written yet.
	s.WriteEvent(patchapp.ChunkEvent{Text: "Hello"})
	s.WriteEvent(patchapp.ChunkEvent{Text: " "})
	s.WriteEvent(patchapp.ChunkEvent{Text: "world"})
	s.WriteEvent(patchapp.ChunkEvent{Text: "!"})
	if buf.Len() != 0 {
		t.Fatalf("text should accumulate, got premature write: %q", buf.String())
	}
	// Boundary event flushes pending text first, then emits itself.
	s.WriteEvent(patchapp.UsageEvent{Usage: llm.Usage{TotalTokens: 5}})
	out := buf.String()
	if !strings.Contains(out, `"kind":"text","text":"Hello world!"`) {
		t.Fatalf("expected single coalesced text event, got %q", out)
	}
	if !strings.Contains(out, `"kind":"usage"`) {
		t.Fatalf("expected usage event after flush, got %q", out)
	}
	// Order: text flush before usage.
	textIdx := strings.Index(out, `"kind":"text"`)
	usageIdx := strings.Index(out, `"kind":"usage"`)
	if textIdx == -1 || usageIdx == -1 || textIdx > usageIdx {
		t.Fatalf("text must appear before usage, got %q", out)
	}
}

func TestSink_AccumulatesReasoningSeparately(t *testing.T) {
	var buf bytes.Buffer
	s := newOutSink(&buf, time.Unix(0, 0).UTC())
	s.WriteEvent(patchapp.ReasoningChunkEvent{Text: "think"})
	s.WriteEvent(patchapp.ReasoningChunkEvent{Text: "ing..."})
	s.WriteEvent(patchapp.ChunkEvent{Text: "reply"})
	s.WriteEnd("ok") // flush both
	out := buf.String()
	if !strings.Contains(out, `"kind":"reasoning","text":"thinking..."`) {
		t.Fatalf("expected coalesced reasoning, got %q", out)
	}
	if !strings.Contains(out, `"kind":"text","text":"reply"`) {
		t.Fatalf("expected coalesced text, got %q", out)
	}
	// Reasoning flushes before text (model thinks before speaking).
	rIdx := strings.Index(out, `"kind":"reasoning"`)
	tIdx := strings.Index(out, `"kind":"text"`)
	if rIdx == -1 || tIdx == -1 || rIdx > tIdx {
		t.Fatalf("reasoning must appear before text, got %q", out)
	}
}

func TestSink_AllEventKinds(t *testing.T) {
	var buf bytes.Buffer
	s := newOutSink(&buf, time.Unix(0, 0).UTC())
	s.WriteEvent(patchapp.ChunkEvent{Text: "a"})
	s.WriteEvent(patchapp.ReasoningChunkEvent{Text: "b"})
	s.WriteEvent(patchapp.AppendEvent{Text: "c"})
	s.WriteEvent(patchapp.UsageEvent{Usage: llm.Usage{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3}})
	s.WriteEvent(patchapp.TurnDoneEvent{Usage: llm.Usage{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3}})
	s.WriteEvent(patchapp.TurnErrEvent{Err: errString("boom")})
	s.WriteEvent(patchapp.TTYExecEvent{Cmd: "vim x", WorkDir: "/tmp"})

	want := []string{
		`"kind":"text"`, `"kind":"reasoning"`, `"kind":"tool"`,
		`"kind":"usage"`, `"kind":"turn_done"`,
		`"kind":"error"`, `"kind":"tty_exec_request"`,
	}
	for _, w := range want {
		if !strings.Contains(buf.String(), w) {
			t.Errorf("missing %q in %q", w, buf.String())
		}
	}
}

type errString string

func (e errString) Error() string { return string(e) }
