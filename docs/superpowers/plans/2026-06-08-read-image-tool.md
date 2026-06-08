# read_image Tool Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a model-callable `read_image` tool so vision-capable models can load an image file from disk mid-turn and see it.

**Architecture:** The OpenAI Chat Completions API (and the `openai-go` SDK: `ToolMessage[T string | []ChatCompletionContentPartTextParam]`) makes tool-role messages **text-only** — images cannot ride in a tool result. So `read_image` returns a short text acknowledgement as its tool result, and the loaded image is injected as a synthetic **user** message appended immediately after all tool results in the round (the only role for which `toMessages` emits `image_url` parts). The image pipeline (decode → downscale ≤1000px → JPEG q85 → base64 data URI) is the existing one from `internal/chat/image.go`, refactored into a shared `loadImagePart` helper that both the existing `/image` slash command and the new tool use. Dispatch is handled inline in `executeToolCalls`, mirroring how `compact_history` and `shell_interactive` are already special-cased (it is **not** a `NewHandlers` entry).

**Tech Stack:** Go 1.25, `github.com/openai/openai-go` v1.12.0, `gopher-lua` config, `fakellm` test harness.

---

## Contract (shared across all tasks — do not change without updating every task)

- **Tool name:** `read_image`
- **Tool args schema:** `{ "type": "object", "properties": { "path": {"type":"string"} }, "required": ["path"] }`
- **Helper signatures (package `chat`):**
  - `func loadImagePart(path, workDir string) (part llm.ContentPart, w, h int, err error)` — resolves `path` against `workDir`, validates type/size, decodes, resizes, JPEG-encodes; returns an `image_url` ContentPart plus the **source** pixel dimensions.
  - `func handleReadImage(rawArgs, workDir string) (resultText string, part llm.ContentPart)` — parses `{"path":...}`; on success returns acknowledgement text + a populated part; on error returns an `"error: ..."` string + the zero `llm.ContentPart{}`.
  - `func resizeAndEncodeJPEG(raw []byte, maxSide, quality int) (encoded string, w, h int, err error)` — now returns source dimensions alongside the base64 string.
- **`toolLoopOutcome`** gains field `pendingImages []llm.ContentPart`.
- **Gate:** `luacfg.ToolGates.Image bool`; Lua key `image`; schema var `readImageTool`.
- **Injected user message** carries `Content: "[read_image attached N image(s)]"` (history trace; ignored by the adapter when ContentParts are present) and `ContentParts: [<image parts...>, {text: "Above are the image(s) you loaded with read_image."}]`.

---

## Task 1: Refactor image pipeline + add `handleReadImage`

**Files:**
- Modify: `internal/chat/image.go`
- Test: `internal/chat/image_test.go`

- [ ] **Step 1: Update existing `resizeAndEncodeJPEG` tests are unaffected, then write failing tests for the new helpers**

Append to `internal/chat/image_test.go` (the `writePNG`/`makePNG` helpers already exist there):

