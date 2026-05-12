package chat

import (
	"encoding/json"
	"io"
	"sync"
	"time"

	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/patchapp"
	"github.com/weatherjean/shell3/internal/patchtui"
)

// outSink writes one JSONL event per call to a writer. Safe for concurrent
// writes; serializes through an internal mutex. All text payloads have ANSI
// escape sequences stripped before serialization.
type outSink struct {
	mu  sync.Mutex
	w   io.Writer
	now func() time.Time
}

func newOutSink(w io.Writer, fixed time.Time) *outSink {
	now := func() time.Time { return time.Now().UTC() }
	if !fixed.IsZero() {
		now = func() time.Time { return fixed }
	}
	return &outSink{w: w, now: now}
}

type outEvent struct {
	TS         string `json:"ts"`
	Kind       string `json:"kind"`
	Text       string `json:"text,omitempty"`
	Raw        string `json:"raw,omitempty"`
	Input      string `json:"input,omitempty"`
	Persona    string `json:"persona,omitempty"`
	Model      string `json:"model,omitempty"`
	Out        string `json:"out,omitempty"`
	Headless   *bool  `json:"headless,omitempty"`
	Cmd        string `json:"cmd,omitempty"`
	WorkDir    string `json:"workdir,omitempty"`
	Error      string `json:"error,omitempty"`
	Status     string `json:"status,omitempty"`
	Prompt     int    `json:"prompt,omitempty"`
	Completion int    `json:"completion,omitempty"`
	Total      int    `json:"total,omitempty"`
}

func (s *outSink) write(e outEvent) {
	if s == nil {
		return
	}
	e.TS = s.now().Format(time.RFC3339Nano)
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := json.Marshal(e)
	if err != nil {
		return
	}
	data = append(data, '\n')
	_, _ = s.w.Write(data)
}

// WriteStart emits the first line of the JSONL stream.
func (s *outSink) WriteStart(input, persona, model, out string, headless bool) {
	h := headless
	s.write(outEvent{Kind: "start", Input: input, Persona: persona, Model: model, Out: out, Headless: &h})
}

// WriteEnd emits the final line. status should be "ok" or "error".
func (s *outSink) WriteEnd(status string) {
	s.write(outEvent{Kind: "end", Status: status})
}

// WriteEvent maps a patchapp.Event to its JSONL form.
func (s *outSink) WriteEvent(ev patchapp.Event) {
	switch v := ev.(type) {
	case patchapp.ChunkEvent:
		s.write(outEvent{Kind: "text", Text: patchtui.StripANSI(v.Text)})
	case patchapp.ReasoningChunkEvent:
		s.write(outEvent{Kind: "reasoning", Text: patchtui.StripANSI(v.Text)})
	case patchapp.AppendEvent:
		s.write(outEvent{Kind: "tool", Raw: patchtui.StripANSI(v.Text)})
	case patchapp.UsageEvent:
		s.write(usageEv("usage", v.Usage))
	case patchapp.TurnDoneEvent:
		s.write(usageEv("turn_done", v.Usage))
	case patchapp.TurnErrEvent:
		msg := ""
		if v.Err != nil {
			msg = v.Err.Error()
		}
		s.write(outEvent{Kind: "error", Error: msg})
	case patchapp.TTYExecEvent:
		s.write(outEvent{Kind: "tty_exec_request", Cmd: v.Cmd, WorkDir: v.WorkDir})
	}
}

func usageEv(kind string, u llm.Usage) outEvent {
	return outEvent{
		Kind:       kind,
		Prompt:     u.PromptTokens,
		Completion: u.CompletionTokens,
		Total:      u.TotalTokens,
	}
}
