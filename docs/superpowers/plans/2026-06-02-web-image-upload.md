# Web image upload — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Add single-image upload to `shell3 web` — an attach button uploads one image with the textarea text as prompt; the server resizes/encodes (reusing pkg/chat) and runs a multimodal turn.

**Architecture:** Reuse the engine's existing vision path (`llm.Message.ContentParts`, `chat.RunTurn`). Add a bytes-based image builder in `pkg/chat`, generalize the web `Hub` to run any `llm.Message`, add a `POST /image` multipart endpoint, and an attach-button UI. `internal/web` keeps depending only on `pkg/chat` + `pkg/llm`.

**Tech stack:** Go 1.25, stdlib `image`/`mime/multipart`, the existing `pkg/llm/fakellm` for tests.

---

## Task A: bytes-based image builder in pkg/chat

**Files:** Modify `pkg/chat/image.go`; Create `pkg/chat/image_bytes_test.go`

- [ ] **Step 1: failing test** — create `pkg/chat/image_bytes_test.go`:

```go
package chat

import (
	"bytes"
	"image"
	"image/png"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/pkg/llm"
)

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestBuildImageMessageFromBytes_OK(t *testing.T) {
	msg, err := BuildImageMessageFromBytes(tinyPNG(t), "")
	if err != nil {
		t.Fatalf("BuildImageMessageFromBytes: %v", err)
	}
	if len(msg.ContentParts) != 2 {
		t.Fatalf("parts = %d, want 2", len(msg.ContentParts))
	}
	if msg.ContentParts[0].Type != llm.ContentPartTypeImageURL ||
		!strings.HasPrefix(msg.ContentParts[0].ImageURL, "data:image/jpeg;base64,") {
		t.Errorf("image part wrong: %+v", msg.ContentParts[0])
	}
	if msg.ContentParts[1].Type != llm.ContentPartTypeText || msg.ContentParts[1].Text != "Describe this image." {
		t.Errorf("text part wrong: %+v", msg.ContentParts[1])
	}
}

func TestBuildImageMessageFromBytes_Oversize(t *testing.T) {
	if _, err := BuildImageMessageFromBytes(make([]byte, maxImageBytes+1), "x"); err == nil {
		t.Fatal("expected error for oversize image")
	}
}

func TestBuildImageMessageFromBytes_Undecodable(t *testing.T) {
	if _, err := BuildImageMessageFromBytes([]byte("not an image"), "x"); err == nil {
		t.Fatal("expected error for undecodable bytes")
	}
}
```

- [ ] **Step 2: run** `go test ./pkg/chat/ -run BuildImageMessageFromBytes` → FAIL (undefined).

- [ ] **Step 3: implement** — in `pkg/chat/image.go`, add the function and refactor `BuildImageMessage` to delegate.

Add after the `const`/`var` block:

```go
// BuildImageMessageFromBytes builds a multimodal user message from raw image
// bytes (from an upload or a file). It enforces the 10 MB cap, resizes/encodes
// to JPEG, and defaults the prompt when empty. Shared by the TUI /image path
// and the web /image upload.
func BuildImageMessageFromBytes(raw []byte, prompt string) (llm.Message, error) {
	if len(raw) > maxImageBytes {
		return llm.Message{}, fmt.Errorf("image too large (%d MB, max 10 MB)", len(raw)>>20)
	}
	encoded, err := resizeAndEncodeJPEG(raw, maxImageSide, jpegQuality)
	if err != nil {
		return llm.Message{}, fmt.Errorf("image encode: %w", err)
	}
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		prompt = "Describe this image."
	}
	return llm.Message{
		Role: llm.RoleUser,
		ContentParts: []llm.ContentPart{
			{Type: llm.ContentPartTypeImageURL, ImageURL: "data:image/jpeg;base64," + encoded},
			{Type: llm.ContentPartTypeText, Text: prompt},
		},
	}, nil
}
```

Then in `BuildImageMessage`, replace the tail (from `raw, err := os.ReadFile(path)` to the end of the function) with:

```go
	raw, err := os.ReadFile(path)
	if err != nil {
		return llm.Message{}, fmt.Errorf(`cannot read "%s": %w`, path, err)
	}
	return BuildImageMessageFromBytes(raw, prompt)
```

