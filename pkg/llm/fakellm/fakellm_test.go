package fakellm

import (
	"context"
	"testing"

	"github.com/weatherjean/shell3/pkg/llm"
)

func TestClient_ScriptedReply(t *testing.T) {
	c := New(Script{
		Events: []llm.StreamEvent{
			{TextDelta: "hello"},
			{TextDelta: " world"},
		},
	})
	var got []string
	err := c.Stream(context.Background(), nil, nil, func(ev llm.StreamEvent) {
		if ev.TextDelta != "" {
			got = append(got, ev.TextDelta)
		}
	})
	if err != nil {
		t.Fatalf("Stream err: %v", err)
	}
	if len(got) != 2 || got[0] != "hello" || got[1] != " world" {
		t.Errorf("got %v, want [hello, ' world']", got)
	}
	if c.CallCount() != 1 {
		t.Errorf("CallCount = %d, want 1", c.CallCount())
	}
}

func TestClient_MultipleScripts(t *testing.T) {
	c := New(
		Script{Events: []llm.StreamEvent{{TextDelta: "a"}}},
		Script{Events: []llm.StreamEvent{{TextDelta: "b"}}},
	)
	var got []string
	for i := 0; i < 3; i++ {
		_ = c.Stream(context.Background(), nil, nil, func(ev llm.StreamEvent) {
			got = append(got, ev.TextDelta)
		})
	}
	// Third call should repeat the last script.
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "b" {
		t.Errorf("got %v, want [a b b]", got)
	}
	if c.CallCount() != 3 {
		t.Errorf("CallCount = %d, want 3", c.CallCount())
	}
}