```go
func TestHandleReadImage_Success(t *testing.T) {
	tmp := t.TempDir()
	imgPath := filepath.Join(tmp, "shot.png")
	writePNG(t, imgPath)

	out, part := handleReadImage(`{"path":"`+imgPath+`"}`, "")
	if part.Type != llm.ContentPartTypeImageURL {
		t.Fatalf("part type = %q, want image_url", part.Type)
	}
	if !strings.HasPrefix(part.ImageURL, "data:image/jpeg;base64,") {
		t.Errorf("image URL prefix wrong: %.30s", part.ImageURL)
	}
	if !strings.Contains(out, "Loaded image") {
		t.Errorf("result text = %q, want it to mention loading", out)
	}
}

func TestHandleReadImage_BadJSON(t *testing.T) {
	out, part := handleReadImage(`{not json`, "")
	if part.ImageURL != "" {
		t.Error("expected zero part on bad json")
	}
	if !strings.HasPrefix(out, "error:") {
		t.Errorf("want error string, got %q", out)
	}
}

func TestHandleReadImage_MissingPath(t *testing.T) {
	out, part := handleReadImage(`{"path":"  "}`, "")
	if part.ImageURL != "" || !strings.HasPrefix(out, "error:") {
		t.Errorf("want error + zero part, got out=%q part=%+v", out, part)
	}
}

func TestHandleReadImage_Unsupported(t *testing.T) {
	out, part := handleReadImage(`{"path":"/tmp/x.bmp"}`, "")
	if part.ImageURL != "" || !strings.Contains(out, "unsupported") {
		t.Errorf("want unsupported error + zero part, got out=%q", out)
	}
}

func TestLoadImagePart_ReturnsDimensions(t *testing.T) {
	tmp := t.TempDir()
	imgPath := filepath.Join(tmp, "shot.png")
	writePNG(t, imgPath) // makePNG is 4x4
	part, w, h, err := loadImagePart(imgPath, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w != 4 || h != 4 {
		t.Errorf("dims = %dx%d, want 4x4", w, h)
	}
	if part.Type != llm.ContentPartTypeImageURL {
		t.Errorf("part type = %q", part.Type)
	}
}
```

Add `"encoding/json"` is NOT needed in the test (handleReadImage takes a raw string). Ensure the test file imports already include `llm`, `strings`, `filepath`, `os`, `testing` (they do).

- [ ] **Step 2: Run the new tests — verify they fail to compile (helpers undefined)**

Run: `go test ./internal/chat/ -run 'HandleReadImage|LoadImagePart' 2>&1 | head -20`
Expected: build failure — `undefined: handleReadImage`, `undefined: loadImagePart`.

- [ ] **Step 3: Refactor `image.go` — change `resizeAndEncodeJPEG` to return dimensions, extract `loadImagePart`, add `handleReadImage`, and route `BuildImageMessage` through `loadImagePart`**

Replace the body of `BuildImageMessage` (keep its arg-parsing prologue) so the validation/load is delegated. The final form of the file's middle section:

```go
// BuildImageMessage parses "/image args" into a multimodal llm.Message.
// Quoted paths handle filenames with spaces: /image "Screenshot 2026.png" prompt
func BuildImageMessage(args, workDir string) (llm.Message, error) {
	args = strings.TrimSpace(args)
	if args == "" {
		return llm.Message{}, fmt.Errorf(`usage: /image "<path>" [prompt]`)
	}

	var path, prompt string
	if strings.HasPrefix(args, `"`) {
		var ok bool
		path, prompt, ok = strings.Cut(args[1:], `"`)
		if !ok {
			return llm.Message{}, fmt.Errorf("unterminated quote in path")
		}
		path = strings.ReplaceAll(path, `\ `, " ") // unescape shell-escaped spaces inside quotes
		prompt = strings.TrimSpace(prompt)
	} else {
		path, prompt, _ = strings.Cut(args, " ")
		prompt = strings.TrimSpace(prompt)
	}

	if prompt == "" {
		prompt = "Describe this image."
	}

	part, _, _, err := loadImagePart(path, workDir)
	if err != nil {
		return llm.Message{}, err
	}

	return llm.Message{
		Role: llm.RoleUser,
		ContentParts: []llm.ContentPart{
			part,
			{Type: llm.ContentPartTypeText, Text: prompt},
		},
	}, nil
}