and delete the now-redundant `if prompt == "" { prompt = "Describe this image." }` block earlier in `BuildImageMessage` (the bytes builder defaults it). Keep the path/ext/`os.Stat` size checks.

- [ ] **Step 4: run** `go test ./pkg/chat/` → PASS (new + existing image tests).

- [ ] **Step 5: commit**

```bash
git add pkg/chat/image.go pkg/chat/image_bytes_test.go
git commit -m "feat(chat): BuildImageMessageFromBytes; share encode path with /image"
```

---

## Task B: generalize the web Hub to run any llm.Message

**Files:** Modify `internal/web/hub.go`, `internal/web/hub_test.go`, `internal/web/server_test.go`, `cmd/shell3/web.go`

- [ ] **Step 1: change the Hub** in `internal/web/hub.go`.

Add the import:
```go
	"github.com/weatherjean/shell3/pkg/llm"
```
(group with the existing `github.com/weatherjean/shell3/pkg/chat` import.)

Change the `run` field type in the `Hub` struct from:
```go
	run  func(ctx context.Context, input string) // blocks until the turn completes
```
to:
```go
	run  func(ctx context.Context, msg llm.Message) // blocks until the turn completes
```

Change `NewHub`'s signature accordingly:
```go
func NewHub(sess *chat.Session, run func(ctx context.Context, msg llm.Message)) *Hub {
	return &Hub{sess: sess, run: run, subs: make(map[*subscriber]struct{})}
}
```

Replace the whole `Submit` method with a private `submit` plus two public entry points:
```go
// submit starts a turn for msg. Returns ErrBusy if a turn is in flight.
func (h *Hub) submit(msg llm.Message) error {
	h.mu.Lock()
	if h.busy {
		h.mu.Unlock()
		return ErrBusy
	}
	h.busy = true
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	h.wg.Add(1) // under the lock: Close() must never observe a 0 count between unlock and Add
	h.mu.Unlock()

	go func() {
		defer func() {
			cancel()
			h.mu.Lock()
			h.busy = false
			h.cancel = nil
			h.mu.Unlock()
			h.wg.Done()
		}()
		h.run(ctx, msg)
	}()
	return nil
}

// Submit starts a plain-text turn.
func (h *Hub) Submit(text string) error {
	return h.submit(llm.Message{Role: llm.RoleUser, Content: text})
}

// SubmitMessage starts a turn for a prebuilt message (e.g. multimodal/image).
func (h *Hub) SubmitMessage(msg llm.Message) error {
	return h.submit(msg)
}
```

- [ ] **Step 2: update the run closures in the tests** so they match the new signature.

In `internal/web/hub_test.go`:
- `newTestHub`: change `run := func(ctx context.Context, input string) { sess.Run(ctx, tc, input) }` to `run := func(ctx context.Context, msg llm.Message) { sess.Run(ctx, tc, msg.Content) }`.
- `TestHub_CancelAbortsInFlightTurn` and `TestHub_BusyRejectsConcurrentSubmit`: change their local `run := func(ctx context.Context, input string) { ... }` to `func(ctx context.Context, msg llm.Message) { ... }` (the bodies ignore the arg — keep them).

In `internal/web/server_test.go`:
- `newTestServer`: change the closure passed to `NewHub` from `func(ctx context.Context, input string) { sess.Run(ctx, tc, input) }` to `func(ctx context.Context, msg llm.Message) { sess.Run(ctx, tc, msg.Content) }`. (`pkg/llm` is already imported there.)

- [ ] **Step 3: update `cmd/shell3/web.go` run closure** to route by message kind.

Add the import `"github.com/weatherjean/shell3/pkg/llm"` (group with the other `pkg/...` imports). Replace the `web.NewHub(...)` call:
```go
	hub := web.NewHub(sess, func(turnCtx context.Context, msg llm.Message) {
		modelMu.Lock()
		snapshot := tc
		modelMu.Unlock()
		if len(msg.ContentParts) == 0 {
			sess.Run(turnCtx, snapshot, msg.Content)
		} else {
			chat.RunTurn(turnCtx, snapshot, sess, msg)
		}
	})
	hub.Start()
```

