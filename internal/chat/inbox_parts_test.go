package chat

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
	"github.com/weatherjean/shell3/internal/persona"
)

// TestRunParts_UserMessageCarriesParts: the turn's user message keeps Content
// for history/audit AND carries ContentParts = [text, media...] for the wire
// (the adapter ignores Content when ContentParts is set).
func TestRunParts_UserMessageCarriesParts(t *testing.T) {
	fake := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "ok"}}})
	sess, _ := newCollectorSession(SessionOpts{})
	img, _, err := MediaPartFromBytes(pngBytes(t, 2, 2), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	cfg := TurnConfig{LLM: fake, Personality: persona.Persona{SystemPrompt: "t"}, Log: LogOrNoop(nil)}
	sess.RunParts(context.Background(), cfg, "what is this", []llm.ContentPart{img})

	msgs := fake.Calls[0].Msgs
	last := msgs[len(msgs)-1]
	if last.Role != llm.RoleUser || last.Content != "what is this" {
		t.Fatalf("last request message = %+v", last)
	}
	if len(last.ContentParts) != 2 ||
		last.ContentParts[0].Type != llm.ContentPartTypeText || last.ContentParts[0].Text != "what is this" ||
		!strings.HasPrefix(last.ContentParts[1].ImageURL, "data:image/jpeg;base64,") {
		t.Fatalf("ContentParts = %+v, want [text(\"what is this\"), image data URI]", last.ContentParts)
	}
}

// TestRunParts_EmptyText_NoEmptyTextPart: RunParts with empty input text and
// media parts must not prepend an empty text part — some providers reject
// empty text content parts. ContentParts carries the media only.
func TestRunParts_EmptyText_NoEmptyTextPart(t *testing.T) {
	fake := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "ok"}}})
	sess, _ := newCollectorSession(SessionOpts{})
	img, _, err := MediaPartFromBytes(pngBytes(t, 2, 2), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	cfg := TurnConfig{LLM: fake, Personality: persona.Persona{SystemPrompt: "t"}, Log: LogOrNoop(nil)}
	sess.RunParts(context.Background(), cfg, "", []llm.ContentPart{img})

	msgs := fake.Calls[0].Msgs
	last := msgs[len(msgs)-1]
	if len(last.ContentParts) != 1 || last.ContentParts[0].Type != llm.ContentPartTypeImageURL {
		t.Fatalf("ContentParts = %+v, want [image] with no empty text part", last.ContentParts)
	}
}

// TestRunParts_NoParts_PlainMessage: RunParts(nil) and Run produce the
// historical plain user message (no ContentParts) — byte-compatible requests.
func TestRunParts_NoParts_PlainMessage(t *testing.T) {
	fake := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "ok"}}})
	sess, _ := newCollectorSession(SessionOpts{})
	cfg := TurnConfig{LLM: fake, Personality: persona.Persona{SystemPrompt: "t"}, Log: LogOrNoop(nil)}
	sess.RunParts(context.Background(), cfg, "hi", nil)
	msgs := fake.Calls[0].Msgs
	if last := msgs[len(msgs)-1]; last.Content != "hi" || last.ContentParts != nil {
		t.Fatalf("no-parts user message = %+v, want plain Content only", last)
	}
}

// TestInjectReminder_MultimodalUserMessage: when the target user message has
// ContentParts, the reminder must land in its text part too (the adapter
// drops Content), and the original parts slice — shared with sess.messages —
// must not be mutated.
func TestInjectReminder_MultimodalUserMessage(t *testing.T) {
	orig := []llm.ContentPart{
		{Type: llm.ContentPartTypeText, Text: "hi"},
		{Type: llm.ContentPartTypeImageURL, ImageURL: "data:image/jpeg;base64,xxx"},
	}
	msgs := []llm.Message{{Role: llm.RoleUser, Content: "hi", ContentParts: orig}}
	out := injectReminder(msgs, "<system-reminder>r</system-reminder>")
	if !strings.Contains(out[0].ContentParts[0].Text, "<system-reminder>") {
		t.Fatalf("reminder missing from text part: %+v", out[0].ContentParts)
	}
	if strings.Contains(orig[0].Text, "<system-reminder>") {
		t.Fatal("reminder leaked into the shared original parts slice (history pollution)")
	}
}

