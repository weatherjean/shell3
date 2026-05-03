package patchwidgets

import (
	"strings"
	"testing"
)

// ---- result helpers ----

func TestResultHelpers(t *testing.T) {
	r := okResult("hello")
	if !r.OK || r.Value != "hello" || r.Reason != ReasonOK {
		t.Fatalf("okResult: %+v", r)
	}

	r2 := okIndex("pick", 3)
	if !r2.OK || r2.Value != "pick" || r2.Index == nil || *r2.Index != 3 {
		t.Fatalf("okIndex: %+v", r2)
	}

	if c := cancelResult(); c.OK || c.Reason != ReasonCancel {
		t.Fatalf("cancelResult: %+v", c)
	}
	if to := timeoutResult(); to.OK || to.Reason != ReasonTimeout {
		t.Fatalf("timeoutResult: %+v", to)
	}
	if e := eofResult(); e.OK || e.Reason != ReasonEOF {
		t.Fatalf("eofResult: %+v", e)
	}
}

// ---- render helpers ----

func TestRenderHelpers(t *testing.T) {
	// dim / muted / boldP all return ANSI-wrapped strings containing the input.
	for name, fn := range map[string]func(string) string{
		"dim":   dim,
		"muted": muted,
		"boldP": boldP,
	} {
		out := fn("text")
		if !strings.Contains(out, "text") {
			t.Errorf("%s: output %q does not contain input", name, out)
		}
	}
}

func TestTitleLine(t *testing.T) {
	line := titleLine("What is your name?", "(optional)")
	if !strings.Contains(line, "What is your name?") {
		t.Errorf("titleLine missing question: %q", line)
	}
	if !strings.Contains(line, "(optional)") {
		t.Errorf("titleLine missing hint: %q", line)
	}

	noHint := titleLine("Question?", "")
	if strings.Contains(noHint, "(optional)") {
		t.Errorf("titleLine with empty hint should not include hint: %q", noHint)
	}
}

func TestHintLine(t *testing.T) {
	line := hintLine("enter: submit", "esc: cancel")
	if !strings.Contains(line, "enter: submit") || !strings.Contains(line, "esc: cancel") {
		t.Errorf("hintLine missing parts: %q", line)
	}
}

// ---- PickChoice.Display ----

func TestPickChoiceDisplay(t *testing.T) {
	c := PickChoice{Value: "v", Label: "Human label"}
	if c.Display() != "Human label" {
		t.Fatalf("expected label, got %q", c.Display())
	}
	c2 := PickChoice{Value: "v"}
	if c2.Display() != "v" {
		t.Fatalf("expected value fallback, got %q", c2.Display())
	}
}
