package output

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"charm.land/glamour/v2"
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

// MarkdownEmitter buffers tokens and re-renders markdown on each chunk,
// overwriting previous output in the terminal for a live streaming effect.
type MarkdownEmitter struct {
	w      io.Writer
	buf    strings.Builder
	r      *glamour.TermRenderer
	lines  int // lines printed so far for current assistant turn
}

// NewMarkdownEmitter returns a MarkdownEmitter writing to w.
func NewMarkdownEmitter(w io.Writer) *MarkdownEmitter {
	r, _ := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(100),
	)
	return &MarkdownEmitter{w: w, r: r}
}

// Emit handles an event, re-rendering accumulated markdown on each token.
func (e *MarkdownEmitter) Emit(ev Event) {
	switch ev.Type {
	case EventThinking:
		fmt.Fprintf(e.w, "\033[2mthinking: %s\033[0m\n", ev.Text)
	case EventToken:
		e.buf.WriteString(ev.Text)
		rendered, err := e.r.Render(e.buf.String())
		if err != nil {
			// fallback: print raw token
			fmt.Fprint(e.w, ev.Text)
			return
		}
		// clear previously printed lines
		if e.lines > 0 {
			fmt.Fprintf(e.w, "\033[%dA\033[J", e.lines)
		}
		fmt.Fprint(e.w, rendered)
		e.lines = strings.Count(rendered, "\n")
	case EventToolCall:
		e.lines = 0
		fmt.Fprintf(e.w, "\n\033[33m→ %s\033[0m\n", ev.Tool)
	case EventToolResult:
		fmt.Fprintf(e.w, "%s\n", ev.Text)
	case EventDone:
		e.buf.Reset()
		e.lines = 0
		fmt.Fprintln(e.w)
	case EventError:
		fmt.Fprintf(e.w, "\033[31merror: %s\033[0m\n", ev.Message)
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
