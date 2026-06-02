# Design: image upload for `shell3 web`

**Date:** 2026-06-02
**Branch:** `feat/web-mode`
**Status:** Approved

## Goal

Let a `shell3 web` user attach **one** image to a message: an attach button picks
a file; on Send the browser uploads the image plus the textarea text as the
prompt; the server resizes/encodes it (reusing `pkg/chat`) and runs a multimodal
turn — the web equivalent of the TUI's `/image`.

## Existing plumbing (verified contract)

- `pkg/llm/types.go`: `Message.ContentParts []ContentPart`; `ContentPart{Type
  ContentPartType, Text string, ImageURL string}`; `ContentPartTypeText` /
  `ContentPartTypeImageURL`. Image parts carry a `data:image/jpeg;base64,…` URI.
- `pkg/chat/image.go`: `BuildImageMessage(args, workDir)` (path-based, TUI) and
  `resizeAndEncodeJPEG(raw []byte, maxSide, quality int) (string, error)`.
  Constants: `maxImageBytes = 10<<20`, `maxImageSide = 1000`, `jpegQuality = 85`.
  Builds `ContentParts{ {image_url, dataURI}, {text, prompt} }`, default prompt
  "Describe this image.".
- Turn path: a message with non-empty `ContentParts` bypasses `Session.Run`
  (string-only) and is run via `chat.RunTurn(ctx, cfg, sess, msg)`. `RunTurn`
  does **not** emit a `user_message` event.
- OpenAI adapter passes `ImageURL` straight through as an image content part.

## Decisions

- **Attach-button only** (no paste/drag-drop in MVP).
- **Single image per turn.**
- **Server-side** resize/encode, reusing `pkg/chat` (one source of truth, matches
  the TUI, minimal JS; fine on localhost).

## Changes

### 1. `pkg/chat/image.go` — bytes-based builder (DRY)

Add:

```go
// BuildImageMessageFromBytes builds a multimodal user message from raw image
// bytes (already read from an upload or file). prompt defaults to a describe
// prompt when empty. Enforces the 10 MB cap and re-encodes to resized JPEG.
func BuildImageMessageFromBytes(raw []byte, prompt string) (llm.Message, error)
```

It enforces `len(raw) <= maxImageBytes`, calls `resizeAndEncodeJPEG(raw,
maxImageSide, jpegQuality)`, defaults the prompt, and returns the `ContentParts`
message. Refactor the existing `BuildImageMessage` (path version) to read the
file (with its extension + size checks) and then delegate to this, so both the
TUI and web share one builder.

### 2. `internal/web/hub.go` — generalize run to multimodal

- Change the run callback type from `func(ctx, string)` to
  `func(ctx context.Context, msg llm.Message)`.
- Keep `Submit(text string) error` (wraps text into `llm.Message{Role: RoleUser,
  Content: text}`); add `SubmitMessage(msg llm.Message) error`. Both funnel
  through one private `submit(msg)` that holds the existing busy-gate + turn
  goroutine + `wg`.
- `internal/web` gains a `pkg/llm` import (still no `internal/*` imports).

### 3. `cmd/shell3/web.go` — route message kinds

The run closure becomes `func(turnCtx, msg llm.Message)`: snapshot `tc` under the
model mutex, then `if len(msg.ContentParts) == 0 { sess.Run(turnCtx, tc,
msg.Content) } else { chat.RunTurn(turnCtx, tc, sess, msg) }` — mirrors the TUI.

### 4. `internal/web/server.go` — `POST /image`

- `r.Body = http.MaxBytesReader(w, r.Body, 12<<20)`; `ParseMultipartForm`.
- Read `image` file part → bytes; `prompt` form value.
- `409` if `hub.Busy()`. Build via `chat.BuildImageMessageFromBytes(raw, prompt)`;
  on error → `400` (oversize/undecodable). Else `hub.SubmitMessage(msg)` → `202`.
- Register `mux.HandleFunc("POST /image", s.handleImage)`.

### 5. `internal/web/assets/index.html` — UI

- A 📎 attach button in the button row; hidden `<input type="file"
  accept="image/*">`. On pick, store the `File` and show a **preview chip**
  (thumbnail via `URL.createObjectURL` + filename + ✕ to remove) above the input.
- `send()`: if a pending image exists, build `FormData` (`image` + `prompt` =
  textarea text), `POST /image`; locally render a `user` block containing the
  thumbnail + prompt (the server emits no `user_message` for image turns); clear
  the chip. No image → existing `/input` path.
- Attach button disabled while busy; `409` shows the busy notice.

### 6. Tests

- `pkg/chat`: `BuildImageMessageFromBytes` with a generated 1×1 PNG → message
  with 2 parts, image part prefixed `data:image/jpeg;base64,`; oversize bytes →
  error; undecodable bytes → error.
- `internal/web`: `SubmitMessage` runs a turn (fakellm, asserts a `turn_done`);
  `POST /image` with a small multipart PNG → `202`; while busy → `409`. Update
  `newTestHub`/`newTestServer` run closures to the `llm.Message` signature.
- `go build ./... && go test ./... -race` green; `internal/web` still imports no
  `internal/*`.

## Non-goals / known MVP limitations

- Multiple images, paste, drag-drop, browser-side resize.
- **Reconnect replay does not include the image** — the sending client renders its
  own thumbnail; `RunTurn` emits no `user_message`, and synthesizing one would
  reintroduce the event-ordering hazard fixed earlier. Documented, not solved.
- No model vision-capability check (same as the TUI; the API errors if unsupported).
