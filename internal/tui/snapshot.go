package tui

// uiState is a deterministic, ANSI-free snapshot of the model's user-visible
// state — designed for "render → snapshot → inspect" assertions in tests with
// no PTY, and shaped so it could later back a runtime dump (e.g. a debug
// command) without redesign. Unexported is fine since tests live in-package.
//
// Determinism: nothing here depends on wall-clock beyond what the feature
// itself already treats as a stable fact for a given instant (e.g. notice is
// activeNotice(), the same TTL-gated text the footer shows — not the raw
// m.notice/m.noticeAt pair). Frame-local animation state that has no
// user-facing meaning on its own — the spinner's rotation counter, the cursor
// blink phase — is deliberately excluded, so two snapshots taken back-to-back
// with identical input are byte-for-byte equal.
type uiState struct {
	input  string // textarea content
	notice string // the active last-action notice, "" if none/faded

	busy bool // a turn is in flight

	scrollY int  // viewport Y-offset
	follow  bool // viewport is locked to the bottom as content streams in

	modal    modalKind // which overlay (if any) owns the screen; see modal.go
	modalSel int       // salient selection within the open modal (bg job row,
	// confirm's Yes=0/No=1, the highlighted palette row); -1 when the open modal
	// (or none) has no selection
	paletteQuery string // the ctrl+p palette's typed filter/input text, "" when closed

	footer     []string // plain-text footer segments, left-to-right then right-to-left groups
	blockCount int      // number of transcript blocks (m.blockStarts)
}

// uiSnapshot captures the model's current render-relevant state. Call it after
// Update (or View, to force layout) to assert on what the user would see,
// without scraping styled/ANSI text.
func (m *model) uiSnapshot() uiState {
	left, right := m.buildFooter()
	footer := append(plainSegs(left), plainSegs(right)...)
	return uiState{
		input:        m.ta.Value(),
		notice:       m.activeNotice(),
		busy:         m.busy,
		scrollY:      m.vp.YOffset(),
		follow:       m.follow,
		modal:        m.currentModal(),
		modalSel:     m.modalSelection(),
		paletteQuery: m.palette.query,
		footer:       footer,
		blockCount:   len(m.blockStarts),
	}
}