// loadImagePart resolves path against workDir, validates type and size, decodes,
// downscales so the longest side is ≤ maxImageSide, JPEG-encodes, and returns an
// image_url ContentPart whose URL is a base64 data URI, plus the source image's
// pixel dimensions.
func loadImagePart(path, workDir string) (llm.ContentPart, int, int, error) {
	if !filepath.IsAbs(path) && workDir != "" {
		path = filepath.Join(workDir, path)
	}

	ext := strings.ToLower(filepath.Ext(path))
	if !supportedImageExts[ext] {
		return llm.ContentPart{}, 0, 0, fmt.Errorf("unsupported file type %q — use jpg, png, gif, or webp", ext)
	}

	info, err := os.Stat(path)
	if err != nil {
		return llm.ContentPart{}, 0, 0, fmt.Errorf(`cannot read "%s": %w`, path, err)
	}
	if info.Size() > maxImageBytes {
		return llm.ContentPart{}, 0, 0, fmt.Errorf("image too large (%d MB, max 10 MB)", info.Size()>>20)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return llm.ContentPart{}, 0, 0, fmt.Errorf(`cannot read "%s": %w`, path, err)
	}

	encoded, w, h, err := resizeAndEncodeJPEG(raw, maxImageSide, jpegQuality)
	if err != nil {
		return llm.ContentPart{}, 0, 0, fmt.Errorf("image encode: %w", err)
	}

	return llm.ContentPart{
		Type:     llm.ContentPartTypeImageURL,
		ImageURL: "data:image/jpeg;base64," + encoded,
	}, w, h, nil
}

// handleReadImage parses {"path": "..."} tool args, loads the image via
// loadImagePart, and returns the tool-result text plus the image ContentPart.
// On any failure it returns an "error: ..." string and the zero ContentPart so
// the caller queues no image.
func handleReadImage(rawArgs, workDir string) (string, llm.ContentPart) {
	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		return fmt.Sprintf("error: bad arguments: %v", err), llm.ContentPart{}
	}
	if strings.TrimSpace(args.Path) == "" {
		return "error: path is required", llm.ContentPart{}
	}
	part, w, h, err := loadImagePart(args.Path, workDir)
	if err != nil {
		return "error: " + err.Error(), llm.ContentPart{}
	}
	return fmt.Sprintf("Loaded image %q (%dx%d). The image is attached as a user message right after the tool results so you can view it on the next step.", args.Path, w, h), part
}
```

Change the `resizeAndEncodeJPEG` signature and body to return source dimensions:

```go
// resizeAndEncodeJPEG decodes raw image bytes, shrinks so longest side ≤
// maxSide (no-op if already within bounds), JPEG-encodes at the given quality,
// and returns the base64 result plus the source image's pixel width and height.
func resizeAndEncodeJPEG(raw []byte, maxSide, quality int) (string, int, int, error) {
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return "", 0, 0, fmt.Errorf("decode: %w", err)
	}

	b := img.Bounds()
	srcW, srcH := b.Dx(), b.Dy()
	w, h := srcW, srcH
	if w > maxSide || h > maxSide {
		if w >= h {
			h = h * maxSide / w
			w = maxSide
		} else {
			w = w * maxSide / h
			h = maxSide
		}
		img = resizeNearest(img, w, h)
	}

	// Pre-allocate: JPEG at q85 is typically 0.1–0.5 bits/pixel.
	buf := bytes.NewBuffer(make([]byte, 0, len(raw)/4))
	if err := jpeg.Encode(buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return "", 0, 0, fmt.Errorf("jpeg encode: %w", err)
	}

	return base64.StdEncoding.EncodeToString(buf.Bytes()), srcW, srcH, nil
}
```

Add `"encoding/json"` to the `image.go` import block (it currently is not imported).

- [ ] **Step 4: Run the chat package tests — verify all pass**

Run: `go test ./internal/chat/ -run 'Image|ReadImage' -v 2>&1 | tail -30`
Expected: PASS for both the pre-existing `TestBuildImageMessage_*` tests and the new `TestHandleReadImage_*` / `TestLoadImagePart_*`.

- [ ] **Step 5: Commit**

```bash
git add internal/chat/image.go internal/chat/image_test.go
git commit -m "refactor(chat): extract loadImagePart, add handleReadImage core"
```

---

## Task 2: Dispatch `read_image` + inject the image user message in the turn loop

**Files:**
- Modify: `internal/chat/turn.go` (`toolLoopOutcome` struct ~line 209; `executeToolCalls` ~line 229; the inline tool-name chain ~line 267; `RunTurn` end-of-loop ~line 202)
- Test: `internal/chat/turn_readimage_test.go` (new)

- [ ] **Step 1: Write the failing turn test**

Create `internal/chat/turn_readimage_test.go`:

```go
package chat

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
	"github.com/weatherjean/shell3/internal/persona"
)