- [ ] **Step 4: build + test** — `go build ./... && go test ./internal/web/ ./cmd/shell3/ -race` → PASS.

- [ ] **Step 5: commit**

```bash
git add internal/web/hub.go internal/web/hub_test.go internal/web/server_test.go cmd/shell3/web.go
git commit -m "feat(web): Hub.SubmitMessage; run closure routes multimodal turns"
```

---

## Task C: POST /image endpoint

**Files:** Modify `internal/web/server.go`, `internal/web/server_test.go`

- [ ] **Step 1: failing test** — add to `internal/web/server_test.go`.

Add imports `"bytes"`, `"image"`, `"image/png"`, `"mime/multipart"` to the existing import block. Then add:

```go
func pngMultipart(t *testing.T, prompt string) (string, *bytes.Buffer) {
	t.Helper()
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	fw, err := mw.CreateFormFile("image", "x.png")
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(fw, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	if prompt != "" {
		_ = mw.WriteField("prompt", prompt)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	return mw.FormDataContentType(), body
}

func TestServer_ImageUploadReturns202(t *testing.T) {
	srv := newTestServer(t, fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "ok"}}})
	ct, body := pngMultipart(t, "what is this")
	res, err := http.Post(srv.URL+"/image", ct, body)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("image status = %d, want 202", res.StatusCode)
	}
}

func TestServer_ImageUploadRejectsNonImage(t *testing.T) {
	srv := newTestServer(t)
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	fw, _ := mw.CreateFormFile("image", "x.png")
	_, _ = fw.Write([]byte("not an image"))
	_ = mw.Close()
	res, err := http.Post(srv.URL+"/image", mw.FormDataContentType(), body)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", res.StatusCode)
	}
}
```

- [ ] **Step 2: run** `go test ./internal/web/ -run TestServer_ImageUpload` → FAIL (404/no handler).

- [ ] **Step 3: implement** in `internal/web/server.go`. Register the route in `Handler()` (next to the other POSTs):
```go
	mux.HandleFunc("POST /image", s.handleImage)
```
Add the handler:
```go
func (s *Server) handleImage(w http.ResponseWriter, r *http.Request) {
	if s.hub.Busy() {
		http.Error(w, "agent busy", http.StatusConflict)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 12<<20)
	if err := r.ParseMultipartForm(12 << 20); err != nil {
		http.Error(w, "bad upload", http.StatusBadRequest)
		return
	}
	file, _, err := r.FormFile("image")
	if err != nil {
		http.Error(w, "missing image", http.StatusBadRequest)
		return
	}
	defer file.Close()
	raw, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, "read failed", http.StatusBadRequest)
		return
	}
	msg, err := chat.BuildImageMessageFromBytes(raw, r.FormValue("prompt"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.hub.SubmitMessage(msg); err != nil {
		http.Error(w, "agent busy", http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}
```
(`io`, `net/http`, `chat` are already imported in server.go.)

- [ ] **Step 4: run** `go test ./internal/web/ -race` → PASS.

- [ ] **Step 5: commit**

```bash
git add internal/web/server.go internal/web/server_test.go
git commit -m "feat(web): POST /image multipart upload runs a multimodal turn"
```

---

## Task D: attach-button UI

**Files:** Modify `internal/web/assets/index.html`

- [ ] **Step 1: add the attach button + hidden file input + preview chip.**

In the `#buttons` row, add an attach button as the first child:
```html
    <div id="buttons">
      <button id="attach" title="Attach image">📎</button>
      <button id="send">Send</button>
      <button id="stop">Stop</button>
      <button id="clear">Clear</button>
    </div>
```
Immediately after the `<textarea id="input" …></textarea>` line (inside `#bar`, before `#buttons`), add the hidden input and preview chip:
```html
    <input id="file" type="file" accept="image/*" hidden />
    <div id="preview" hidden></div>
```

- [ ] **Step 2: add CSS** (near the `#buttons` rules):
```css
  #attach { flex: 0 0 auto; }
  #preview { display: flex; align-items: center; gap: 8px; color: var(--dim); }
  #preview img { height: 40px; width: 40px; object-fit: cover; border: 1px solid #2a2b3d; border-radius: 4px; }
  #preview button { padding: 2px 8px; }
```

