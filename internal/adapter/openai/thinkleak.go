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
}

const (
	leakHolding = iota // start of stream: buffer while a leading tag is still possible
	leakTrimming       // tag consumed: swallow whitespace up to the first real rune
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
