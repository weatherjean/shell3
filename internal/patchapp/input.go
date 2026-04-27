package patchapp

import (
	"bytes"
	"unicode/utf8"
)

// Bracketed paste mode escape sequences. Enabling this asks the terminal
// to wrap pasted content in start/end markers so we can distinguish it
// from typed input.
const (
	pasteOn    = "\x1b[?2004h"
	pasteOff   = "\x1b[?2004l"
	pasteStart = "\x1b[200~"
	pasteEnd   = "\x1b[201~"
)

// keyKind enumerates the high-level input categories the App handles.
type keyKind int

const (
	keyNone keyKind = iota
	keyChar
	keyEnter
	keyAltEnter
	keyBackspace
	keyEscape
	keyLeft
	keyRight
	keyUp
	keyDown
	keyHome
	keyEnd
	keyCtrlC
	keyPasteStart
	keyPasteEnd
)

// parsedKey is one decoded input event. Only the fields relevant to its
// kind are populated.
type parsedKey struct {
	kind keyKind
	r    rune // populated for keyChar
}

// parseInput consumes bytes from data and returns one decoded key plus the
// number of bytes it used. If no complete key is recognised it returns
// keyNone with consumed=1 so the caller advances and tries again.
//
// parseInput is stateless. Bracketed paste body bytes between paste
// boundaries are returned as keyChar; the caller tracks the paste flag
// using the keyPasteStart / keyPasteEnd events.
func parseInput(data []byte) (parsedKey, int) {
	if len(data) == 0 {
		return parsedKey{kind: keyNone}, 0
	}

	// Bracketed paste boundaries. If a read ends mid-sequence, ask the caller
	// to retain the partial bytes for the next read.
	if bytes.HasPrefix(data, []byte(pasteStart)) {
		return parsedKey{kind: keyPasteStart}, len(pasteStart)
	}
	if len(data) > 1 && bytes.HasPrefix([]byte(pasteStart), data) {
		return parsedKey{kind: keyNone}, 0
	}
	if bytes.HasPrefix(data, []byte(pasteEnd)) {
		return parsedKey{kind: keyPasteEnd}, len(pasteEnd)
	}
	if len(data) > 1 && bytes.HasPrefix([]byte(pasteEnd), data) {
		return parsedKey{kind: keyNone}, 0
	}

	b := data[0]

	if b == 3 {
		return parsedKey{kind: keyCtrlC}, 1
	}
	if b == 13 || b == 10 {
		return parsedKey{kind: keyEnter}, 1
	}
	if b == 127 || b == 8 {
		return parsedKey{kind: keyBackspace}, 1
	}

	// Esc or Esc-prefixed sequence.
	if b == 27 {
		if len(data) == 1 {
			return parsedKey{kind: keyEscape}, 1
		}
		if data[1] == 13 || data[1] == 10 {
			return parsedKey{kind: keyAltEnter}, 2
		}
		// CSI: ESC [ ...
		if len(data) >= 3 && data[1] == '[' {
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
			}
			// \x1b[N~ forms (Home/End/Delete on some terminals).
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
		return parsedKey{kind: keyNone}, 1
	}

	// Printable ASCII.
	if b >= 32 && b < 127 {
		return parsedKey{kind: keyChar, r: rune(b)}, 1
	}

	// Printable UTF-8. Keep the TUI input buffer as runes, not raw bytes;
	// otherwise pasted punctuation is re-encoded as mojibake when echoed or
	// submitted (for example, the UTF-8 bytes for an em dash become Latin-1-
	// style replacement text).
	if b >= utf8.RuneSelf {
		if !utf8.FullRune(data) {
			return parsedKey{kind: keyNone}, 0
		}
		r, size := utf8.DecodeRune(data)
		if r != utf8.RuneError || size > 1 {
			return parsedKey{kind: keyChar, r: r}, size
		}
	}

	return parsedKey{kind: keyNone}, 1
}
