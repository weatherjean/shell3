# read_media Tool Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to execute this plan task-by-task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Generalize the `read_image` tool into a modality-general `read_media` tool (image + audio) and remove `/image` entirely.

**Architecture:** See `docs/superpowers/specs/2026-06-08-read-media-design.md`. Media rides in synthetic **user** messages (tool messages are text-only); `read_media` returns text + queues a `ContentPart` injected after the round's tool results. Extension-routed loaders produce `image_url` or `input_audio` parts.

**Tech Stack:** Go 1.25, `openai-go` v1.12.0, `gopher-lua`, `fakellm`.

**Branch:** `feat/read-image-tool` (continue on it). Every task must leave `go build ./...` and `go test ./...` GREEN (atomic commits).

---

## Contract (shared)

- New: `llm.ContentPartTypeInputAudio = "input_audio"`; `ContentPart` gains `AudioData string`, `AudioFormat string`.
- `chat.loadAudioPart(path, workDir string) (llm.ContentPart, string, error)` — audio part + human desc.
- `chat.loadMediaPart(path, workDir string) (llm.ContentPart, string, error)` — routes by ext.
- `chat.handleReadMedia(rawArgs, workDir string) (string, llm.ContentPart)` — tool handler core.
- Queue guard: a part is present iff `part.Type != ""`.
- `toolLoopOutcome.pendingMedia []llm.ContentPart`. Tool name `read_media`. Gate `ToolGates.Media` / Lua key `media`. Schema var `readMediaTool`.
- `supportedAudioExts = {".wav", ".mp3"}`; `maxAudioBytes = 25 << 20`.

---

## Task 1: Audio in the type system + adapter

**Files:** `internal/llm/types.go`, `internal/adapter/openai/client.go`, `internal/adapter/openai/internals_test.go` (or a new `_test.go`).

- [ ] Add `ContentPartTypeInputAudio` const and `AudioData`/`AudioFormat` fields to `ContentPart`.
- [ ] In `toMessages`' `case llm.RoleUser` parts loop, add a `case llm.ContentPartTypeInputAudio` building `openai.InputAudioContentPart(openai.ChatCompletionContentPartInputAudioInputAudioParam{Data: p.AudioData, Format: p.AudioFormat})`.
- [ ] Test (adapter): a user `llm.Message` with an `input_audio` ContentPart serializes (via the existing traffic-tap / a direct `toMessages` call if accessible) to a content part carrying the base64 data and format. Follow the existing adapter test style.
- [ ] `go test ./internal/llm/ ./internal/adapter/openai/`; commit `feat(llm): add input_audio content part + adapter case`.

## Task 2: Rename gate + tool to media (luacfg + scaffold)

**Files:** `internal/luacfg/luacfg.go`, `register.go`, `tooldefs.go`, `tool_test.go`, `internal/scaffold/defaults/shell3.lua`.

- [ ] `ToolGates.Image` → `Media`; `toolGateKeys` `"image"`→`"media"`; gate literal `Image: optBool(tt,"image")` → `Media: optBool(tt,"media")`.
- [ ] `readImageTool` → `readMediaTool`: Name `"read_media"`, description: `"Load a media file (image: jpg/png/gif; audio: wav/mp3) from disk so a vision/audio-capable model can perceive it. It is decoded and attached as a user message immediately after the tool results, appearing in your view on the next step. For text files use bash with cat/sed/head."`, schema `{path}` required. Gate on `g.Media`.
- [ ] `tool_test.go`: rename test to `TestToolDefs_MediaGate`, assert `read_media` present with `Media:true`, absent when off, schema has `path`.
- [ ] Scaffold: `image = true   -- ...` → `media = true   -- read_media: load image/audio so a capable model can perceive it` on base AND plan agents.
- [ ] `go test ./internal/luacfg/ ./internal/scaffold/`; commit `feat(luacfg): rename image gate/tool to media`.

## Task 3: Audio loader

**Files:** `internal/chat/audio.go` (new), `internal/chat/audio_test.go` (new).

- [ ] `supportedAudioExts`, `maxAudioBytes`, `loadAudioPart(path, workDir)`: resolve path against workDir (when relative), lowercased ext check against `supportedAudioExts` (else `"unsupported ... "`), `os.Stat` size cap (`maxAudioBytes` → "audio too large..."), `os.ReadFile`, `base64.StdEncoding.EncodeToString`, format = strings.TrimPrefix(ext,"."), return `llm.ContentPart{Type: llm.ContentPartTypeInputAudio, AudioData: enc, AudioFormat: format}`, desc `fmt.Sprintf("%s audio, %.1f MB", format, float64(size)/(1<<20))`.
- [ ] Tests: success (`.wav` and `.mp3` → correct Type/Format/non-empty AudioData), unsupported ext, missing file. Use tiny byte fixtures written to `t.TempDir()` (audio content need not be valid — loader does not decode).
- [ ] `go test ./internal/chat/`; commit `feat(chat): add loadAudioPart for input_audio media`.

