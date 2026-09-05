package chat

import (
	"context"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
)

type truncatingClient struct {
	text      string
	truncated bool
}

func (c truncatingClient) Stream(_ context.Context, _ []llm.Message, _ []llm.ToolDefinition, onEvent func(llm.StreamEvent)) error {
	onEvent(llm.StreamEvent{TextDelta: c.text})
	onEvent(llm.StreamEvent{Truncated: c.truncated})
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

	msgs := s.messages
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

	msgs := s.messages
	last := msgs[len(msgs)-1]
	if strings.Contains(last.Content, truncationNotice) {
		t.Fatalf("spurious truncation notice: %q", last.Content)
	}
}
