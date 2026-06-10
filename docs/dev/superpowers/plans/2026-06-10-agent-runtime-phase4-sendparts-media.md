# Agent Runtime Phase 4: `SendParts` Inbound Media — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Hosts can attach images and audio to a turn (`Session.SendParts`) and to mid-turn steering (`Interject(text, parts...)`), from a file path or from in-memory bytes — Telegram voice notes and photos flow in without touching disk. Parts ride the same `llm.ContentPart` plumbing `read_media` already uses, with the same size caps.

**Architecture:** `internal/chat` gains MIME-routed byte loaders (`MediaPartFromBytes`, factored from the path loaders, which stay) and exports `LoadMediaPart`; the inbox becomes `[]inboxItem{text, parts}`; `Session.RunParts` builds a multimodal user message and `Run` delegates to it; the two turn-loop drain sites deliver queued parts via the existing media-injection mechanism (generalized into `attachmentsMessage`, shared with `read_media`); `injectReminder` learns to mirror reminders into the text part of multimodal user messages (the adapter ignores `Content` when `ContentParts` is set). `pkg/shell3` adds the public `Part`/`PartKind` surface, `SendParts` (with `Send` delegating), and a variadic `Interject`. The TUI only updates its narrow `session` interface signature.

**Tech Stack:** Go 1.25, fakellm. Branch: `agent-runtime`. Spec: `docs/dev/superpowers/specs/2026-06-10-agent-runtime-design.md` ("Inbound media").

**Conventions:** TDD, `go test -race -count=1`, `make lint`, one commit per task, doc comments state contracts.

**Key facts the design rests on** (verified against current code):

