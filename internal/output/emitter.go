package output

import (
	"encoding/json"
	"fmt"
	"io"
)

// Emitter receives agent output events.
type Emitter interface {
	Emit(Event)
}

// EmitterFunc is a func that implements Emitter.
type EmitterFunc func(Event)

// Emit implements Emitter.
func (f EmitterFunc) Emit(e Event) { f(e) }

// PlainEmitter writes human-readable output to w.
type PlainEmitter struct{ w io.Writer }

// NewPlainEmitter returns a PlainEmitter writing to w.
func NewPlainEmitter(w io.Writer) *PlainEmitter { return &PlainEmitter{w} }

// Emit writes ev as human-readable text.
func (e *PlainEmitter) Emit(ev Event) {
	switch ev.Type {
	case EventThinking:
		fmt.Fprintf(e.w, "thinking: %s\n", ev.Text)
	case EventToken:
		fmt.Fprint(e.w, ev.Text)
	case EventToolCall:
		fmt.Fprintf(e.w, "\n[%s] %v\n", ev.Tool, ev.Params)
	case EventToolResult:
		fmt.Fprintf(e.w, "%s\n[%s done]\n", ev.Text, ev.Tool)
	case EventDone:
		fmt.Fprintln(e.w)
	case EventError:
		fmt.Fprintf(e.w, "error: %s\n", ev.Message)
	}
}

// JSONLEmitter writes one JSON line per event to w.
type JSONLEmitter struct{ w io.Writer }

// NewJSONLEmitter returns a JSONLEmitter writing to w.
func NewJSONLEmitter(w io.Writer) *JSONLEmitter { return &JSONLEmitter{w} }

// Emit marshals ev to a JSON line.
func (e *JSONLEmitter) Emit(ev Event) {
	b, _ := json.Marshal(ev)
	fmt.Fprintf(e.w, "%s\n", b)
}
