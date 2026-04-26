# patchtui

Differential terminal renderer for inline TUI apps. Keeps native scrollback and text selection working — never enters the alternate screen.

## What it does

- **Differential render**: each `Render(frame)` diffs against the previous frame and overwrites only the lines that changed. Cursor moves are bounded to the live frame, regardless of scrollback length.
- **Synchronized output** (CSI ?2026): each update wraps in begin/end sync so the terminal paints atomically — no flicker.
- **Print to scrollback**: `Print(lines)` commits text once. Never re-rendered.
- **Cursor placement**: embed `CursorMarker` anywhere in a frame line; the renderer strips it from output and parks the hardware cursor at that column.
- **ANSI primitives** (`ansi.go`): `Bold`, `Dim`, `Reset`, named colors, `FgRGB`, `BgRGB`.
- **Text helpers** (`text.go`): `SplitLines`, `VisibleLen` (ANSI-aware), `RuneWidth` (CJK/emoji).

## What it does NOT do

- No keyboard input handling.
- No raw/cooked mode switching (use `golang.org/x/term`).
- No styling theme — colors are caller's choice.

## Usage

```go
r := patchtui.New()

// Commit history (won't be re-rendered).
r.Print([]string{"> hello"})

// Live frame with cursor at end of input.
r.Render([]string{
    "> " + userInput + patchtui.CursorMarker,
    "── status ──",
})
```

For input loop + lifecycle on top of patchtui, see `patchapp`.
