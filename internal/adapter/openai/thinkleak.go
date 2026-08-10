package openai

import "strings"

// Some OpenAI-compatible providers (MiniMax) serve reasoning natively wrapped
// in <think>…</think> and split it into the reasoning_content delta field, but
// leak the trailing "</think>" delimiter into the start of the content stream
// — typically as the entire content of a tool-call turn. thinkLeakFilter
// suppresses exactly that: a "</think>" (with optional surrounding whitespace)
// at the very start of a message's content, arriving whole or split across
// deltas. Everything else — including a literal "</think>" later in the
// message — passes through untouched. Legitimate content that *starts* with
// "</think>" is indistinguishable from the leak and is dropped; accepted.
type thinkLeakFilter struct {
	state int // holding → trimming → passthrough
	buf   string
	// reasoningSeen widens the net: when the stream also carried a split
	// reasoning field, a "</think>" near the very start of content is the
	// provider's reasoning tail bleeding over (observed live as
	// "state.</think>" opening a message), so everything through the tag is
	// dropped. Without a reasoning stream only the bare leading tag matches —
	// a message that legitimately MENTIONS the tag early stays intact.
	reasoningSeen bool
}

// leakScanCap is how far (bytes, after leading whitespace) into the content a
// glued "</think>" still counts as reasoning bleed-over when reasoningSeen.
const leakScanCap = 64

const (
	leakHolding  = iota // start of stream: buffer while a leading tag is still possible
	leakTrimming        // tag consumed: swallow whitespace up to the first real rune
	leakPassthrough
)

const leakedTag = "</think>"

// filter processes one content delta and returns the text to emit now
// (possibly empty while the filter is still deciding).
func (f *thinkLeakFilter) filter(delta string) string {
	switch f.state {
	case leakPassthrough:
		return delta
	case leakTrimming:
		s := strings.TrimLeft(delta, " \t\r\n")
		if s == "" {
			return ""
		}
		f.state = leakPassthrough
		return s
	}
	f.buf += delta
	trimmed := strings.TrimLeft(f.buf, " \t\r\n")
	if f.reasoningSeen {
		// Wider net: scan the opening window for the tag anywhere, not just
		// at position zero.
		if i := strings.Index(trimmed, leakedTag); i >= 0 && i <= leakScanCap {
			f.state = leakTrimming
			f.buf = ""
			return f.filter(trimmed[i+len(leakedTag):])
		}
		if len(trimmed) > leakScanCap+len(leakedTag) {
			f.state = leakPassthrough
			out := f.buf
			f.buf = ""
			return out
		}
		return "" // window not exhausted — keep holding (flush releases at EOS)
	}
	switch {
	case strings.HasPrefix(trimmed, leakedTag):
		f.state = leakTrimming
		f.buf = ""
		return f.filter(trimmed[len(leakedTag):])
	case strings.HasPrefix(leakedTag, trimmed): // still a viable prefix (or all whitespace)
		return ""
	default:
		f.state = leakPassthrough
		out := f.buf
		f.buf = ""
		return out
	}
}

// flush releases anything still held at end of stream (e.g. a message that was
// only ever a partial prefix like "</thi").
func (f *thinkLeakFilter) flush() string {
	out := ""
	if f.state == leakHolding {
		out = f.buf
	}
	f.state = leakPassthrough
	f.buf = ""
	return out
}
