package openai

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openai/openai-go"
)

// replayCorpus runs a captured MiniMax stream (one raw chunk JSON per line,
// recorded off the wire — see testdata/minimax/README) through the same
// reasoning/content split the live Stream loop uses, and returns what the user
// would see and what would be filed as reasoning.
func replayCorpus(t *testing.T, name string) (visible, reasoning string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "minimax", name+".jsonl"))
	if err != nil {
		t.Skipf("no corpus %s: %v", name, err)
	}
	var vis, rea strings.Builder
	var part tagPartitioner
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var chunk openai.ChatCompletionChunk
		if err := json.Unmarshal([]byte(line), &chunk); err != nil {
			t.Fatalf("corpus %s: %v", name, err)
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		d := chunk.Choices[0].Delta
		r := deltaReasoning(d)
		if r != "" {
			rea.WriteString(r)
		}
		v, rr := part.pushDelta(d.Content, r)
		vis.WriteString(v)
		rea.WriteString(rr)
	}
	v, rr := part.flush()
	vis.WriteString(v)
	rea.WriteString(rr)
	return vis.String(), rea.String()
}

// The bug this whole change exists for: MiniMax streams its reasoning through
// `content` as well as through the reasoning field, so appending every content
// delta splices the model's thinking into its own answer. Replay proves the
// reasoning text no longer appears in what the user sees.
func TestCorpusReasoningStaysOutOfAnswer(t *testing.T) {
	// tags_in_prose is deliberately absent: see TestCorpusTagsInProseIsProviderDamage.
	for _, name := range []string{"plain_answer", "long_prose", "code_fence"} {
		t.Run(name, func(t *testing.T) {
			visible, reasoning := replayCorpus(t, name)
			if strings.TrimSpace(visible) == "" {
				t.Fatal("no visible answer survived the split")
			}
			if strings.TrimSpace(reasoning) == "" {
				t.Fatal("no reasoning captured — the ExtraFields read is not working")
			}
			// The giveaway phrasing of MiniMax's chain-of-thought. If any of it
			// reaches the answer, reasoning is leaking into content again.
			for _, tell := range []string{"The user wants", "The user is asking", "Let me "} {
				if strings.Contains(visible, tell) {
					t.Errorf("reasoning leaked into the answer (%q present)\n--- visible ---\n%s", tell, visible)
				}
			}
			if strings.Contains(visible, "</think>") {
				t.Errorf("think wrapper reached the answer\n--- visible ---\n%s", visible)
			}
		})
	}
}

// A plain arithmetic answer must survive intact — the split must not be so
// aggressive that it eats short replies.
func TestCorpusPlainAnswerIntact(t *testing.T) {
	visible, _ := replayCorpus(t, "plain_answer")
	if !strings.Contains(visible, "391") {
		t.Fatalf("17*23 answer lost in the split: %q", visible)
	}
}

// deltaReasoning must take exactly one field even when a gateway populates
// several with the same text, or the reasoning is emitted twice.
func TestDeltaReasoningPrefersOneField(t *testing.T) {
	var d openai.ChatCompletionChunkChoiceDelta
	if err := json.Unmarshal([]byte(`{"content":"","reasoning":"dup","reasoning_content":"dup"}`), &d); err != nil {
		t.Fatal(err)
	}
	if got := deltaReasoning(d); got != "dup" {
		t.Fatalf("want single %q, got %q", "dup", got)
	}
	var empty openai.ChatCompletionChunkChoiceDelta
	if err := json.Unmarshal([]byte(`{"content":"hello"}`), &empty); err != nil {
		t.Fatal(err)
	}
	if got := deltaReasoning(empty); got != "" {
		t.Fatalf("want no reasoning, got %q", got)
	}
}

func TestVisibleContent(t *testing.T) {
	cases := []struct{ name, content, reasoning, want string }{
		{"no reasoning passes through", "the answer", "", "the answer"},
		{"exact duplicate dropped", "thinking out loud", "thinking out loud", ""},
		{"wrapper glued to duplicate dropped", "<think>\nThe", "The", ""},
		{"genuine answer beside reasoning kept", "the answer", "different thought", "the answer"},
		{"empty content stays empty", "", "some reasoning", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := visibleContent(c.content, c.reasoning); got != c.want {
				t.Fatalf("visibleContent(%q, %q) = %q, want %q", c.content, c.reasoning, got, c.want)
			}
		})
	}
}

// A reply that itself discusses <think> tags cannot be cleanly split, and that
// is a provider defect, not an adapter one. Captured from the wire: MiniMax
// interleaves the model's reasoning and its answer through `content` at
// sub-sentence granularity, with no field distinguishing them —
//
//	13 content-only  'So this regex matches a string that contains "'   (reasoning)
//	24 content-only  'It matches a'                                     (answer)
//	21 content-only  ' would match... The user wants one short'          (reasoning)
//
// — so no client-side rule can separate them. What the adapter CAN guarantee is
// that the wrapper tags never reach the user and the answer is not lost
// wholesale. This test pins that weaker contract so a future change that
// silently starts dropping the whole reply gets caught, and documents why the
// stronger contract is not met here.
func TestCorpusTagsInProseIsProviderDamage(t *testing.T) {
	visible, reasoning := replayCorpus(t, "tags_in_prose")
	if strings.Contains(visible, "<think>\nThe") {
		t.Error("the reasoning wrapper reached the answer")
	}
	if strings.TrimSpace(reasoning) == "" {
		t.Error("no reasoning captured at all")
	}
	if !strings.Contains(visible, "It matches a") {
		t.Errorf("the model's actual answer was lost entirely:\n%s", visible)
	}
}
