package askui

// uiState is a deterministic, ANSI-free snapshot of render-relevant state.
type uiState struct {
	input      string
	notice     string
	busy       bool
	quitArmed  bool
	follow     bool
	footer     []string
	blockCount int
}

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

func plainSegs(segs []footerSeg) []string {
	out := make([]string, len(segs))
	for i, s := range segs {
		out[i] = s.plain
	}
	return out
}