// TestInterject_PartsMidTurn_DeliveredAfterRound: parts queued during a tool
// round arrive in round 2 as the trailing user media message (the read_media
// injection mechanism), while the steering text rides the usual reminder on
// the last tool message.
func TestInterject_PartsMidTurn_DeliveredAfterRound(t *testing.T) {
	fake := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{
			{ToolCall: &llm.ToolCall{ID: "a", Name: "echo", RawArgs: `{}`}},
		}},
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "seen"}}},
	)
	sess, _ := newCollectorSession(SessionOpts{})
	img, _, err := MediaPartFromBytes(pngBytes(t, 2, 2), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	cfg := TurnConfig{
		LLM:         fake,
		Personality: persona.Persona{SystemPrompt: "t"},
		Handlers: map[string]ToolHandler{"echo": funcHandler{name: "echo",
			fn: func(context.Context, string, json.RawMessage, ToolConfig) (string, error) {
				sess.Interject("look at this", img)
				return "echoed", nil
			}}},
		Log: LogOrNoop(nil),
	}
	RunTurn(context.Background(), cfg, sess, llm.Message{Role: llm.RoleUser, Content: "go"}, nil)

	if fake.CallCount() != 2 {
		t.Fatalf("want 2 LLM rounds, got %d", fake.CallCount())
	}
	round2 := fake.Calls[1].Msgs
	last := round2[len(round2)-1]
	if last.Role != llm.RoleUser || !msgHasMediaPart(last) {
		t.Fatalf("round 2 must end with the injected media user message; got %+v", last)
	}
	var note string
	for _, p := range last.ContentParts {
		if p.Type == llm.ContentPartTypeText {
			note = p.Text
		}
	}
	if !strings.Contains(note, "sent by the user") {
		t.Fatalf("attachment note must say the user sent it: %q", note)
	}
	// The steering text still rides the reminder on the tool message.
	if !strings.Contains(round2[len(round2)-2].Content, "look at this") {
		t.Fatalf("interject text reminder missing: %+v", round2[len(round2)-2])
	}
}

// TestInterject_PartsIdle_DeliveredNextTurn: parts queued while idle are
// injected as a user message at the start of the next turn, right after the
// interject reminder lands on the turn's user message.
func TestInterject_PartsIdle_DeliveredNextTurn(t *testing.T) {
	fake := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "ok"}}})
	sess, _ := newCollectorSession(SessionOpts{})
	aud, _, err := MediaPartFromBytes([]byte("RIFF-fake"), "audio/wav")
	if err != nil {
		t.Fatal(err)
	}
	sess.Interject("voice note", aud)

	cfg := TurnConfig{LLM: fake, Personality: persona.Persona{SystemPrompt: "t"}, Log: LogOrNoop(nil)}
	RunTurn(context.Background(), cfg, sess, llm.Message{Role: llm.RoleUser, Content: "hi"}, nil)

	msgs := fake.Calls[0].Msgs
	last := msgs[len(msgs)-1]
	if last.Role != llm.RoleUser || !msgHasMediaPart(last) {
		t.Fatalf("queued parts must arrive as the final user message; got %+v", last)
	}
	for _, p := range last.ContentParts {
		if p.Type == llm.ContentPartTypeInputAudio && p.AudioFormat != "wav" {
			t.Fatalf("audio format = %q, want wav", p.AudioFormat)
		}
	}
	// The steering text rode the reminder on the turn's user message.
	if !strings.Contains(msgs[len(msgs)-2].Content, "voice note") {
		t.Fatalf("interject reminder missing from user message: %q", msgs[len(msgs)-2].Content)
	}
}
