// Package patchtui is a differential terminal renderer for inline TUI apps.
//
// patchtui solves a specific problem: building a TUI app where the user can
// still select and copy text natively in their terminal. Most TUI frameworks
// take over the entire screen via the alternate screen buffer or capture
// mouse events for a viewport, both of which break native text selection.
//
// patchtui never enters the alternate screen and never clears scrollback.
// Each call to [Renderer.Render] diffs the new frame against the previous
// one, moves the cursor with ANSI escapes, and overwrites only the lines
// that actually changed. Synchronized output (CSI ?2026) wraps each update
// so the terminal paints atomically without flicker.
//
// Lines committed to scrollback via [Renderer.Print] are written once and
// never re-rendered, which keeps cursor movement bounded to the live frame
// regardless of history length.
//
// To position the hardware cursor inside the rendered frame (for example,
// at the end of an input field), embed [CursorMarker] in any frame line.
// The renderer strips the marker before writing and places the terminal
// cursor at that exact column.
//
// Typical usage:
//
//	r := patchtui.New()
//
//	// Commit a user message to scrollback (won't be re-rendered).
//	r.Print([]string{"> hello"})
//
//	// Render the live area (input box + status bar). The marker tells the
//	// renderer where to leave the hardware cursor.
//	r.Render([]string{
//	    "> " + userInput + patchtui.CursorMarker,
//	    "── status ──",
//	})
//
// patchtui does not handle keyboard input or terminal mode switching;
// callers wire those up themselves (typically with golang.org/x/term for
// raw mode). For convenience, base SGR primitives — [Reset], [Bold], named
// colors, and [FgRGB]/[BgRGB] — are exported in ansi.go so callers do not
// have to redefine the common escape sequences.
package patchtui

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

// CursorMarker is a zero-width APC sequence. Embed it in any frame line
// passed to [Renderer.Render] to mark the column where the hardware cursor
// should be placed after the render completes. The renderer strips the
// marker from the output, so it never reaches the terminal.
//
// Only one marker per frame is supported; if multiple are present, the
// last one wins.
const CursorMarker = "\x1b_cur\x1b\\"

// Renderer maintains the state needed for differential rendering.
//
// A Renderer is safe for concurrent use across goroutines: each public
// method holds an internal mutex for the duration of the call. Callers
// should still serialize render-related state changes externally to avoid
// frame tearing across logically-related updates.
type Renderer struct {
	mu        sync.Mutex
	prev      []string // previous frame, with cursor marker stripped
	cursorRow int      // current hardware cursor row, relative to frame row 0
	width     int
	height    int
	inited    bool
	out       io.Writer // destination; nil means os.Stdout
	outFile   *os.File  // set when out is an *os.File; used for TIOCGWINSZ
}

// New returns a new Renderer. The first call to [Renderer.Render] or
// [Renderer.Print] writes from the current cursor position; subsequent
// calls update the frame in place.
//
// The terminal size is sampled immediately so that the first Render does not
// treat the transition from zero to actual dimensions as a size change and
// emit a full-screen clear.
func New() *Renderer {
	w, h := Size()
	return &Renderer{width: w, height: h}
}

// SetOutput redirects renderer output to w. By default the renderer writes
// to os.Stdout. Pass nil to restore the default. Safe to call between
// renders; do not call concurrently with Render/Print.
//
// If w implements *os.File, the renderer uses its file descriptor for
// TIOCGWINSZ so that terminal size is read correctly even when os.Stdout
// is a pipe (e.g. inside hook subprocesses).
func (r *Renderer) SetOutput(w io.Writer) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.out = w
	if f, ok := w.(*os.File); ok {
		r.outFile = f
		// Re-sample size from the new output file so the next Render does not
		// treat the source switch as a size change and emit a full-screen clear.
		r.width, r.height = sizeFromFd(f.Fd())
	} else {
		r.outFile = nil
	}
}

// writer returns the current output destination.
func (r *Renderer) writer() io.Writer {
	if r.out != nil {
		return r.out
	}
	return os.Stdout
}

// winsize mirrors the kernel struct used by the TIOCGWINSZ ioctl.
type winsize struct {
	Row, Col, Xpixel, Ypixel uint16
}

// Size returns the current terminal width and height in columns and rows.
// It uses the TIOCGWINSZ ioctl on stdout. If the ioctl fails (for example,
// when stdout is piped), Size returns 80x24.
func Size() (width, height int) {
	return sizeFromFd(os.Stdout.Fd())
}

// size returns the terminal dimensions using the renderer's output file when
// available, falling back to os.Stdout. This ensures correct dimensions even
// when os.Stdout is a pipe (e.g. inside hook subprocesses).
func (r *Renderer) size() (int, int) {
	if r.outFile != nil {
		return sizeFromFd(r.outFile.Fd())
	}
	return Size()
}