func msgHasImagePart(m llm.Message) bool {
	for _, p := range m.ContentParts {
		if p.Type == llm.ContentPartTypeImageURL &&
			strings.HasPrefix(p.ImageURL, "data:image/jpeg;base64,") {
			return true
		}
	}
	return false
}

func anyMsgHasImagePart(msgs []llm.Message) bool {
	for _, m := range msgs {
		if msgHasImagePart(m) {
			return true
		}
	}
	return false
}

// TestRunTurn_ReadImage_InjectsImageUserMessage drives a turn where round 1 calls
// read_image and round 2 returns text. The image must be injected as a user
// message AFTER the tool result, and round 2's prompt must carry the image part.
func TestRunTurn_ReadImage_InjectsImageUserMessage(t *testing.T) {
	tmp := t.TempDir()
	imgPath := filepath.Join(tmp, "shot.png")
	writePNG(t, imgPath) // helper from image_test.go (same package)

	fake := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{
			{ToolCall: &llm.ToolCall{ID: "i", Name: "read_image", RawArgs: `{"path":"` + imgPath + `"}`}},
			{Usage: &llm.Usage{TotalTokens: 5}},
		}},
		fakellm.Script{Events: []llm.StreamEvent{
			{TextDelta: "I see a red square"},
			{Usage: &llm.Usage{TotalTokens: 6}},
		}},
	)
	cfg := TurnConfig{
		LLM:         fake,
		Personality: persona.Persona{SystemPrompt: "test"},
		Log:         LogOrNoop(nil),
	}

	_, sess := collectTurn(t, context.Background(), cfg, "what is this")

	if !hasToolMessage(sess, "read_image", "Loaded image") {
		t.Fatalf("expected read_image tool result, got %+v", sess.messages)
	}

	toolIdx, imgUserIdx := -1, -1
	for i, m := range sess.messages {
		if m.Role == llm.RoleTool && m.Name == "read_image" {
			toolIdx = i
		}
		if m.Role == llm.RoleUser && msgHasImagePart(m) {
			imgUserIdx = i
		}
	}
	if imgUserIdx < 0 {
		t.Fatalf("no injected image user message in %+v", sess.messages)
	}
	if imgUserIdx <= toolIdx {
		t.Fatalf("image user message must follow the tool result (tool=%d img=%d)", toolIdx, imgUserIdx)
	}

	if fake.CallCount() != 2 {
		t.Fatalf("expected 2 LLM rounds, got %d", fake.CallCount())
	}
	if !anyMsgHasImagePart(fake.Calls[1].Msgs) {
		t.Fatalf("round 2 prompt is missing the injected image part")
	}
}

