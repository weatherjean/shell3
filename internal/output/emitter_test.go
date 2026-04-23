package output_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/output"
)

func TestPlainEmitter(t *testing.T) {
	var buf bytes.Buffer
	e := output.NewPlainEmitter(&buf)
	e.Emit(output.Event{Type: output.EventToken, Text: "hello"})
	e.Emit(output.Event{Type: output.EventToken, Text: " world"})
	e.Emit(output.Event{Type: output.EventDone, Text: "hello world"})
	if !strings.Contains(buf.String(), "hello") {
		t.Errorf("expected output to contain 'hello', got: %q", buf.String())
	}
}

func TestJSONLEmitter(t *testing.T) {
	var buf bytes.Buffer
	e := output.NewJSONLEmitter(&buf)
	e.Emit(output.Event{Type: output.EventToken, Text: "hi"})
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	var ev output.Event
	if err := json.Unmarshal([]byte(lines[0]), &ev); err != nil {
		t.Fatal(err)
	}
	if ev.Type != output.EventToken || ev.Text != "hi" {
		t.Errorf("unexpected event: %+v", ev)
	}
}