func sizeFromFd(fd uintptr) (width, height int) {
	var ws winsize
	if _, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		fd,
		syscall.TIOCGWINSZ,
		uintptr(unsafe.Pointer(&ws)),
	); errno == 0 && ws.Col > 0 && ws.Row > 0 {
		return int(ws.Col), int(ws.Row)
	}
	return 80, 24
}

// Render diffs lines against the previous frame and writes the minimal
// update. Unchanged lines are skipped; only the changed range is rewritten.
//
// If a line contains [CursorMarker], the marker is stripped before output
// and the hardware cursor is placed at the marker's visible column after
// rendering.
//
// The whole update is wrapped in synchronized-output mode (CSI ?2026) so
// the terminal applies it atomically.
func (r *Renderer) Render(lines []string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	width, height := r.size()

	// Extract cursor marker.
	markerRow, markerCol := -1, -1
	clean := make([]string, len(lines))
	for i, line := range lines {
		if idx := strings.Index(line, CursorMarker); idx >= 0 {
			markerRow = i
			markerCol = visibleWidth(line[:idx])
			line = line[:idx] + line[idx+len(CursorMarker):]
		}
		clean[i] = line
	}
	lines = clean

	var buf strings.Builder
	buf.WriteString("\x1b[?25l\x1b[?2026h") // hide cursor + sync output

	sizeChanged := r.width != width || r.height != height
	if !r.inited || sizeChanged {
		r.fullRender(&buf, lines, sizeChanged)
	} else {
		r.diffRender(&buf, lines)
	}

	// Position hardware cursor at marker location.
	if markerRow >= 0 && markerRow <= len(lines)-1 {
		r.moveCursorTo(&buf, markerRow)
		if markerCol > 0 {
			fmt.Fprintf(&buf, "\x1b[%dC", markerCol)
		}
	}

	buf.WriteString("\x1b[?2026l\x1b[?25h")  // end sync + show cursor
	io.WriteString(r.writer(), buf.String()) //nolint:errcheck

	r.prev = clone(lines)
	r.width = width
	r.height = height
	r.inited = true
}

// Print writes lines to the terminal as committed scrollback content.
// The current rendered frame (if any) is erased first; the lines are
// written each followed by CRLF; renderer state is reset so the next
// [Renderer.Render] starts a fresh frame at the cursor's new position.
//
// Use Print for content that should never be re-rendered: user messages,
// finalized streamed responses, tool results, errors, log lines. Print
// is the bridge between the live frame (managed by Render) and the
// terminal's natural scrollback buffer.
func (r *Renderer) Print(lines []string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var buf strings.Builder
	buf.WriteString("\x1b[?25l\x1b[?2026h")

	// Erase the current rendered frame so it doesn't appear above the
	// printed lines.
	if r.inited && len(r.prev) > 0 {
		r.moveCursorTo(&buf, 0)
		buf.WriteString("\r\x1b[0J")
	} else {
		buf.WriteString("\r")
	}

	// Print each line followed by CRLF. Cursor ends on a fresh empty line.
	for _, line := range lines {
		buf.WriteString(line)
		buf.WriteString("\r\n")
	}

	buf.WriteString("\x1b[?2026l")
	io.WriteString(r.writer(), buf.String()) //nolint:errcheck

	// Reset state so the next Render starts a fresh frame at the new cursor.
	// Set width/height to current so the first subsequent Render does not
	// see a spurious size change and clear the screen.
	w, h := r.size()
	r.prev = nil
	r.inited = false
	r.cursorRow = 0
	r.width = w
	r.height = h
}

// PrintAndRender commits lines to scrollback and redraws the live frame in a
// single synchronized terminal update. It is useful for inline TUIs that keep
// a status/input frame below streaming output: the live frame is erased, the
// committed lines are written, and the replacement frame is painted before the
// terminal is allowed to display an intermediate state.
func (r *Renderer) PrintAndRender(lines, frame []string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	width, height := r.size()

	// Extract cursor marker from the replacement frame.
	markerRow, markerCol := -1, -1
	clean := make([]string, len(frame))
	for i, line := range frame {
		if idx := strings.Index(line, CursorMarker); idx >= 0 {
			markerRow = i
			markerCol = visibleWidth(line[:idx])
			line = line[:idx] + line[idx+len(CursorMarker):]
		}
		clean[i] = line
	}
	frame = clean

	var buf strings.Builder
	buf.WriteString("\x1b[?25l\x1b[?2026h")

	// Erase the current rendered frame so it doesn't appear above the
	// committed lines.
	if r.inited && len(r.prev) > 0 {
		r.moveCursorTo(&buf, 0)
		buf.WriteString("\r\x1b[0J")
	} else {
		buf.WriteString("\r")
	}

	for _, line := range lines {
		buf.WriteString(line)
		buf.WriteString("\r\n")
	}

	// The frame now starts at the cursor's current row after the committed
	// lines. Treat it as a fresh render, matching Print followed by Render,
	// but without exposing the erased-frame gap to the terminal.
	r.prev = nil
	r.inited = false
	r.cursorRow = 0
	r.width = width
	r.height = height
	r.fullRender(&buf, frame, false)

	if markerRow >= 0 && markerRow <= len(frame)-1 {
		r.moveCursorTo(&buf, markerRow)
		if markerCol > 0 {
			fmt.Fprintf(&buf, "\x1b[%dC", markerCol)
		}
	}

	buf.WriteString("\x1b[?2026l\x1b[?25h")
	io.WriteString(r.writer(), buf.String()) //nolint:errcheck

	r.prev = clone(frame)
	r.width = width
	r.height = height
	r.inited = true
}