// TestRunTurn_ReadImage_ErrorQueuesNoImage: a bad path yields an error tool
// result and NO injected image user message.
func TestRunTurn_ReadImage_ErrorQueuesNoImage(t *testing.T) {
	fake := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{
			{ToolCall: &llm.ToolCall{ID: "i", Name: "read_image", RawArgs: `{"path":"/no/such/file.png"}`}},
			{Usage: &llm.Usage{TotalTokens: 5}},
		}},
		fakellm.Script{Events: []llm.StreamEvent{
			{TextDelta: "could not load"},
			{Usage: &llm.Usage{TotalTokens: 6}},
		}},
	)
	cfg := TurnConfig{
		LLM:         fake,
		Personality: persona.Persona{SystemPrompt: "test"},
		Log:         LogOrNoop(nil),
	}

	_, sess := collectTurn(t, context.Background(), cfg, "look")

	for _, m := range sess.messages {
		if m.Role == llm.RoleUser && msgHasImagePart(m) {
			t.Fatalf("error path must not inject an image user message")
		}
	}
}
```

- [ ] **Step 2: Run the test — verify it fails**

Run: `go test ./internal/chat/ -run 'RunTurn_ReadImage' -v 2>&1 | tail -20`
Expected: FAIL — `read_image` falls through to `error: unknown tool "read_image"`, so no `"Loaded image"` tool result and no injected user message.

- [ ] **Step 3: Add the `pendingImages` field to `toolLoopOutcome`**

In `internal/chat/turn.go`, change:

```go
type toolLoopOutcome struct {
	allMsgs      []llm.Message // updated slice (compact_history may have replaced it)
	cancelled    bool          // a guard returned a cancel decision
	cancelReason string        // reason text for the cancellation reminder
	pendingImages []llm.ContentPart // images loaded by read_image, injected as a user message after the loop
}
```

- [ ] **Step 4: Dispatch `read_image` inline and collect its image part**

In `executeToolCalls`, declare the accumulator near the top of the function, beside `cancelled`:

```go
	var cancelled bool
	var cancelReason string
	var pendingImages []llm.ContentPart
```

Add a branch in the `if out == "" { ... }` chain, immediately after the `shell_interactive` branch and before the `cfg.MCPToolNames[tc.Name]` branch:

```go
			} else if tc.Name == "read_image" {
				var part llm.ContentPart
				out, part = handleReadImage(tc.RawArgs, cfg.WorkDir)
				if part.ImageURL != "" {
					pendingImages = append(pendingImages, part)
				}
```

Then carry `pendingImages` out on the normal-completion return (the final `return` of `executeToolCalls`):

```go
	if ctx.Err() != nil {
		return toolLoopOutcome{}, ctx.Err()
	}
	return toolLoopOutcome{allMsgs: allMsgs, pendingImages: pendingImages}, nil
```

(Leave the guard-cancel return as-is — no image injection on a cancelled turn.)

- [ ] **Step 5: Inject the image user message in `RunTurn` after the reminder block**

In `RunTurn`, the end of the `for` loop body currently ends with the reminder injection block. Immediately AFTER that block (still inside the `for`), add:

```go
			// read_image results are text-only (tool messages can't carry images),
			// so any images it loaded are appended here as a synthetic user
			// message — the only role the adapter renders image parts for. This
			// runs after the reminder block so the reminder lands on the last tool
			// message (text), not on this parts-only user message (whose Content
			// the adapter ignores).
			if len(outcome.pendingImages) > 0 {
				parts := make([]llm.ContentPart, 0, len(outcome.pendingImages)+1)
				parts = append(parts, outcome.pendingImages...)
				parts = append(parts, llm.ContentPart{
					Type: llm.ContentPartTypeText,
					Text: "Above are the image(s) you loaded with read_image.",
				})
				imgMsg := llm.Message{
					Role:         llm.RoleUser,
					Content:      fmt.Sprintf("[read_image attached %d image(s)]", len(outcome.pendingImages)),
					ContentParts: parts,
				}
				allMsgs = append(allMsgs, imgMsg)
				sess.append(imgMsg)
			}
```

- [ ] **Step 6: Run the read_image turn tests — verify they pass**

Run: `go test ./internal/chat/ -run 'RunTurn_ReadImage' -v 2>&1 | tail -20`
Expected: PASS for both new tests.

- [ ] **Step 7: Run the whole chat package — verify no regression**

Run: `go test ./internal/chat/ 2>&1 | tail -20`
Expected: `ok  github.com/weatherjean/shell3/internal/chat`

- [ ] **Step 8: Commit**

```bash
git add internal/chat/turn.go internal/chat/turn_readimage_test.go
git commit -m "feat(chat): dispatch read_image tool and inject loaded image as user message"
```

---

## Task 3: Wire the `image` tool gate + schema (luacfg)

**Files:**
- Modify: `internal/luacfg/luacfg.go:22` (`ToolGates` struct)
- Modify: `internal/luacfg/register.go:86` (`toolGateKeys`) and `:174` (gate construction)
- Modify: `internal/luacfg/tooldefs.go` (add `readImageTool`, gate it in `ToolDefs`)
- Test: `internal/luacfg/tool_test.go` (add a focused test)

This task is independent of Tasks 1 & 2 (it only references the tool-name string `read_image` and the gate). It can run in parallel with them.

- [ ] **Step 1: Write the failing test**

Append to `internal/luacfg/tool_test.go`:

```go
func TestToolDefs_ImageGate(t *testing.T) {
	defs := ToolDefs(ToolGates{Image: true}, nil, false)
	var found bool
	for _, d := range defs {
		if d.Name == "read_image" {
			found = true
			if _, ok := d.Parameters["properties"].(map[string]any)["path"]; !ok {
				t.Errorf("read_image schema missing 'path' property")
			}
		}
	}
	if !found {
		t.Fatalf("read_image not present when Image gate on; got %d defs", len(defs))
	}

	// Gate off → absent.
	off := ToolDefs(ToolGates{}, nil, false)
	for _, d := range off {
		if d.Name == "read_image" {
			t.Fatalf("read_image present with Image gate off")
		}
	}
}
```

If `internal/luacfg/tool_test.go` does not exist, create it with the standard header:

```go
package luacfg

import "testing"
```

(plus the test above). Confirm the package name matches existing `_test.go` files in that dir (`package luacfg`).

- [ ] **Step 2: Run the test — verify it fails**

Run: `go test ./internal/luacfg/ -run 'ToolDefs_ImageGate' 2>&1 | head -20`
Expected: build failure — `unknown field Image in struct literal` / `undefined: readImageTool`.

- [ ] **Step 3: Add `Image` to `ToolGates`**

In `internal/luacfg/luacfg.go`:

```go
type ToolGates struct {
	Bash, BashBg, ShellInteractive, Edit, History, Prune, Compact, Image bool
}
```

- [ ] **Step 4: Add the `readImageTool` schema and gate it**

In `internal/luacfg/tooldefs.go`, add near the other tool vars:

```go
var readImageTool = llm.ToolDefinition{
	Name: "read_image",
	Description: "Load an image file (jpg, png, gif) from disk so you can SEE it. " +
		"The image is decoded, downscaled, and attached as a user message immediately after the tool results, so it appears in your view on the next step. " +
		"Requires a vision-capable model. This tool is for images only — to read text files use `bash` with cat/sed/head.",
	Parameters: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{"type": "string", "description": "Path to the image file (absolute or relative to the project root)."},
		},
		"required": []string{"path"},
	},
}
```

In `ToolDefs`, add a gate block (place it after the `g.Edit` block, before `g.History`):

```go
	if g.Image {
		defs = append(defs, readImageTool)
	}
