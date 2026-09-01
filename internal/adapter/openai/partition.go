package openai

import "strings"

// tagPartitioner splits streamed <think> blocks from answers across chunk
// boundaries. Tags inside Markdown code remain visible content.
type tagPartitioner struct {
	// pending is a trailing fragment that may still become a tag or fence
	// marker once more arrives: "<thi", "``".
	pending string
	inThink bool
	inFence bool // inside a ``` fenced block
	inCode  bool // inside a single-backtick span
	// started makes the leading-whitespace trim cover a reply that opens with
	// newlines before any think block at all.
	started bool
	// strict latches once the stream is seen to carry a native reasoning
	// field. From then on, text inside a <think> block in `content` duplicates
	// reasoning already delivered — possibly on an EARLIER chunk, which is why
	// this is stream-level state, not a per-delta comparison — so it is
	// dropped. The tags are still tracked: they keep the duplicate out of the
	// answer.
	strict bool
}

const (
	thinkOpen  = "<think>"
	thinkClose = "</think>"
	fence      = "```"
)

// pushDelta consumes one content delta with the reasoning fragment that
// arrived beside it, returning what to show and what to file as reasoning.
//
// The two signals combine, they do not chain. The delta whose content
// duplicates the reasoning field is usually the one carrying the opening
// "<think>", so dropping it wholesale — as a dedup pass before the scanner
// would — blinds the scanner to the block it needed, and the rest of the
// chain-of-thought, arriving with no reasoning field, sails through as answer.
// So: always scan for tags, and let the duplicate check decide only routing.
func (p *tagPartitioner) pushDelta(content, reasoning string) (visible, reasoningOut string) {
	if reasoning != "" {
		p.strict = true
	}
	dup := reasoning != "" && visibleContent(content, reasoning) == ""
	vis, rea := p.push(content)
	if dup {
		// Duplicated reasoning: the scan's real job was updating the tag
		// state, and both halves of its output are dropped. The caller emits
		// the reasoning FIELD for this delta, so returning the scanned
		// copy too would file the same thought twice.
		return "", ""
	}
	return vis, rea
}

// push consumes one content delta and returns the text to show the user and
// the text to file as reasoning.
func (p *tagPartitioner) push(chunk string) (visible, reasoning string) {
	var vis, rea strings.Builder
	s := p.pending + chunk
	p.pending = ""

	for len(s) > 0 {
		// Inside a fence or code span nothing is a tag: consume to the closer,
		// holding back a partial closer for the next delta.
		if p.inFence || p.inCode {
			closer := fence
			if p.inCode {
				closer = "`"
			}
			i := strings.Index(s, closer)
			if i < 0 {
				if held := heldSuffix(s, closer); held != "" {
					p.emit(&vis, &rea, s[:len(s)-len(held)])
					p.pending = held
					return vis.String(), rea.String()
				}
				p.emit(&vis, &rea, s)
				return vis.String(), rea.String()
			}
			p.emit(&vis, &rea, s[:i+len(closer)])
			s = s[i+len(closer):]
			p.inFence, p.inCode = false, false
			continue
		}

		next, kind := nextMarker(s)
		if next < 0 {
			// No marker in this delta; hold back anything that could still
			// become one so a tag split across deltas is not missed.
			if held := heldSuffix(s, thinkOpen, thinkClose, fence, "`"); held != "" {
				p.emit(&vis, &rea, s[:len(s)-len(held)])
				p.pending = held
				return vis.String(), rea.String()
			}
			p.emit(&vis, &rea, s)
			return vis.String(), rea.String()
		}
		p.emit(&vis, &rea, s[:next])
		switch kind {
		case thinkOpen:
			p.inThink = true
			s = s[next+len(thinkOpen):]
		case thinkClose:
			p.inThink = false
			s = s[next+len(thinkClose):]
		case fence:
			p.inFence = true
			p.emit(&vis, &rea, fence)
			s = s[next+len(fence):]
		default: // single backtick
			p.inCode = true
			p.emit(&vis, &rea, "`")
			s = s[next+1:]
		}
	}
	return vis.String(), rea.String()
}

// flush releases anything still held at end of stream. Text held inside an
// unclosed <think> is reasoning; anything else is answer text — a partial tag
// that never completed was ordinary prose all along.
func (p *tagPartitioner) flush() (visible, reasoning string) {
	s := p.pending
	p.pending = ""
	if s == "" {
		return "", ""
	}
	if p.inThink {
		if p.strict {
			return "", ""
		}
		return "", s
	}
	return s, ""
}

// emit routes a run of plain text to the answer or the reasoning side.
func (p *tagPartitioner) emit(vis, rea *strings.Builder, s string) {
	if s == "" {
		return
	}
	if p.inThink {
		if !p.strict {
			rea.WriteString(s)
		}
		return
	}
	if !p.started {
		// Swallow whitespace ahead of the first real answer rune — either the
		// gap a closing </think> left behind, or a reply that simply opens with
		// newlines. Once real text lands, whitespace is content again.
		if s = strings.TrimLeft(s, " \t\r\n"); s == "" {
			return
		}
		p.started = true
	}
	vis.WriteString(s)
}

// nextMarker finds the earliest of the tag/fence markers in s, returning its
// index and which one it was (-1 when none is present). A fence is checked
// before a bare backtick so ``` is never read as an empty code span.
func nextMarker(s string) (int, string) {
	best, kind := -1, ""
	for _, m := range []string{thinkOpen, thinkClose, fence} {
		if i := strings.Index(s, m); i >= 0 && (best < 0 || i < best) {
			best, kind = i, m
		}
	}
	if i := strings.Index(s, "`"); i >= 0 && (best < 0 || i < best) {
		// Only a lone backtick — a fence at the same spot already won above.
		if !strings.HasPrefix(s[i:], fence) {
			best, kind = i, "`"
		}
	}
	return best, kind
}

// heldSuffix returns the trailing part of s that is a proper prefix of any
// marker, so it can be carried into the next delta instead of being emitted
// and losing the split. Returns "" when nothing needs holding.
func heldSuffix(s string, markers ...string) string {
	for _, m := range markers {
		for n := len(m) - 1; n > 0; n-- {
			if n <= len(s) && strings.HasSuffix(s, m[:n]) {
				return s[len(s)-n:]
			}
		}
	}
	return ""
}
