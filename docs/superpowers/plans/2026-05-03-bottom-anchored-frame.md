# Bottom-Anchored Live Frame Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Anchor `patchapp` live frame (streaming preview + input box + status bar) to the bottom N rows of the terminal so scrollback grows above it. When TTY handed off (hook/shell_interactive), only the live frame area is erased; widgets render at the bottom too.

**Architecture:** Use DECSTBM scroll region (`CSI t;b r`) to reserve the bottom `frameHeight` rows. `Print` writes inside the scroll region (rows scroll up naturally as more lines arrive). `Render` positions cursor outside the region (the bottom band) and paints the frame there. `Erase` clears only the bottom band.

**Tech Stack:** Go, ANSI escapes (DECSTBM, CUP, ED, EL).

---

## File Structure

- `internal/patchtui/renderer.go` — add `frameHeight` field, scroll-region management, bottom-anchored Render/Print/Erase. This is the core change.
- `internal/patchtui/renderer_test.go` — extend with a fake-tty buffer test verifying the emitted escape sequence layout.
- `internal/patchapp/loop.go` — set scroll region on startup, clear on shutdown.
- `internal/patchapp/lifecycle.go` — Pause clears bottom band only; Resume re-establishes scroll region.
- `internal/patchwidgets/confirm.go` (and friends) — no change needed; opens fresh tty so scroll region from parent shell is irrelevant. Widget paints inline; we'll position it at the bottom in `confirm.go` by emitting CUP before render.

## Conventions

- ANSI sequences as raw strings: `"\x1b[<top>;<bot>r"` (DECSTBM), `"\x1b[<row>;<col>H"` (CUP), `"\x1b[2K"` (EL2), `"\x1b[0J"` (ED0), `"\x1b[r"` (DECSTBM reset).
- Row/col are 1-indexed in CUP. Internal row tracking remains 0-indexed (frame-relative).
- `frameHeight` is the count of frame rows last rendered. Scroll region is `[1, height - frameHeight]`. Frame lives at `[height - frameHeight + 1, height]`.
- When `frameHeight` changes, recompute and emit new scroll region before painting.

---

## Task 1: Add frameHeight tracking + scroll-region helpers

**Files:**
- Modify: `internal/patchtui/renderer.go`

- [ ] **Step 1: Add `frameHeight` field and helpers**

In `Renderer` struct add:
```go
frameHeight int // rows reserved at bottom for the live frame
```

After `sizeFromFd` add:
```go
// setScrollRegion emits DECSTBM to reserve the bottom frameHeight rows for
// the live frame. The scrollback region becomes rows [1, height-frameHeight].
// Caller must hold r.mu.
func (r *Renderer) setScrollRegion(buf *strings.Builder) {
	if r.frameHeight <= 0 || r.height <= r.frameHeight {
		return
	}
	fmt.Fprintf(buf, "\x1b[1;%dr", r.height-r.frameHeight)
}

// resetScrollRegion clears DECSTBM so the full screen scrolls again.
// Call on shutdown.
func (r *Renderer) resetScrollRegion(buf *strings.Builder) {
	buf.WriteString("\x1b[r")
}

// frameTopRow returns the 1-indexed terminal row of the frame's first line.
func (r *Renderer) frameTopRow() int {
	if r.height <= r.frameHeight {
		return 1
	}
	return r.height - r.frameHeight + 1
}
```

- [ ] **Step 2: Build clean**

Run: `go build ./internal/patchtui/`
Expected: success.

- [ ] **Step 3: Commit**

```bash
git add internal/patchtui/renderer.go
git commit -m "patchtui: add frameHeight + scroll-region helpers"
```

---

## Task 2: Render paints frame at bottom band

**Files:**
- Modify: `internal/patchtui/renderer.go`

- [ ] **Step 1: Update Render to position cursor at frame top before painting**

Locate `Render`. After `clean := …` extraction and `lines = clean`, BEFORE the `var buf strings.Builder` line, capture the previous frame height:
```go
oldFrameHeight := r.frameHeight
r.frameHeight = len(lines)
```

Then after `buf.WriteString("\x1b[?25l\x1b[?2026h")`:
```go
// Reserve scrollback / frame split when frame size changes.
if r.frameHeight != oldFrameHeight || sizeChanged {
    r.setScrollRegion(&buf)
}
// Position cursor at frame top so paint lands in the bottom band.
fmt.Fprintf(&buf, "\x1b[%d;1H", r.frameTopRow())
```