// Erase wipes the currently-rendered frame from the terminal and parks the
// cursor at row 0 of where the frame began. Use Erase to dismiss a live
// widget without leaving its lines in scrollback. After Erase, the renderer
// is reset; the next [Renderer.Render] starts a fresh frame at the cursor.
func (r *Renderer) Erase() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.inited {
		return
	}
	var buf strings.Builder
	buf.WriteString("\x1b[?2026h")
	r.moveCursorTo(&buf, 0)
	buf.WriteString("\r\x1b[0J")
	buf.WriteString("\x1b[?2026l")
	io.WriteString(r.writer(), buf.String()) //nolint:errcheck
	r.prev = nil
	r.inited = false
	r.cursorRow = 0
}

// Reset clears the renderer's internal state. The next call to
// [Renderer.Render] will be treated as a first render at the current
// cursor position. Call Reset after operations that disturb the terminal
// out-of-band (resize, releasing the terminal to a child process, etc.).
// The current terminal size is sampled so the next Render does not treat
// the resize as a size change and clear the screen.
func (r *Renderer) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.prev = nil
	r.inited = false
	r.cursorRow = 0
	r.width, r.height = r.size()
}

// fullRender writes the entire frame from scratch. On a size change the
// visible screen is cleared first; scrollback above is preserved by the
// terminal.
func (r *Renderer) fullRender(buf *strings.Builder, lines []string, sizeChanged bool) {
	if sizeChanged {
		buf.WriteString("\x1b[2J\x1b[H") // clear screen, cursor home
	} else {
		buf.WriteString("\r")
	}
	for i, line := range lines {
		if i > 0 {
			buf.WriteString("\r\n")
		}
		buf.WriteString("\x1b[2K") // clear line before writing
		buf.WriteString(line)
	}
	if len(lines) > 0 {
		r.cursorRow = len(lines) - 1
	} else {
		r.cursorRow = 0
	}
}

// diffRender writes only the lines that differ from the previous frame.
// Trailing lines that existed in the previous frame but not in the new
// one are erased.
func (r *Renderer) diffRender(buf *strings.Builder, lines []string) {
	firstChanged, lastChanged := -1, -1
	n := len(lines)
	if len(r.prev) > n {
		n = len(r.prev)
	}
	for i := 0; i < n; i++ {
		var oldLine, newLine string
		if i < len(r.prev) {
			oldLine = r.prev[i]
		}
		if i < len(lines) {
			newLine = lines[i]
		}
		if oldLine != newLine {
			if firstChanged == -1 {
				firstChanged = i
			}
			lastChanged = i
		}
	}

	if firstChanged == -1 {
		return // nothing changed
	}

	// Rewrite changed lines that fall within the new frame.
	end := lastChanged
	if end > len(lines)-1 {
		end = len(lines) - 1
	}
	if end >= firstChanged {
		r.moveCursorTo(buf, firstChanged)
		for i := firstChanged; i <= end; i++ {
			if i > firstChanged {
				buf.WriteString("\r\n")
			}
			buf.WriteString("\x1b[2K")
			buf.WriteString(lines[i])
		}
		r.cursorRow = end
	}

	// Erase trailing lines that were in the old frame but not the new.
	if len(r.prev) > len(lines) {
		extra := len(r.prev) - len(lines)
		r.moveCursorTo(buf, len(lines)-1)
		for i := 0; i < extra; i++ {
			buf.WriteString("\r\n\x1b[2K")
			r.cursorRow++
		}
		r.moveCursorTo(buf, len(lines)-1)
	}
}

// moveCursorTo emits the ANSI sequence to move the cursor to column 0 of
// the given row, where row is relative to the frame's row 0. The trailing
// carriage return guarantees the column is 0 when this returns.
func (r *Renderer) moveCursorTo(buf *strings.Builder, row int) {
	diff := row - r.cursorRow
	switch {
	case diff > 0:
		fmt.Fprintf(buf, "\x1b[%dB", diff)
	case diff < 0:
		fmt.Fprintf(buf, "\x1b[%dA", -diff)
	}
	buf.WriteString("\r")
	r.cursorRow = row
}

// visibleWidth returns the number of visible columns occupied by s.
// Delegates to VisibleLen for correct grapheme-cluster and wide-char handling.
func visibleWidth(s string) int {
	return VisibleLen(s)
}

// clone returns a shallow copy of s.
func clone(s []string) []string {
	c := make([]string, len(s))
	copy(c, s)
	return c
}