## Task 4: Media dispatcher + handler + turn-loop switch

**Files:** `internal/chat/media.go` (new), `internal/chat/media_test.go` (new), `internal/chat/turn.go`, `internal/chat/image.go` (remove `handleReadImage`), `internal/chat/image_test.go` (remove `TestHandleReadImage_*`, `TestLoadImagePart_ReturnsDimensions` stays), rename `internal/chat/turn_readimage_test.go` → `turn_readmedia_test.go`.

- [ ] `media.go`: `loadMediaPart(path, workDir)` routes lowercased ext → `loadImagePart` (wrap dims into desc `fmt.Sprintf("image %dx%d", w, h)`) or `loadAudioPart`; else unsupported-media error. `handleReadMedia(rawArgs, workDir)`: parse `{"path"}`, bad-JSON/empty-path/load-error → `("error: ...", llm.ContentPart{})`; success → `("Loaded <desc> from %q. It is attached as a user message right after the tool results so you can view/hear it on the next step.", part)`.
- [ ] Remove `handleReadImage` from `image.go` and its tests from `image_test.go` (migrate equivalent coverage to `media_test.go` as `TestHandleReadMedia_*`). Keep `loadImagePart`, `resizeAndEncodeJPEG`, `resizeNearest`, `supportedImageExts`, `BuildImageMessage` (Task 5 removes BuildImageMessage).
- [ ] `turn.go`: `pendingImages`→`pendingMedia` (field + locals + return); branch `read_image`→`read_media` calling `handleReadMedia`; queue guard `if part.Type != ""`; injection trailing text `"Above are the media file(s) you loaded with read_media."`; history Content `"[read_media attached %d file(s)]"`; update the comment.
- [ ] Rename `turn_readimage_test.go`→`turn_readmedia_test.go`: `read_image`→`read_media`, helper/test names `…Media…`, result substring `"Loaded "`; ADD `TestRunTurn_ReadMedia_InjectsAudioUserMessage` (script a `read_media` call on a `.mp3` fixture; assert an `input_audio`-bearing user message is injected after the tool result and present in round 2's prompt). `msgHasImagePart`→generalize to `msgHasMediaPart` (image_url OR input_audio).
- [ ] `go test ./internal/chat/`; commit `feat(chat): read_media tool dispatch (image + audio)`.

## Task 5: Remove /image entirely

**Files:** `internal/chat/image.go` (remove `BuildImageMessage`), `internal/chat/image_test.go` (remove `TestBuildImageMessage_*`), `pkg/shell3/shell3.go`, `pkg/shell3/shell3_test.go`, `internal/tui/interactive.go`, `internal/tui/interactive_test.go`.

- [ ] Remove `chat.BuildImageMessage` + its tests.
- [ ] `pkg/shell3`: delete `ImageMessage`, `Attachment`, `Message`, `SendMessage`; rewrite `Send(ctx, prompt string)` to own the route/turn goroutine machinery directly via `s.sess.Run(turnCtx, tc, prompt)` (move the body of the old `SendMessage`, dropping the `built.ContentParts` branch). Remove now-unused imports. Delete `TestImageMessage_*`; convert `TestSendMessage_TextPath` → `TestSend_TextPath` driving `s.Send(...)`.
- [ ] `internal/tui/interactive.go`: delete the `/image` `RegisterSlash` block; change `launchTurn(msg shell3.Message)` to `launchTurn(prompt string)` (always `sess.Send`); remove the attachment branch; remove `SendMessage` from the local `session` interface; fix the `/image`-referencing comments.
- [ ] `internal/tui/interactive_test.go`: remove `SendMessage` from `fakeSession`; drop `"image"` from the expected command list in the slash-command test.
- [ ] `go build ./... && go test ./...`; commit `refactor: remove /image; read_media is the only media path`.

## Task 6: Full verification

- [ ] `make build`, `go vet ./...`, `go test ./...` — all green.
- [ ] `grep -rn "BuildImageMessage\|ImageMessage\|SendMessage\|read_image\|pendingImages\|\.Image\b" --include='*.go'` returns nothing (no dangling references).

---

## Self-Review
- Spec coverage: audio types+adapter (T1), config rename (T2), audio loader (T3), dispatcher+handler+turn (T4), /image removal (T5), verify (T6). ✔
- Atomic green: T1 additive; T2 self-contained rename; T3 additive new files; T4 swaps handler+dispatch together (no dangling `handleReadImage`); T5 removes BuildImageMessage with its only caller ImageMessage together. ✔
- Naming consistency: `pendingMedia`, `handleReadMedia`, `loadMediaPart`, `loadAudioPart`, `ToolGates.Media`, `read_media`, `readMediaTool`, `ContentPartTypeInputAudio`. ✔
