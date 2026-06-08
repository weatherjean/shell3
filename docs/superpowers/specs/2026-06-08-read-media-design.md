# read_media Tool Design

**Date:** 2026-06-08
**Status:** Approved (supersedes the `read_image` tool added earlier on branch `feat/read-image-tool`)

## Problem

We added a model-callable `read_image` tool plus the older human-driven `/image` slash command. But "image" is an arbitrary stopping point — multimodal models also accept audio — and having both a slash command and a tool is two ways to do one thing. We want a single, modality-general `read_media` tool, and to remove `/image` entirely.

## Decisions (from brainstorming)

1. **Scope:** image + audio now. Architecture left extensible to PDF/file later. (Video is not supported by the OpenAI chat-completions wire and is out of scope.)
2. **Remove `/image` completely:** the TUI slash command, `chat.BuildImageMessage`, and the `pkg/shell3` `ImageMessage`/`Attachment`/`Message`/`SendMessage` machinery. The model-initiated `read_media` tool is the only way media reaches the model. To show the model media, the human says "look at foo.png" and the model calls `read_media`.
3. **No capability detection:** zero new model config. `read_media` loads any supported media type when gated; if the active model lacks the modality, the provider's error surfaces and the model adapts.

## Wire reality (OpenAI-compatible chat completions)

All media rides as **user-message content parts** — never in tool-role messages, which are text-only (`openai-go` `ToolMessage[T string | []…TextParam]`). So `read_media` returns a text acknowledgement as its tool result, and the loaded media is injected as a synthetic **user** message after the round's tool results — the seam already built for `read_image`.

- **image** → `image_url` content part, data URI (vision models). Already working.
- **audio** → `input_audio` content part: `{data: base64, format: "wav"|"mp3"}` (audio models). New.
- file/PDF (`file` content part) — deferred; the architecture (extension-routed loader) accommodates it later.

## Architecture

### Data model (`internal/llm/types.go`)
Extend the existing flat `ContentPart`, mirroring how `ImageURL` already sits on it:
```go
const (
    ContentPartTypeText       ContentPartType = "text"
    ContentPartTypeImageURL   ContentPartType = "image_url"
    ContentPartTypeInputAudio ContentPartType = "input_audio"
)

type ContentPart struct {
    Type        ContentPartType
    Text        string
    ImageURL    string // image_url (data URI or HTTPS URL)
    AudioData   string // base64-encoded raw audio bytes (input_audio)
    AudioFormat string // "wav" | "mp3"
}
```

### Adapter (`internal/adapter/openai/client.go` `toMessages`)
In the `case llm.RoleUser` parts loop, add:
```go
case llm.ContentPartTypeInputAudio:
    parts = append(parts, openai.InputAudioContentPart(openai.ChatCompletionContentPartInputAudioInputAudioParam{
        Data:   p.AudioData,
        Format: p.AudioFormat,
    }))
```
Tool-role messages remain text-only (unchanged).

### Media pipeline (`internal/chat/`)
- `image.go` — keeps image-specific code: `supportedImageExts`, `loadImagePart`, `resizeAndEncodeJPEG`, `resizeNearest`. (`BuildImageMessage` and `handleReadImage` are removed — see below.)
- `audio.go` (new) — `supportedAudioExts = {.wav, .mp3}`; `loadAudioPart(path, workDir) (llm.ContentPart, string, error)`: resolve path, ext-check, size-cap, read raw bytes, base64-encode (no transcode), set `AudioData`/`AudioFormat` (format from extension), return the part plus a human description like `"mp3 audio, 2.1 MB"`.
- `media.go` (new) — the dispatcher + tool handler:
  - `loadMediaPart(path, workDir) (llm.ContentPart, string, error)` routes by lowercased extension to `loadImagePart` (returning a desc like `"image 1024x768"`) or `loadAudioPart`; unknown extension → `"unsupported media type %q — use jpg, png, gif, wav, or mp3"`.
  - `handleReadMedia(rawArgs, workDir) (string, llm.ContentPart)` — parses `{"path":...}`; on bad JSON / empty path / load error returns `("error: ...", zero ContentPart)`; on success returns `("Loaded <desc> from %q. It is attached as a user message right after the tool results so you can view/hear it on the next step.", part)`.

A `ContentPart` is "non-empty" (queue it) when `Type != ""` — the queue guard becomes `if part.Type != "" { pendingMedia = append(...) }`.

### Turn loop (`internal/chat/turn.go`)
- `toolLoopOutcome.pendingImages` → `pendingMedia []llm.ContentPart`.
- The inline dispatch branch becomes `else if tc.Name == "read_media" { out, part = handleReadMedia(tc.RawArgs, cfg.WorkDir); if part.Type != "" { pendingMedia = append(pendingMedia, part) } }`.
- Injection (after the reminder block) uses `pendingMedia`; the trailing text part reads `"Above are the media file(s) you loaded with read_media."`; the history-trace `Content` reads `"[read_media attached N file(s)]"`. Ordering, no-inject-on-cancel, and one-message-per-round invariants are unchanged.

### Config (`internal/luacfg/`)
- `ToolGates.Image` → `ToolGates.Media`; Lua key `image` → `media`.
- `readImageTool` → `readMediaTool`: Name `"read_media"`, description covering image+audio and pointing text reads to `bash`, schema `{path}` required.
- `ToolDefs` gates it on `g.Media`.
- Scaffold `internal/scaffold/defaults/shell3.lua`: `image = true` → `media = true` on the base and plan agents.

### Removal of `/image` (`internal/tui/`, `pkg/shell3/`, `internal/chat/`)
- `chat.BuildImageMessage` + its `TestBuildImageMessage_*` tests — deleted.
- `pkg/shell3`: delete `ImageMessage`, `Attachment`, `Message`, and `SendMessage`; fold the turn/route machinery into `Send(ctx, prompt string)` (it already only takes the text path once attachments are gone). Delete `TestImageMessage_*` and convert `TestSendMessage_TextPath` to drive `Send`.
- `internal/tui/interactive.go`: remove the `/image` slash command; `launchTurn(msg shell3.Message)` → `launchTurn(prompt string)` calling `sess.Send`; drop `SendMessage` from the local `session` interface.
- `internal/tui/interactive_test.go`: drop `SendMessage` from `fakeSession`; remove `"image"` from the expected slash-command list.

## Testing

- **Adapter:** a user message with an `input_audio` part serializes to an `input_audio` content part with the right `data`/`format`.
- **audio.go:** `loadAudioPart` returns a part with `Type=input_audio`, correct `AudioFormat` from extension, base64 `AudioData`; rejects unsupported ext and missing file.
- **media.go:** `loadMediaPart` routes `.png`→image part, `.mp3`→audio part, `.bmp`→unsupported error; `handleReadMedia` success + error paths.
- **turn.go:** existing read_media injection tests (renamed from read_image) plus an audio variant asserting an `input_audio`-bearing user message is injected after the tool result and re-sent in round 2.
- **luacfg:** `read_media` present when `Media` gate on, absent when off; schema has `path`.
- **Removal:** packages compile with no dangling references; `/image` absent from the command list; `Send` text path still works.
- **Full suite:** `go build ./...`, `go vet ./...`, `go test ./...` all green.

## Out of scope / deferred
- PDF/`file` content parts (architecture accommodates; not built).
- Audio transcoding (only wav/mp3 accepted as-is).
- Per-model modality capability config.
- The pre-existing `.webp` ext entry (no Go stdlib decoder) — left as-is; the image loader rejects it at decode if ever hit.
