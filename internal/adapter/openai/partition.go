package openai

import "strings"

// tagPartitioner splits a streamed content channel into answer text and
// reasoning text by tracking <think>…</think> wrappers, incrementally and
// across arbitrary chunk boundaries.
//
// It exists because MiniMax does not reliably keep reasoning out of `content`.
// The cheap rule in visibleContent catches the common shape (a content delta
// byte-identical to its sibling reasoning field), but when the model's own
// output quotes think-tags the provider's server-side de-duplication breaks
// down and whole paragraphs of chain-of-thought arrive in `content` with NO
// reasoning field beside them, wrapped in the tags. Only a tag scanner can
// separate those.
//
// The hard part is that text ABOUT think-tags must survive: a reply explaining
// `<think>` inside backticks, or a fenced code block containing one, is answer
// text and not a delimiter. So the scanner tracks Markdown code context and
// ignores any tag inside it — the same rule openclaw's reasoning-tag
// partitioner uses (isInsideCode). Without it, asking the agent to explain a
// regex silently truncates its reply at the first quoted tag.
type tagPartitioner struct {
	// pending holds a trailing fragment that might still become a tag or a
	// fence marker once more of the stream arrives ("<thi", "``").
	pending string
	inThink bool
	inFence bool // inside a ``` fenced block
	inCode  bool // inside a single-backtick span
	// started records whether any answer text has been emitted yet, so the
	// leading-whitespace trim also covers a reply that opens with newlines
	// before any think block at all.
	started bool
	// strict is latched once the stream is seen to carry a native reasoning
	// field. From then on, text found inside a <think> block in `content` is
	// known to be a duplicate of reasoning already delivered through that
	// field — possibly on an EARLIER chunk, which is why this is stream-level
	// state and not a per-delta comparison — so it is dropped rather than
	// filed a second time. The tags are still tracked: they are what keeps the
	// duplicate out of the answer. (openclaw calls this markStrict.)
	strict bool
}

// markStrict records that this stream delivers reasoning through a native
// field, so reasoning recovered from content tags is redundant.
func (p *tagPartitioner) markStrict() { p.strict = true }

const (
	thinkOpen  = "<think>"
	thinkClose = "</think>"
	fence      = "```"
)

// pushDelta consumes one content delta together with the reasoning fragment
// that arrived on the same delta, and returns the text to show the user and the
// text to file as reasoning.
//
// The two signals must be combined, not chained. The delta whose content
// duplicates the reasoning field is usually the very one carrying the opening
// "<think>" wrapper, so dropping it wholesale — as a dedup pass running before
// the scanner would — leaves the scanner blind to the block it needed to see,
// and the rest of the chain-of-thought (which arrives with no reasoning field
// beside it) sails through as answer text. So: always scan for tags to keep the
// state machine honest, and use the duplicate check only to decide where this
// delta's text is routed.
func (p *tagPartitioner) pushDelta(content, reasoning string) (visible, reasoningOut string) {
	if reasoning != "" {
		p.markStrict()
	}
	dup := reasoning != "" && visibleContent(content, reasoning) == ""
	vis, rea := p.push(content)
	if dup {
		// Duplicated reasoning: the scan has done its real job (updating the
		// tag state), and both halves of its output are dropped. The caller
		// emits the reasoning FIELD for this delta, so returning the scanned
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