- [ ] **Step 3: add the image logic** in the `<script>`. Near the top globals (after `let pinned = …`):
```js
let pendingImage = null; // File awaiting send
```
Add helpers (place beside `send`):
```js
const fileEl = document.getElementById('file');
const previewEl = document.getElementById('preview');

function clearPending() {
  if (pendingImage && pendingImage._url) URL.revokeObjectURL(pendingImage._url);
  pendingImage = null;
  previewEl.hidden = true;
  previewEl.innerHTML = '';
  fileEl.value = '';
}
function setPending(file) {
  clearPending();
  pendingImage = file;
  file._url = URL.createObjectURL(file);
  previewEl.hidden = false;
  const img = el('img'); img.src = file._url;
  const name = el('span', null, file.name);
  const x = el('button', null, '✕'); x.onclick = clearPending;
  previewEl.appendChild(img); previewEl.appendChild(name); previewEl.appendChild(x);
}
async function sendImage(text) {
  const fd = new FormData();
  fd.append('image', pendingImage);
  fd.append('prompt', text);
  const url = pendingImage._url, name = pendingImage.name;
  pinned = true;
  const res = await fetch('/image', { method: 'POST', body: fd });
  if (res.status === 202) {
    // Render our own user block (server emits no user_message for image turns).
    const b = block('user', '');
    const img = el('img'); img.src = url; img.style.height = '120px'; img.style.borderRadius = '4px'; img.style.display = 'block'; img.style.margin = '4px 0';
    b.appendChild(img);
    if (text) b.appendChild(el('div', null, text));
    pendingImage = null; previewEl.hidden = true; previewEl.innerHTML = ''; fileEl.value = '';
  } else {
    block('meta', res.status === 409 ? '(agent busy — wait or Stop the current turn)' : '✗ image upload failed');
  }
}
```
> Note: in `sendImage` we don't `revokeObjectURL` immediately because the rendered `<img>` still uses the blob URL; it's released on `/clear` (`resetView`) implicitly when the DOM is cleared. That's acceptable for the MVP.

Wire the attach button + file input (next to the other button handlers):
```js
document.getElementById('attach').onclick = () => fileEl.click();
fileEl.addEventListener('change', () => { if (fileEl.files[0]) setPending(fileEl.files[0]); });
```

Update `setBusy` to also disable attach:
```js
function setBusy(b) { document.body.classList.toggle('busy', b); inputEl.disabled = b; sendEl.disabled = b; document.getElementById('attach').disabled = b; }
```

In `send()`, handle the pending image **before** the slash/`/input` logic — right after `inputEl.value = ''; inputEl.style.height = 'auto';`:
```js
  if (pendingImage) { await sendImage(text); return; }
```
(Allows an empty `text` — the server defaults the prompt.) Keep the early `if (!text) return;` but move it: change the top of `send()` so an attached image can send with no text:
```js
async function send() {
  const text = inputEl.value.trim();
  if (!text && !pendingImage) return;
  inputEl.value = '';
  inputEl.style.height = 'auto';
  if (pendingImage) { await sendImage(text); return; }
  ...
```

- [ ] **Step 4: build** `go build ./internal/web/ ./cmd/shell3` → success (embed compiles).

- [ ] **Step 5: commit**

```bash
git add internal/web/assets/index.html
git commit -m "feat(web): attach-button image upload with preview"
```

---

## Task E: full validation

- [ ] **Step 1:** `go build ./... && go vet ./... && go test ./... -race` → all green.
- [ ] **Step 2:** confirm layering: `go list -f '{{join .Imports "\n"}}' ./internal/web | grep weatherjean` shows only `pkg/chat` and `pkg/llm` (no `internal/*`).
- [ ] **Step 3:** commit any fixes.

---

## Self-review notes
- Spec coverage: bytes builder → A; Hub multimodal + run routing → B; `/image` endpoint → C; UI → D; validation/layering → E. All spec sections mapped.
- Type consistency: `BuildImageMessageFromBytes(raw, prompt)`, `Hub.SubmitMessage(llm.Message)`, run closure `func(ctx, llm.Message)`, `POST /image` — consistent across tasks.
- No placeholders; every code step is complete.