```

- [ ] **Step 5: Add the Lua key and gate construction in `register.go`**

In `toolGateKeys` (line ~86), add `"image": true`:

```go
var toolGateKeys = map[string]bool{
	"bash": true, "bash_bg": true, "shell_interactive": true, "edit": true,
	"history": true, "custom": true, "skill": true,
	"prune": true, "compact": true, "mcp": true, "image": true,
}
```

In the `a.Gates = ToolGates{...}` literal (line ~174), add the field:

```go
		a.Gates = ToolGates{
			Bash:             optBool(tt, "bash"),
			BashBg:           optBool(tt, "bash_bg"),
			ShellInteractive: optBool(tt, "shell_interactive"),
			Edit:             optBool(tt, "edit"),
			History:          optBool(tt, "history"),
			Prune:            optBool(tt, "prune"),
			Compact:          optBool(tt, "compact"),
			Image:            optBool(tt, "image"),
		}
```

- [ ] **Step 6: Run the luacfg tests — verify pass**

Run: `go test ./internal/luacfg/ 2>&1 | tail -20`
Expected: `ok  github.com/weatherjean/shell3/internal/luacfg`

- [ ] **Step 7: Commit**

```bash
git add internal/luacfg/luacfg.go internal/luacfg/register.go internal/luacfg/tooldefs.go internal/luacfg/tool_test.go
git commit -m "feat(luacfg): add image tool gate and read_image schema"
```

---

## Task 4: Enable `read_image` in the scaffold config + docs

**Files:**
- Modify: `internal/scaffold/defaults/shell3.lua` (base + plan agent `tools` blocks, ~line 649 and ~line 682)
- Modify: `internal/scaffold/scaffold_test.go` (if it asserts on tool keys — otherwise skip)
- Modify: `README.md` (tool list, if one exists)

- [ ] **Step 1: Add `image = true` to the base agent tools block**

In `internal/scaffold/defaults/shell3.lua`, the `base` agent (`tools = { ... }` near line 649) — add the line after `compact = true,`:

```lua
    compact           = true,
    image             = true,   -- read_image: load an image so a vision-capable model can see it
    custom            = { web_fetch, brave_search },
