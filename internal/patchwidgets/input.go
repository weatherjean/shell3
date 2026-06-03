package patchwidgets

// keyKind enumerates the input events that widgets care about. Subset of
// the patchapp parser; no bracketed paste handling, but adds keyTimeout
// and keyEOF for read-deadline / closed-tty signalling.
type keyKind int

const (
	keyNone keyKind = iota
	keyChar
	keyEnter
	keyBackspace
	keyEscape
	keyLeft
	keyRight
	keyUp
	keyDown
	keyHome
	keyEnd
	keyTab
	keyShiftTab
	keyCtrlC
	keyCtrlU // clear input line
	keyCtrlW // delete previous word
	keyTimeout
	keyEOF
)

// parsedKey is one decoded input event. r is set only for keyChar.
type parsedKey struct {
	kind keyKind
	r    rune
}

// parseKey decodes one key from the head of data. Returns the parsed key
// and the number of bytes consumed. Unknown sequences yield keyNone with
// consumed=1 so callers advance and try again.
func parseKey(data []byte) (parsedKey, int) {
	if len(data) == 0 {
		return parsedKey{kind: keyNone}, 0
	}
	b := data[0]

	switch b {
	case 3:
		return parsedKey{kind: keyCtrlC}, 1
	case 9:
		return parsedKey{kind: keyTab}, 1
	case 10, 13:
		return parsedKey{kind: keyEnter}, 1
	case 21:
		return parsedKey{kind: keyCtrlU}, 1
	case 23:
		return parsedKey{kind: keyCtrlW}, 1
	case 8, 127:
		return parsedKey{kind: keyBackspace}, 1
	case 27:
		if len(data) == 1 {
			return parsedKey{kind: keyEscape}, 1
		}
		// CSI: ESC [ ...
		if data[1] == '[' && len(data) >= 3 {
			switch data[2] {
			case 'A':
				return parsedKey{kind: keyUp}, 3
			case 'B':
				return parsedKey{kind: keyDown}, 3
			case 'C':
				return parsedKey{kind: keyRight}, 3
			case 'D':
				return parsedKey{kind: keyLeft}, 3
			case 'H':
				return parsedKey{kind: keyHome}, 3
			case 'F':
				return parsedKey{kind: keyEnd}, 3
			case 'Z':
				return parsedKey{kind: keyShiftTab}, 3
			}
			// ESC [ N ~ forms — Home/End/Delete on some terminals.
			for j := 3; j < len(data) && j < 8; j++ {
				if data[j] == '~' {
					switch data[2] {
					case '1', '7':
						return parsedKey{kind: keyHome}, j + 1
					case '4', '8':
						return parsedKey{kind: keyEnd}, j + 1
					}
					return parsedKey{kind: keyNone}, j + 1
				}
				if data[j] < '0' || data[j] > '9' {
					break
				}
			}
		}
		return parsedKey{kind: keyEscape}, 1
	}

	// Printable ASCII.
	if b >= 32 && b < 127 {
		return parsedKey{kind: keyChar, r: rune(b)}, 1
	}

	// UTF-8 multi-byte: greedy take up to 4 bytes and decode.
	if b >= 0x80 {
		n := utf8Len(b)
		if n > 0 && n <= len(data) {
			r := utf8Decode(data[:n])
			if r > 0 {
				return parsedKey{kind: keyChar, r: r}, n
			}
		}
	}
	return parsedKey{kind: keyNone}, 1
}

func utf8Len(b byte) int {
	switch {
	case b&0xE0 == 0xC0:
		return 2
	case b&0xF0 == 0xE0:
		return 3
	case b&0xF8 == 0xF0:
		return 4
	}
	return 0
}

func utf8Decode(b []byte) rune {
	switch len(b) {
	case 2:
		return rune(b[0]&0x1F)<<6 | rune(b[1]&0x3F)
	case 3:
		return rune(b[0]&0x0F)<<12 | rune(b[1]&0x3F)<<6 | rune(b[2]&0x3F)
	case 4:
		return rune(b[0]&0x07)<<18 | rune(b[1]&0x3F)<<12 | rune(b[2]&0x3F)<<6 | rune(b[3]&0x3F)
	}
	return 0
}

// trimToWord backs up over trailing whitespace, then over the trailing
// non-whitespace word. Used by Ctrl+W to delete the previous word.
func trimToWord(s []rune) []rune {
	i := len(s)
	for i > 0 && (s[i-1] == ' ' || s[i-1] == '\t') {
		i--
	}
	for i > 0 && s[i-1] != ' ' && s[i-1] != '\t' {
		i--
	}
	return s[:i]
}
