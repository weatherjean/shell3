package chat

import (
	"context"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
)

// truncatingClient emits some text and then a terminal event flagged
// Truncated, the way a provider that hit the output cap does.
type truncatingClient struct {
	text      string
	truncated bool
}

func (c truncatingClient) Stream(_ context.Context, _ []llm.Message, _ []llm.ToolDefinition, onEvent func(llm.StreamEvent)) error {
	onEvent(llm.StreamEvent{TextDelta: c.text})
	onEvent(llm.StreamEvent{Done: true, Truncated: c.truncated})
	return nil
}

func TestStreamOnceReportsTruncation(t *testing.T) {
	s, _ := newCollectorSession(SessionOpts{})
	text, _, _, _, truncated, err := streamOnce(context.Background(),
		truncatingClient{text: "1\n2\n3", truncated: true}, nil, nil, s)
	if err != nil {
		t.Fatalf("streamOnce err: %v", err)
	}
	if !truncated {
		t.Fatal("truncation not reported to the caller")
	}
	if text != "1\n2\n3" {
		t.Fatalf("text: %q", text)
	}
}

func TestStreamOnceCleanStopNotTruncated(t *testing.T) {
	s, _ := newCollectorSession(SessionOpts{})
	_, _, _, _, truncated, err := streamOnce(context.Background(),
		truncatingClient{text: "all done"}, nil, nil, s)
	if err != nil {
		t.Fatalf("streamOnce err: %v", err)
	}
	if truncated {
		t.Fatal("clean stop reported as truncated")
	}
}

// The user must SEE that the reply was cut. Front-ends build the reply from
// token events, so the notice has to arrive as one — and it must also be in
// the recorded message, so the model knows on the next round that its own
// previous output was cut off.
func TestTruncatedTurnAppendsVisibleNotice(t *testing.T) {
	s, c := newCollectorSession(SessionOpts{})
	cfg := TurnConfig{LLM: truncatingClient{text: "1\n2\n3", truncated: true}}
	RunTurn(context.Background(), cfg, s, llm.Message{Role: llm.RoleUser, Content: "count to 200"}, nil)

	var streamed strings.Builder
	for _, ev := range c.all() {
		if ev.Kind == EventAssistantToken {
			streamed.WriteString(ev.Text)
		}
	}
	if !strings.Contains(streamed.String(), truncationNotice) {
		t.Fatalf("notice not streamed to the front-end: %q", streamed.String())
	}

	msgs := s.Messages()
	last := msgs[len(msgs)-1]
	if last.Role != llm.RoleAssistant {
		t.Fatalf("last message role: %v", last.Role)
	}
	if !strings.Contains(last.Content, truncationNotice) {
		t.Fatalf("notice missing from recorded message: %q", last.Content)
	}
	if !strings.HasPrefix(last.Content, "1\n2\n3") {
		t.Fatalf("original text lost: %q", last.Content)
	}
}

func TestUntruncatedTurnHasNoNotice(t *testing.T) {
	s, _ := newCollectorSession(SessionOpts{})
	cfg := TurnConfig{LLM: truncatingClient{text: "all done"}}
	RunTurn(context.Background(), cfg, s, llm.Message{Role: llm.RoleUser, Content: "hi"}, nil)

	msgs := s.Messages()
	last := msgs[len(msgs)-1]
	if strings.Contains(last.Content, truncationNotice) {
		t.Fatalf("spurious truncation notice: %q", last.Content)
	}
}