```

- [ ] **Step 2: Add `image = true` to the plan agent tools block**

In the `plan` agent (`tools = { ... }` near line 682) — add the same line after `compact = true,`:

```lua
    compact           = true,
    image             = true,   -- read_image: load an image so a vision-capable model can see it
    custom            = { web_fetch, brave_search },
```

- [ ] **Step 3: Check the scaffold test does not pin an exact tool-key set**

Run: `go test ./internal/scaffold/ 2>&1 | tail -20`
Expected: `ok`. If it fails because a test asserts an exact tool-key list, update that test's expected set to include `image`.

- [ ] **Step 4: Update README tool documentation (only if a tool list exists)**

Run: `grep -n "edit_file\|prune_tool_result\|bash_bg" README.md | head`
If a built-in tool list is present, add a `read_image` entry mirroring the others, e.g.:

```markdown
- `read_image` — load an image file (jpg/png/gif) so a vision-capable model can see it.
```

If no such list exists, skip this step (do not invent a new section).

- [ ] **Step 5: Commit**

```bash
git add internal/scaffold/defaults/shell3.lua internal/scaffold/scaffold_test.go README.md
git commit -m "docs(scaffold): enable read_image tool in default agents"
```

---

## Task 5: Full-suite verification + build

**Files:** none (verification only)

- [ ] **Step 1: Build**

Run: `make build`
Expected: builds cleanly, no errors.

- [ ] **Step 2: Run the entire test suite**

Run: `go test ./... 2>&1 | tail -40`
Expected: every package `ok` (or cached). No `FAIL`.

- [ ] **Step 3: Vet**

Run: `go vet ./... 2>&1 | tail -20`
Expected: no output.

- [ ] **Step 4: Final commit (if anything outstanding)**

```bash
git status
# only if there are uncommitted changes from the verification step
```

---

## Self-Review notes

- **Spec coverage:** model-callable tool (Task 2/3), image reaches the model despite text-only tool messages (Task 2 user-message injection), reuses existing image pipeline (Task 1), config/gate surface (Task 3/4), tested end-to-end with fakellm (Task 2), full-suite green (Task 5). ✔
- **Type consistency:** `loadImagePart` returns `(llm.ContentPart, int, int, error)`; `handleReadImage` returns `(string, llm.ContentPart)`; `resizeAndEncodeJPEG` returns `(string, int, int, error)`; `toolLoopOutcome.pendingImages []llm.ContentPart`; gate field `Image` / Lua key `image` — consistent across Tasks 1–4. ✔
- **No adapter change needed:** `toMessages` already renders user `ContentParts` as `ImageContentPart`; tool messages stay text-only. ✔
- **Out of scope (documented, not fixed here):** `.webp` is in `supportedImageExts` but Go stdlib has no webp decoder, so real webp will fail at decode — pre-existing behavior, unchanged. Token estimation does not count `ContentParts` bytes — pre-existing, consistent with `/image`.
