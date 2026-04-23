package agent_test

import (
	"context"
	"testing"

	"github.com/weatherjean/shell3/internal/agent"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/output"
)

type fakeClient struct{ responses []string }

func (f *fakeClient) Stream(_ context.Context, _ []llm.Message, _ []llm.ToolDefinition, onEvent func(llm.StreamEvent)) error {
	for _, r := range f.responses {
		onEvent(llm.StreamEvent{TextDelta: r})
	}
	onEvent(llm.StreamEvent{Done: true})
	return nil
}

func TestAgentRun_SimpleResponse(t *testing.T) {
	client := &fakeClient{responses: []string{"hello ", "world"}}
	var events []output.Event
	emit := output.EmitterFunc(func(e output.Event) { events = append(events, e) })

	cfg := agent.Config{
		SystemPrompt: "you are helpful",
		LLM:          client,
		Emitter:      emit,
	}
	sess := &agent.Session{}
	if err := agent.RunTurn(context.Background(), cfg, sess, "hi"); err != nil {
		t.Fatal(err)
	}

	var done *output.Event
	for i := range events {
		if events[i].Type == output.EventDone {
			done = &events[i]
		}
	}
	if done == nil {
		t.Fatal("expected EventDone")
	}
	if done.Text != "hello world" {
		t.Errorf("got %q", done.Text)
	}
}
