package askui

// uiState is a deterministic, ANSI-free snapshot of the model's user-visible
// state — designed for "drive → snapshot → inspect" assertions in tests with no
// PTY. Unexported is fine since tests live in-package.
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

	busy      bool // a turn is in flight
	quitArmed bool // ctrl+c pressed once with no turn running

	follow bool // viewport is locked to the bottom as content streams in

	footer     []string // plain-text footer segments, left group then right group
	blockCount int      // number of transcript blocks
}

// uiSnapshot captures the model's current render-relevant state. Call it after
// Update (or View, to force layout) to assert on what the user would see,
// without scraping styled/ANSI text.
func (m *model) uiSnapshot() uiState {
	left, right := m.buildFooter()
	footer := append(plainSegs(left), plainSegs(right)...)
	return uiState{
		input:      m.ta.Value(),
		notice:     m.activeNotice(),
		busy:       m.busy,
		quitArmed:  m.quitArmed,
		follow:     m.follow,
		footer:     footer,
		blockCount: len(m.blockStarts),
	}
}