Replace the existing `if !r.inited || sizeChanged { r.fullRender(&buf, lines, sizeChanged) }` and `else { r.diffRender(&buf, lines) }` with:
```go
// Always full-render after positioning at frame top.
r.fullRender(&buf, lines, false)
```

(diffRender is incompatible with absolute positioning — drop it for the bottom-anchored path. Keep the function for now but unused.)

- [ ] **Step 2: Update fullRender to not emit \r at the start**

Inside `fullRender`, replace:
```go
if sizeChanged {
    buf.WriteString("\x1b[2J\x1b[H") // clear screen, cursor home
} else {
    buf.WriteString("\r")
}
```

with:
```go
// Cursor has already been positioned by caller (Render) at frame top.
// We just clear each line before writing.
```

- [ ] **Step 3: Build**

Run: `go build ./internal/patchtui/`
Expected: success (diffRender now unused — that's fine, leave for now).

- [ ] **Step 4: Commit**

```bash
git add internal/patchtui/renderer.go
git commit -m "patchtui: bottom-anchor Render via CUP + scroll region"
```

---

## Task 3: Print writes inside scroll region

**Files:**
- Modify: `internal/patchtui/renderer.go`

- [ ] **Step 1: Rewrite Print body**

Replace `Print` body with:
```go
func (r *Renderer) Print(lines []string) {
    r.mu.Lock()
    defer r.mu.Unlock()

    var buf strings.Builder
    buf.WriteString("\x1b[?25l\x1b[?2026h")

    // Make sure scroll region is set (in case Render hasn't been called).
    r.setScrollRegion(&buf)

    // Position cursor at the bottom row of the scroll region. Writing a line
    // then \r\n there triggers a one-line scroll within the region, leaving
    // the bottom frame band untouched.
    bottomOfScroll := r.height - r.frameHeight
    if bottomOfScroll < 1 {
        bottomOfScroll = r.height // fallback when no frame yet
    }
    fmt.Fprintf(&buf, "\x1b[%d;1H", bottomOfScroll)

    for _, line := range lines {
        buf.WriteString("\x1b[2K")
        buf.WriteString(line)
        buf.WriteString("\r\n")
    }

    buf.WriteString("\x1b[?2026l")
    io.WriteString(r.writer(), buf.String()) //nolint:errcheck

    // Re-paint the live frame on top — its content didn't change but the
    // CUP we just emitted may have moved the cursor outside.
    r.prev = nil
    r.inited = false
    r.cursorRow = 0
}
```

- [ ] **Step 2: Update PrintAndRender similarly**

Replace `PrintAndRender` body with:
```go
func (r *Renderer) PrintAndRender(lines, frame []string) {
    r.Print(lines)
    r.Render(frame)
}
```

(Composing the two simpler primitives. Sync flicker is acceptable since both wrap their own DEC 2026 sync.)

- [ ] **Step 3: Build**

Run: `go build ./internal/patchtui/`
Expected: success.

- [ ] **Step 4: Commit**

```bash
git add internal/patchtui/renderer.go
git commit -m "patchtui: Print writes inside scroll region; PrintAndRender = Print+Render"
```

---

## Task 4: Erase clears only the bottom band

**Files:**
- Modify: `internal/patchtui/renderer.go`

- [ ] **Step 1: Rewrite Erase**

Replace `Erase` body with:
```go
func (r *Renderer) Erase() {
    r.mu.Lock()
    defer r.mu.Unlock()
    if r.frameHeight <= 0 {
        return
    }
    var buf strings.Builder
    buf.WriteString("\x1b[?2026h")
    fmt.Fprintf(&buf, "\x1b[%d;1H", r.frameTopRow())
    buf.WriteString("\x1b[0J") // erase from cursor to end of screen
    buf.WriteString("\x1b[?2026l")
    io.WriteString(r.writer(), buf.String()) //nolint:errcheck
    r.prev = nil
    r.inited = false
    r.cursorRow = 0
    r.frameHeight = 0
}
```

- [ ] **Step 2: Build**

Run: `go build ./internal/patchtui/`
Expected: success.

- [ ] **Step 3: Commit**

```bash
git add internal/patchtui/renderer.go
git commit -m "patchtui: Erase only clears bottom frame band"
```

---

## Task 5: Add ResetScrollRegion + integrate into App lifecycle

**Files:**
- Modify: `internal/patchtui/renderer.go`
- Modify: `internal/patchapp/loop.go`
- Modify: `internal/patchapp/lifecycle.go`

- [ ] **Step 1: Add public Reset method that also resets scroll region**

Append to renderer.go:
```go
// Teardown emits DECSTBM reset so the terminal scrolls normally after the
// app exits. Call once on shutdown.
func (r *Renderer) Teardown() {
    r.mu.Lock()
    defer r.mu.Unlock()
    var buf strings.Builder
    r.resetScrollRegion(&buf)
    io.WriteString(r.writer(), buf.String()) //nolint:errcheck
    r.frameHeight = 0
}
```

- [ ] **Step 2: Wire Teardown in patchapp loop**

In `internal/patchapp/loop.go`, find the loop teardown (where the renderer is finalized after the loop exits). Add `a.r.Teardown()` after the loop ends and before final cleanup. (Search for where the loop returns.)

If the loop exits at `return nil` near the bottom of the run function, immediately before that:
```go
a.r.Teardown()
```

- [ ] **Step 3: Lifecycle Pause/Resume**

In `internal/patchapp/lifecycle.go`:

`Pause` already calls `a.r.Erase()` — Erase now only clears the bottom band, which is the desired behavior. No change.

`Resume`: after `a.r.Reset()` and `a.render()`, the scroll region is re-emitted by `Render` automatically (since `frameHeight != oldFrameHeight` after Reset zeroed it). No change.

- [ ] **Step 4: Build all**

Run: `go build ./...`
Expected: success.

- [ ] **Step 5: Commit**

```bash
git add internal/patchtui/renderer.go internal/patchapp/loop.go
git commit -m "patchapp: teardown DECSTBM on app exit"
```

---

## Task 6: Position confirm widget at bottom of terminal

**Files:**
- Modify: `internal/patchwidgets/confirm.go`
- Modify: `internal/patchwidgets/ask.go`
- Modify: `internal/patchwidgets/pick.go`

- [ ] **Step 1: Confirm widget — pre-position cursor at bottom**

In `internal/patchwidgets/confirm.go`, after `r := patchtui.New()` and `r.SetOutput(t.f)`, before `defer r.Erase()`:

```go
// Reserve N bottom rows for the widget by writing N newlines and moving
// back up. This pushes any stale scrollback up so the widget always paints
// in the last N rows.
const widgetRows = 3
for i := 0; i < widgetRows; i++ {
    t.f.WriteString("\r\n")
}
fmt.Fprintf(t.f, "\x1b[%dA", widgetRows) // move cursor up N
```

Add `"fmt"` import if missing.

- [ ] **Step 2: Same for ask.go and pick.go**

Apply the same pattern in `ask.go` and `pick.go`. For `pick.go` use a larger constant (the picker frame can be tall). Use `widgetRows = 8` for pick.

For ask use `widgetRows = 3`.

- [ ] **Step 3: Build**

Run: `go build ./internal/patchwidgets/`
Expected: success.

- [ ] **Step 4: Commit**

```bash
git add internal/patchwidgets/
git commit -m "patchwidgets: pre-scroll terminal so widget paints in bottom rows"
```

---

## Task 7: Manual smoke test

- [ ] **Step 1: Build and install**

Run: `make install`
Expected: `/Users/weatherjean/go/bin/shell3` updated.

- [ ] **Step 2: Run shell3, verify**

Run: `shell3`
Verify:
1. Welcome card renders, then input bar at the BOTTOM of the terminal (not just below the welcome).
2. Type "hi", get a response. Input bar still at bottom; conversation in the middle scrolls up.
3. Trigger a bash tool call. Confirm widget appears in the BOTTOM rows of the terminal, not at the top.
4. Confirm with Enter. Tool result prints in scrollback (top region), input bar back at bottom.
5. Quit with Ctrl-C / `/exit`. Terminal scroll region resets — verify by typing in shell after, content scrolls normally.

- [ ] **Step 3: Final commit if test passes**

If everything passes, no further commits. Push branch:
```bash
git push -u origin feat/bottom-anchored-frame
```