- `internal/adapter/openai/client.go` `toMessages`: for a user message with `len(ContentParts) > 0`, ONLY the parts are serialized — `Content` is ignored on the wire. So every multimodal user message must carry its text as a leading text part, and reminders appended to `Content` must also land in a text part.
- `internal/chat/turn.go` already injects `read_media` parts as a synthetic user message after the tool round (tool messages can't carry media; the adapter renders image/audio parts only on user messages). User-attached parts reuse exactly this mechanism.
- Audit JSONL: the sink (`outsink.go WriteChatEvent`) serializes `chat.Event` values, not `llm.Message`s. The `user_message` event carries `Text` only, and the injected media user messages are appended to history without emitting any event — so **media bytes never reach the audit log** (deliberate: no megabytes of base64 per attachment; same as `read_media` today). The audit shows the `user_message` line with the prompt text, and for mid-turn parts the `system_reminder` line with the steering text. No new event kinds.
- Store history (`saveHistory` → `appendHistory`) records `m.Content` for user messages, so the injected media messages show their bracketed `[attached …]` label and the SendParts user message shows the prompt — both readable, no blobs.

---

### Task 1: internal/chat — byte-based media loaders

**Files:**
- Modify: `internal/chat/media.go` (export `LoadMediaPart`, add `MediaPartFromBytes`), `internal/chat/image.go` (add `imagePartFromBytes`), `internal/chat/audio.go` (add `audioPartFromBytes`, delegate from `loadAudioPart`)
- Test: `internal/chat/media_bytes_test.go` (new)

- [ ] **Step 1: Failing tests** (`internal/chat/media_bytes_test.go`):

```go
package chat

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
)

// pngBytes encodes a w×h solid-red PNG in memory (no disk; the byte loaders
// must work without paths). Distinct from image_test.go's writePNG, which
// writes a file.
func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: 255, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestMediaPartFromBytes_PNG: image bytes become a resized-JPEG data-URI part
// with the same description format loadMediaPart produces.
func TestMediaPartFromBytes_PNG(t *testing.T) {
	part, desc, err := MediaPartFromBytes(pngBytes(t, 3, 2), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if part.Type != llm.ContentPartTypeImageURL || !strings.HasPrefix(part.ImageURL, "data:image/jpeg;base64,") {
		t.Fatalf("part = %+v", part)
	}
	if desc != "image 3x2" {
		t.Fatalf("desc = %q, want \"image 3x2\"", desc)
	}
}

// TestMediaPartFromBytes_MIMENormalization: case and ";"-parameters are
// tolerated (Telegram and HTTP stacks send e.g. "audio/ogg; codecs=opus" —
// for supported types we must accept "image/PNG; charset=binary" forms).
func TestMediaPartFromBytes_MIMENormalization(t *testing.T) {
	if _, _, err := MediaPartFromBytes(pngBytes(t, 1, 1), "IMAGE/PNG; charset=binary"); err != nil {
		t.Fatalf("MIME params/case should be tolerated: %v", err)
	}
}

// TestMediaPartFromBytes_Audio: every accepted audio MIME maps to the right
// wire format and the data is base64 of the input, untranscoded.
func TestMediaPartFromBytes_Audio(t *testing.T) {
	raw := []byte("RIFF....fake-wav-payload")
	for mime, format := range map[string]string{
		"audio/wav": "wav", "audio/x-wav": "wav", "audio/wave": "wav",
		"audio/mpeg": "mp3", "audio/mp3": "mp3",
	} {
		part, desc, err := MediaPartFromBytes(raw, mime)
		if err != nil {
			t.Fatalf("%s: %v", mime, err)
		}
		if part.Type != llm.ContentPartTypeInputAudio || part.AudioFormat != format {
			t.Fatalf("%s: part = %+v, want input_audio/%s", mime, part, format)
		}
		if part.AudioData != base64.StdEncoding.EncodeToString(raw) {
			t.Fatalf("%s: AudioData is not the base64 of the input", mime)
		}
		if !strings.Contains(desc, format+" audio") {
			t.Fatalf("%s: desc = %q", mime, desc)
		}
	}
}

// TestMediaPartFromBytes_UnsupportedMIME: anything else errors with guidance.
func TestMediaPartFromBytes_UnsupportedMIME(t *testing.T) {
	if _, _, err := MediaPartFromBytes([]byte("x"), "video/mp4"); err == nil || !strings.Contains(err.Error(), "unsupported MIME") {
		t.Fatalf("want unsupported-MIME error, got %v", err)
	}
}

// TestMediaPartFromBytes_SizeCaps: the path loaders' caps apply to bytes too.
func TestMediaPartFromBytes_SizeCaps(t *testing.T) {
	if _, _, err := MediaPartFromBytes(make([]byte, maxImageBytes+1), "image/png"); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("image cap not enforced: %v", err)
	}
	if _, _, err := MediaPartFromBytes(make([]byte, maxAudioBytes+1), "audio/wav"); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("audio cap not enforced: %v", err)
	}
}
```

- [ ] **Step 2: Verify failure** — `go test ./internal/chat -run TestMediaPartFromBytes -v` → FAIL (`undefined: MediaPartFromBytes`).

- [ ] **Step 3: Implement.**

`media.go` — rename `loadMediaPart` → `LoadMediaPart` (it's the path entry point pkg/shell3 needs; update the one call site in `handleReadMedia` and the three uses in `media_test.go`; extend its doc comment to note it is consumed by pkg/shell3's `Part{Path: …}`). Then add:

```go
// MediaPartFromBytes converts in-memory media bytes into a multimodal
// ContentPart, routing by MIME type — the byte-based sibling of LoadMediaPart
// for hosts that hold the data directly (e.g. a Telegram photo download).
// Matching is case-insensitive and parameters after ";" are ignored:
//
//	image/jpeg, image/png, image/gif, image/webp → image_url (resized,
//	    JPEG-encoded base64 data URI, like read_media)
//	audio/wav, audio/x-wav, audio/wave           → input_audio, format "wav"
//	audio/mpeg, audio/mp3                        → input_audio, format "mp3"
//
// The path loaders' size caps apply (maxImageBytes / maxAudioBytes). Returns
// the part plus a short human-readable description.
func MediaPartFromBytes(data []byte, mime string) (llm.ContentPart, string, error) {
	mt := strings.ToLower(strings.TrimSpace(mime))
	if i := strings.IndexByte(mt, ';'); i >= 0 {
		mt = strings.TrimSpace(mt[:i])
	}
	switch mt {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		return imagePartFromBytes(data)
	case "audio/wav", "audio/x-wav", "audio/wave":
		return audioPartFromBytes(data, "wav")
	case "audio/mpeg", "audio/mp3":
		return audioPartFromBytes(data, "mp3")
	default:
		return llm.ContentPart{}, "", fmt.Errorf("unsupported MIME type %q — use image/jpeg, image/png, image/gif, image/webp, audio/wav, or audio/mpeg", mime)
	}
}
```

`image.go` — the byte half of `loadImagePart` (which keeps its path/stat logic and `(part, w, h, err)` signature — `image_test.go` calls it directly):

```go
// imagePartFromBytes validates the size cap, downscales/JPEG-encodes raw image
// bytes via resizeAndEncodeJPEG, and returns an image_url ContentPart plus an
// "image WxH" description (source pixel dimensions).
func imagePartFromBytes(data []byte) (llm.ContentPart, string, error) {
	if len(data) > maxImageBytes {
		return llm.ContentPart{}, "", fmt.Errorf("image too large (%d MB, max 10 MB)", len(data)>>20)
	}
	encoded, w, h, err := resizeAndEncodeJPEG(data, maxImageSide, jpegQuality)
	if err != nil {
		return llm.ContentPart{}, "", fmt.Errorf("image encode: %w", err)
	}
	return llm.ContentPart{
		Type:     llm.ContentPartTypeImageURL,
		ImageURL: "data:image/jpeg;base64," + encoded,
	}, fmt.Sprintf("image %dx%d", w, h), nil
}
```

`audio.go` — factor the encode tail out of `loadAudioPart` and delegate so the two stay in sync:

```go
// audioPartFromBytes validates the size cap and wraps raw audio bytes as a
// base64 input_audio ContentPart. format must be "wav" or "mp3" (the wire
// formats); audio is never decoded or transcoded.
func audioPartFromBytes(data []byte, format string) (llm.ContentPart, string, error) {
	if len(data) > maxAudioBytes {
		return llm.ContentPart{}, "", fmt.Errorf("audio too large (%d MB, max 25 MB)", len(data)>>20)
	}
	desc := fmt.Sprintf("%s audio, %.1f MB", format, float64(len(data))/(1<<20))
	return llm.ContentPart{
		Type:        llm.ContentPartTypeInputAudio,
		AudioData:   base64.StdEncoding.EncodeToString(data),
		AudioFormat: format,
	}, desc, nil
}
```

`loadAudioPart`'s tail (after `os.ReadFile`) becomes `return audioPartFromBytes(raw, strings.TrimPrefix(ext, "."))` — drop the now-duplicated stat-based size check ONLY if the byte check produces the same message; the `os.Stat` early-out stays (it avoids reading a 2 GB file just to reject it).

- [ ] **Step 4:** `go test -race -count=1 ./internal/chat && make lint` → PASS (new tests + existing `media_test.go`/`audio_test.go`/`image_test.go` after the rename).

- [ ] **Step 5: Commit**

```bash
git add internal/chat && git commit -m "feat(chat): MIME-routed byte media loaders; export LoadMediaPart"
```

---

### Task 2: internal/chat — parts-capable RunParts + media-carrying inbox

**Files:**
- Modify: `internal/chat/session.go` (inboxItem, Interject variadic, drainInbox split, injectReminder multimodal), `internal/chat/session_lifecycle.go` (RunParts, Run delegates), `internal/chat/turn.go` (drain sites, `attachmentsMessage` replacing the inline pendingMedia block)
- Test: `internal/chat/inbox_parts_test.go` (new), plus one `injectReminder` case in `internal/chat/inbox_test.go` or a small new test in `inbox_parts_test.go` (shown below)

- [ ] **Step 1: Failing tests** (`internal/chat/inbox_parts_test.go` — `pngBytes`, `msgHasMediaPart`, `newCollectorSession`, `funcHandler` are existing same-package test helpers):

```go
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
```

- [ ] **Step 2: Verify failure** — `go test ./internal/chat -run 'TestRunParts|TestInjectReminder_Multimodal|TestInterject_Parts' -v` → FAIL (`RunParts` undefined; `Interject` takes 1 arg; `drainInbox` shape).

- [ ] **Step 3: Implement.**

`session.go` — inbox becomes item-shaped (add `"slices"` to imports):

```go
// inboxItem is one queued Interject: steering text plus optional media parts.
type inboxItem struct {
	text  string
	parts []llm.ContentPart
}
```

Field: `inbox []inboxItem` (update the field comment to mention parts). Interject (doc gains one sentence: "parts (images/audio) are delivered alongside the text via a synthetic user message — see drainInbox's callers."):

```go
func (s *Session) Interject(text string, parts ...llm.ContentPart) {
	s.inboxMu.Lock()
	defer s.inboxMu.Unlock()
	s.inbox = append(s.inbox, inboxItem{text: text, parts: slices.Clone(parts)})
}
```

```go
// drainInbox removes all queued interjections, returning the steering texts
// (in arrival order, feeding interjectReminder) and the flattened media parts
// (same order, feeding attachmentsMessage). Called only from the turn
// goroutine.
func (s *Session) drainInbox() (texts []string, parts []llm.ContentPart) {
	s.inboxMu.Lock()
	defer s.inboxMu.Unlock()
	for _, it := range s.inbox {
		texts = append(texts, it.text)
		parts = append(parts, it.parts...)
	}
	s.inbox = nil
	return texts, parts
}
```

`interjectReminder([]string)` is unchanged. `injectReminder` gains the multimodal branch (replace the user-message body; doc comment gains: "When the user message is multimodal the reminder is mirrored into its text part — the adapter sends ContentParts and ignores Content — on a cloned parts slice so the message stored in sess.messages stays reminder-free."):

```go
		if msgs[i].Role == llm.RoleUser {
			msgs[i].Content = msgs[i].Content + "\n\n" + reminder
			if len(msgs[i].ContentParts) > 0 {
				parts := slices.Clone(msgs[i].ContentParts)
				appended := false
				for j := range parts {
					if parts[j].Type == llm.ContentPartTypeText {
						parts[j].Text += "\n\n" + reminder
						appended = true
						break
					}
				}
				if !appended {
					parts = append(parts, llm.ContentPart{Type: llm.ContentPartTypeText, Text: reminder})
				}
				msgs[i].ContentParts = parts
			}
			return msgs
		}
```

`session_lifecycle.go` — `RunParts`, with `Run` delegating (move Run's doc onto RunParts; Run's doc shrinks to "Run executes one text-only turn; see RunParts."):

```go
// RunParts executes one user→assistant turn whose user message carries media
// parts alongside the prompt text. With parts the message gets
// ContentParts = [text(input), parts...]; Content stays set to input for
// history rows and the user_message audit event (the openai adapter sends
// ContentParts and ignores Content when both are present). Emits the
// user_message event, runs the turn loop, and (if cfg.Store is non-nil)
// persists newly appended messages before the terminal event fires. Blocks
// until the turn completes.
func (s *Session) RunParts(ctx context.Context, cfg TurnConfig, input string, parts []llm.ContentPart) {
	emitUserMessage(s, input)
	from := len(s.messages)
	persist := func() {
		if cfg.Store != nil {
			saveHistory(cfg.Store, LogOrNoop(cfg.Log), s, s.id, from)
		}
	}
	userMsg := llm.Message{Role: llm.RoleUser, Content: input}
	if len(parts) > 0 {
		cps := make([]llm.ContentPart, 0, len(parts)+1)
		cps = append(cps, llm.ContentPart{Type: llm.ContentPartTypeText, Text: input})
		cps = append(cps, parts...)
		userMsg.ContentParts = cps
	}
	RunTurn(ctx, cfg, s, userMsg, persist)
}

// Run executes one text-only user→assistant turn; see RunParts.
func (s *Session) Run(ctx context.Context, cfg TurnConfig, input string) {
	s.RunParts(ctx, cfg, input, nil)
}
```

`turn.go` — generalize the pendingMedia injection into a helper (add `"strings"` is already imported):

```go
// attachmentsMessage builds the synthetic user message that delivers media
// parts mid-conversation: read_media loads from the last tool round and/or
// attachments the user sent via Interject. Tool messages can't carry media
// and the adapter renders image/audio parts only on user messages, so this is
// the single injection point. The trailing text part tells the model where
// the media came from. ok is false when there is nothing to deliver.
func attachmentsMessage(readMedia, userSent []llm.ContentPart) (llm.Message, bool) {
	total := len(readMedia) + len(userSent)
	if total == 0 {
		return llm.Message{}, false
	}
	parts := make([]llm.ContentPart, 0, total+1)
	parts = append(parts, readMedia...)
	parts = append(parts, userSent...)
	var notes []string
	if len(readMedia) > 0 {
		notes = append(notes, fmt.Sprintf("%d file(s) you loaded with read_media", len(readMedia)))
	}
	if len(userSent) > 0 {
		notes = append(notes, fmt.Sprintf("%d attachment(s) sent by the user", len(userSent)))
	}
	label := strings.Join(notes, "; ")
	parts = append(parts, llm.ContentPart{
		Type: llm.ContentPartTypeText,
		Text: "Above are the attached media file(s): " + label + ".",
	})
	return llm.Message{
		Role:         llm.RoleUser,
		Content:      "[attached: " + label + "]",
		ContentParts: parts,
	}, true
}
```

Turn-start drain site (currently `if reminder := interjectReminder(sess.drainInbox()); …` right before the round loop) becomes:

```go
	texts, userParts := sess.drainInbox()
	if reminder := interjectReminder(texts); reminder != "" {
		allMsgs = injectReminder(allMsgs, reminder)
		emitSystemReminder(sess, reminder)
	}
	// Parts queued while idle are injected as a user message right after the
	// reminder lands on the turn's user message (consecutive user messages are
	// fine on the wire; only user messages can carry media parts).
	if msg, ok := attachmentsMessage(nil, userParts); ok {
		allMsgs = append(allMsgs, msg)
		sess.append(msg)
	}
```

Post-tool-round drain site: the second `interjectReminder(sess.drainInbox())` block and the entire `if len(outcome.pendingMedia) > 0 { … }` block merge into:

```go
		texts, userParts := sess.drainInbox()
		if reminder := interjectReminder(texts); reminder != "" {
			allMsgs[len(allMsgs)-1].Content += "\n\n" + reminder
			emitSystemReminder(sess, reminder)
		}

		// read_media results are text-only (tool messages can't carry media), so
		// files it loaded — plus any attachments the user interjected during the
		// round — are appended here as a synthetic user message, the only role
		// the adapter renders image/audio parts for. This runs after the
		// reminder block so the reminder lands on the last tool message (text),
		// not on this parts-carrying user message.
		if msg, ok := attachmentsMessage(outcome.pendingMedia, userParts); ok {
			allMsgs = append(allMsgs, msg)
			sess.append(msg)
		}
```

CAREFUL:
- The old strings `"[read_media attached %d file(s)]"` and `"Above are the media file(s) you loaded with read_media."` change shape. `grep -rn "read_media attached\|Above are the media" internal pkg` first — as of writing, no test pins them (`turn_readmedia_test.go` asserts structure via `msgHasMediaPart`, not wording), so the rewrite is safe; if a test has since pinned them, update it.
- Final-round leftovers invariant is preserved automatically: items queued after the last drain stay in the inbox for the next turn (existing behavior, no code).
- `toolLoopOutcome`/`toolLoopState` are untouched — `read_media` still collects into `pendingMedia`.

- [ ] **Step 4:** `go test -race -count=1 ./internal/chat && make lint` → PASS (new tests + all existing inbox/read_media/turn tests).

- [ ] **Step 5: Commit**

```bash
git add internal/chat && git commit -m "feat(chat): RunParts multimodal turns; inbox carries media parts"
```

---

### Task 3: pkg/shell3 — Part surface, SendParts, variadic Interject (+ TUI signature)

**Files:**
- Modify: `pkg/shell3/shell3.go` (PartKind/Part, loadPart/loadParts, SendParts, Send delegates, Interject variadic), `internal/tui/interactive.go` (session interface signature), `internal/tui/interactive_test.go` (fakeSession signature)
- Test: `pkg/shell3/sendparts_test.go` (new)

- [ ] **Step 1: Failing tests** (`pkg/shell3/sendparts_test.go`; copy the in-memory `pngBytes` helper from Task 1 verbatim — pkg tests can't reach chat's test helpers):

```go
package shell3

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
)

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: 255, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestSendParts_ThreadsContentParts: byte-backed image and audio parts reach
// the provider as [text, image data URI, base64 audio] on the user message.
func TestSendParts_ThreadsContentParts(t *testing.T) {
	client := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "seen"}}})
	s := newTestSession(t, client, chat.Config{})
	defer s.Close()

	parts := []Part{
		{Kind: PartImage, Data: pngBytes(t, 2, 2), MIME: "image/png"},
		{Kind: PartAudio, Data: []byte("RIFF-fake"), MIME: "audio/mpeg"},
	}
	for ev := range s.SendParts(context.Background(), "describe these", parts) {
		if ev.Kind == Error {
			t.Fatalf("unexpected error event: %v", ev.Err)
		}
	}
	msgs := client.CallsSnapshot()[0].Msgs
	last := msgs[len(msgs)-1]
	if last.Role != llm.RoleUser || last.Content != "describe these" || len(last.ContentParts) != 3 {
		t.Fatalf("user message = %+v", last)
	}
	if last.ContentParts[0].Text != "describe these" ||
		!strings.HasPrefix(last.ContentParts[1].ImageURL, "data:image/jpeg;base64,") ||
		last.ContentParts[2].AudioFormat != "mp3" ||
		last.ContentParts[2].AudioData != base64.StdEncoding.EncodeToString([]byte("RIFF-fake")) {
		t.Fatalf("ContentParts = %+v", last.ContentParts)
	}
}

// TestSendParts_PathPart: a Path-backed part loads from disk relative to the
// session workdir (extension-routed; MIME ignored).
func TestSendParts_PathPart(t *testing.T) {
	client := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "ok"}}})
	workdir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workdir, "shot.png"), pngBytes(t, 2, 2), 0o644); err != nil {
		t.Fatal(err)
	}
	s := newTestSession(t, client, chat.Config{WorkDir: workdir})
	defer s.Close()

	for ev := range s.SendParts(context.Background(), "look", []Part{{Kind: PartImage, Path: "shot.png"}}) {
		if ev.Kind == Error {
			t.Fatalf("unexpected error event: %v", ev.Err)
		}
	}
	msgs := client.CallsSnapshot()[0].Msgs
	last := msgs[len(msgs)-1]
	if len(last.ContentParts) != 2 || !strings.HasPrefix(last.ContentParts[1].ImageURL, "data:image/jpeg;base64,") {
		t.Fatalf("path part not loaded: %+v", last.ContentParts)
	}
}

// TestSendParts_InvalidPartErrorsAndStaysUsable: every Part-contract violation
// yields exactly one Error event (no turn starts), and the session stays
// usable afterwards.
func TestSendParts_InvalidPartErrorsAndStaysUsable(t *testing.T) {
	client := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "ok"}}})
	s := newTestSession(t, client, chat.Config{})
	defer s.Close()

	cases := []Part{
		{Kind: PartImage},                                                       // neither Path nor Data
		{Kind: PartImage, Data: []byte("x")},                                    // Data without MIME
		{Kind: PartImage, Path: "a.png", Data: []byte("x"), MIME: "image/png"},  // both set
		{Kind: PartImage, Data: []byte("x"), MIME: "video/mp4"},                 // unsupported MIME
		{Kind: PartAudio, Data: pngBytes(t, 1, 1), MIME: "image/png"},           // kind/content mismatch
		{Kind: PartKind(99), Data: []byte("x"), MIME: "audio/wav"},              // unknown kind
	}
	for i, p := range cases {
		var evs []Event
		for ev := range s.SendParts(context.Background(), "x", []Part{p}) {
			evs = append(evs, ev)
		}
		if len(evs) != 1 || evs[0].Kind != Error || evs[0].Err == nil {
			t.Fatalf("case %d: want single Error event, got %+v", i, evs)
		}
	}
	if client.CallCount() != 0 {
		t.Fatalf("no turn should have started; provider saw %d calls", client.CallCount())
	}
	var done bool
	for ev := range s.Send(context.Background(), "hello") {
		if ev.Kind == Done {
			done = true
		}
	}
	if !done {
		t.Fatal("Send after rejected SendParts must complete normally")
	}
}

// TestSendParts_BusyRejected: SendParts honors the same ErrBusy contract as
// Send (mirrors TestSession_BusyEnforcement's blockingClient pattern).
func TestSendParts_BusyRejected(t *testing.T) {
	client := &blockingClient{entered: make(chan struct{}), returned: make(chan struct{})}
	s := newTestSession(t, client, chat.Config{})
	defer s.Close()

	ctx, cancel := context.WithCancel(context.Background())
	out := s.Send(ctx, "hi")
	<-client.entered

	var rejected []Event
	for ev := range s.SendParts(context.Background(), "overlap", []Part{{Kind: PartImage, Data: pngBytes(t, 1, 1), MIME: "image/png"}}) {
		rejected = append(rejected, ev)
	}
	if len(rejected) != 1 || rejected[0].Kind != Error || !errors.Is(rejected[0].Err, ErrBusy) {
		t.Fatalf("SendParts while busy: want one ErrBusy Error event, got %+v", rejected)
	}
	cancel()
	for range out {
	}
}

// TestInterject_PartsReachProvider: a valid part interjected while idle rides
// the next turn as the trailing media user message.
func TestInterject_PartsReachProvider(t *testing.T) {
	client := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "ok"}}})
	s := newTestSession(t, client, chat.Config{})
	defer s.Close()

	s.Interject("photo incoming", Part{Kind: PartImage, Data: pngBytes(t, 2, 2), MIME: "image/png"})
	for range s.Send(context.Background(), "hi") {
	}
	msgs := client.CallsSnapshot()[0].Msgs
	last := msgs[len(msgs)-1]
	found := false
	for _, p := range last.ContentParts {
		if strings.HasPrefix(p.ImageURL, "data:image/jpeg;base64,") {
			found = true
		}
	}
	if last.Role != llm.RoleUser || !found {
		t.Fatalf("interjected part missing from final user message: %+v", last)
	}
}

// TestInterject_InvalidPartDroppedWithNote: Interject never fails — a bad part
// is dropped and a bracketed note is appended to the queued steering text.
func TestInterject_InvalidPartDroppedWithNote(t *testing.T) {
	client := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "ok"}}})
	s := newTestSession(t, client, chat.Config{})
	defer s.Close()

	s.Interject("here you go", Part{Kind: PartImage, Data: []byte("not an image"), MIME: "image/png"})
	for range s.Send(context.Background(), "hi") {
	}
	var joined strings.Builder
	for _, m := range client.CallsSnapshot()[0].Msgs {
		joined.WriteString(m.Content + "\n")
	}
	if !strings.Contains(joined.String(), "here you go") || !strings.Contains(joined.String(), "[attachment dropped:") {
		t.Fatalf("dropped-attachment note missing from request: %q", joined.String())
	}
}
```

(Adjust imports — `os`, `path/filepath`, `errors` are used; `blockingClient` already lives in `shell3_test.go`, same package.)

- [ ] **Step 2: Verify failure** — `go test ./pkg/shell3 -run 'TestSendParts|TestInterject_Parts|TestInterject_Invalid' -v` → FAIL (`undefined: Part`, `SendParts`).

- [ ] **Step 3: Implement** (`pkg/shell3/shell3.go`).

Public types (near ApprovalRequest):

```go
// PartKind discriminates a Part's media type.
type PartKind int

const (
	PartImage PartKind = iota // jpg/png/gif/webp → resized JPEG data URI
	PartAudio                 // wav/mp3 → base64 input_audio
)

// String returns "image"/"audio" for error messages.
func (k PartKind) String() string {
	switch k {
	case PartImage:
		return "image"
	case PartAudio:
		return "audio"
	default:
		return fmt.Sprintf("PartKind(%d)", int(k))
	}
}

// Part is one inbound media attachment for SendParts and Interject. Set
// exactly one of Path or Data. With Data, MIME is required ("image/png",
// "audio/mpeg", …) and selects the handling; with Path, routing is by file
// extension and MIME is ignored. Relative paths resolve against the session
// workdir. Size caps match read_media: 10 MB images, 25 MB audio. Images are
// downscaled and re-encoded as JPEG; audio is passed through untranscoded
// (wav/mp3 only — the wire formats).
type Part struct {
	Kind PartKind
	Path string // file on disk (extension-routed)
	Data []byte // in-memory bytes (MIME-routed)
	MIME string // required with Data, e.g. "image/png", "audio/mpeg"
}
```

Loaders (private, near turnConfig):

```go
// loadPart converts one public Part into an internal ContentPart, enforcing
// the Part contract: exactly one of Path/Data, MIME with Data, and Kind
// matching the loaded media type. Size caps are enforced by the chat loaders.
func (s *Session) loadPart(p Part) (llm.ContentPart, error) {
	if p.Kind != PartImage && p.Kind != PartAudio {
		return llm.ContentPart{}, fmt.Errorf("shell3: unknown part kind %s", p.Kind)
	}
	var cp llm.ContentPart
	var err error
	switch {
	case p.Path != "" && len(p.Data) > 0:
		return llm.ContentPart{}, errors.New("shell3: part sets both Path and Data; set exactly one")
	case p.Path != "":
		cp, _, err = chat.LoadMediaPart(p.Path, s.cfg.WorkDir)
	case len(p.Data) > 0:
		if p.MIME == "" {
			return llm.ContentPart{}, errors.New("shell3: part with Data requires MIME")
		}
		cp, _, err = chat.MediaPartFromBytes(p.Data, p.MIME)
	default:
		return llm.ContentPart{}, errors.New("shell3: part sets neither Path nor Data")
	}
	if err != nil {
		return llm.ContentPart{}, err
	}
	want := llm.ContentPartTypeImageURL
	if p.Kind == PartAudio {
		want = llm.ContentPartTypeInputAudio
	}
	if cp.Type != want {
		return llm.ContentPart{}, fmt.Errorf("shell3: part declared %s but loaded %s media", p.Kind, cp.Type)
	}
	return cp, nil
}

// loadParts converts a Part slice, failing fast on the first invalid part
// (SendParts' all-or-nothing contract; Interject drops per-part instead).
func (s *Session) loadParts(parts []Part) ([]llm.ContentPart, error) {
	if len(parts) == 0 {
		return nil, nil
	}
	out := make([]llm.ContentPart, 0, len(parts))
	for i, p := range parts {
		cp, err := s.loadPart(p)
		if err != nil {
			return nil, fmt.Errorf("part %d: %w", i, err)
		}
		out = append(out, cp)
	}
	return out, nil
}
```

`Send` keeps its full contract doc comment (it remains the documented entry point) plus one new sentence: "SendParts is the media-carrying variant; Send is SendParts with no parts." Its body becomes a delegation, and the current body moves to SendParts with two changes — the parts load at the top and `RunParts` at the bottom:

```go
func (s *Session) Send(ctx context.Context, prompt string) <-chan Event {
	return s.SendParts(ctx, prompt, nil)
}

// SendParts runs one turn for prompt with media attachments. Same channel and
// ErrBusy contract as Send. Invalid parts (see Part) reject the whole call:
// the returned channel emits a single Error event carrying the first part's
// error and closes, without starting a turn — the session stays usable.
// Loading happens up front on the caller's goroutine (a Path part reads the
// file here, not on the turn goroutine).
func (s *Session) SendParts(ctx context.Context, prompt string, parts []Part) <-chan Event {
	cps, err := s.loadParts(parts)
	if err != nil {
		rejected := make(chan Event, 1)
		rejected <- Event{Kind: Error, Err: err}
		close(rejected)
		return rejected
	}
	out := make(chan Event)
	// ... existing Send body verbatim (busy gate, cur/turnCancel bookkeeping,
	// turnConfig, goroutine) with the final call changed to:
	//     s.sess.RunParts(turnCtx, tc, prompt, cps)
	return out
}
```

`Interject` becomes variadic (source-compatible with every existing `Interject(text)` call site); extend its doc: "Optional parts attach media: each invalid part is dropped — Interject never fails — and a bracketed `[attachment dropped: <error>]` note is appended to the queued text so the drop is visible to both the model and the audit reminder.":

```go
func (s *Session) Interject(text string, parts ...Part) {
	var cps []llm.ContentPart
	for _, p := range parts {
		cp, err := s.loadPart(p)
		if err != nil {
			text += "\n[attachment dropped: " + err.Error() + "]"
			continue
		}
		cps = append(cps, cp)
	}
	s.sess.Interject(text, cps...)
}
```

TUI signature follow-through (no behavior change — the TUI passes no parts):
- `internal/tui/interactive.go` `session` interface: `Interject(text string, parts ...shell3.Part)` (keep the doc comment). The `SetInterject` closure body (`sess.Interject(text)`) compiles unchanged.
- `internal/tui/interactive_test.go` `fakeSession`: `func (f *fakeSession) Interject(text string, _ ...shell3.Part) { f.interjections = append(f.interjections, text) }`.

- [ ] **Step 4:** `go test -race -count=1 ./pkg/shell3 ./internal/tui && make lint` → PASS (new tests; existing `TestSession_BusyEnforcement`, interject and approver wiring tests unchanged).

- [ ] **Step 5: Commit**

```bash
git add pkg/shell3 internal/tui && git commit -m "feat(pkg): SendParts and Interject media attachments (public Part surface)"
```

---

### Task 4: Close-out — CHANGELOG, full suite

**Files:**
- Modify: `CHANGELOG.md`

- [ ] **Step 1:** CHANGELOG under `## [Unreleased]` / `### Added`, first bullet:

```markdown
- Inbound media: `Session.SendParts` starts a turn with image/audio
  attachments, and `Interject` accepts the same parts for mid-turn delivery.
  `Part{Kind, Path, Data, MIME}` loads from disk or straight from in-memory
  bytes (Telegram photos and voice notes never touch disk), riding the same
  multimodal plumbing and size caps as `read_media` (10 MB images, 25 MB
  audio). Invalid SendParts attachments reject the turn with a single Error
  event; invalid Interject attachments are dropped with a bracketed note.
```

- [ ] **Step 2: Full verification**

```bash
make lint && go test -race -count=1 ./... && make build
```
→ all green. Also sanity-grep that no caller still references the renamed internals: `grep -rn "loadMediaPart" internal pkg` → no hits.

- [ ] **Step 3: Commit**

```bash
git add -A && git commit -m "docs: changelog for inbound media; phase 4 complete"
```

---

## Self-review notes

- **Spec coverage:** `SendParts(ctx, prompt, parts)` ✓; same parts on `Interject` ✓ (variadic, source-compatible); `Part{Kind, Path, Data, MIME}` ✓; maps onto the existing `llm.ContentPart` plumbing `read_media` uses ✓ (literally the same injection helper); bytes-in-hand never touch disk ✓ (`MediaPartFromBytes`).
- **Adapter-truth decision:** verified `toMessages` ignores `Content` when `ContentParts` is set. Consequences encoded: (a) the SendParts user message carries its prompt as the leading text part, (b) `injectReminder` mirrors reminders into the text part of multimodal user messages on a **cloned** parts slice (the backing array is shared with `sess.messages`, which must stay reminder-free — pinned by `TestInjectReminder_MultimodalUserMessage`).
- **Audit decision (resolves the controller's "verify and state" item):** the JSONL sink serializes `chat.Event`s, not messages, so ContentParts do NOT flow into the audit "automatically" — and we keep it that way. The audit shows the `user_message` line (prompt text) and, for interjected parts, the `system_reminder` line; media bytes are deliberately absent (no base64 blobs in logs, consistent with `read_media` today). No new event kinds.
- **read_media wording change:** `attachmentsMessage` unifies the injected message for both sources, changing the old `[read_media attached N file(s)]` strings. Verified no test pins them; the executor re-greps before relying on that.
- **Kind's purpose:** with MIME (Data) or extension (Path) already routing, `Kind` is a declared-intent cross-check — a mismatch (host says audio, bytes load as image) is rejected rather than silently shipped. Pinned in the validation test table.
- **Busy/ordering:** parts load before the busy gate (pure: reads only `cfg.WorkDir`, which no mutator changes), so a rejected SendParts can't leave the session busy; `ErrBusy` path is byte-identical to Send's.
- **Final-round leftovers invariant:** unchanged by construction — the drains still run only at the two existing sites; anything queued later stays for the next turn.
- **No placeholders:** all snippets are written against the current code on `agent-runtime` (field names, helper names, test helpers `newCollectorSession`/`funcHandler`/`msgHasMediaPart`/`blockingClient` all exist); the one "verbatim body move" (Send → SendParts) is mechanical and bounded.
