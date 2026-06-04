package patchwidgets

import (
	"errors"
	"io"
	"testing"
)

// chunkReader returns each chunk on successive Read calls, then io.EOF.
type chunkReader struct {
	chunks [][]byte
	i      int
}

func (r *chunkReader) Read(p []byte) (int, error) {
	if r.i >= len(r.chunks) {
		return 0, io.EOF
	}
	n := copy(p, r.chunks[r.i])
	r.i++
	return n, nil
}

// errReader returns a fixed non-EOF error on the first Read.
type errReader struct{ err error }

func (r *errReader) Read(p []byte) (int, error) { return 0, r.err }

// F-HH: a single read carrying two keys must surface BOTH keys across two
// readKey calls; the second must not be dropped.
func TestReadKeyBuffersCoalescedKeys(t *testing.T) {
	tt := &tty{reader: &chunkReader{chunks: [][]byte{[]byte("ab")}}}

	k1, err := tt.readKey(0)
	if err != nil {
		t.Fatalf("first readKey err: %v", err)
	}
	if k1.kind != keyChar || k1.r != 'a' {
		t.Fatalf("first key: got %+v want char 'a'", k1)
	}

	k2, err := tt.readKey(0)
	if err != nil {
		t.Fatalf("second readKey err: %v", err)
	}
	if k2.kind != keyChar || k2.r != 'b' {
		t.Fatalf("second key dropped/wrong: got %+v want char 'b'", k2)
	}
}

// F-HH: a CSI arrow sequence immediately followed by a char in one read must
// yield the arrow, then the char.
func TestReadKeyBuffersSequenceThenChar(t *testing.T) {
	// ESC [ A  (Up) followed by 'x'.
	in := append([]byte{27, '[', 'A'}, 'x')
	tt := &tty{reader: &chunkReader{chunks: [][]byte{in}}}

	k1, err := tt.readKey(0)
	if err != nil {
		t.Fatalf("first readKey err: %v", err)
	}
	if k1.kind != keyUp {
		t.Fatalf("first key: got %+v want keyUp", k1)
	}

	k2, err := tt.readKey(0)
	if err != nil {
		t.Fatalf("second readKey err: %v", err)
	}
	if k2.kind != keyChar || k2.r != 'x' {
		t.Fatalf("second key dropped/wrong: got %+v want char 'x'", k2)
	}
}

// F-FF: a genuine io.EOF must still yield keyEOF + io.EOF (normal dismissal).
func TestReadKeyEOF(t *testing.T) {
	tt := &tty{reader: &chunkReader{}} // no chunks -> immediate EOF
	k, err := tt.readKey(0)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("err: got %v want io.EOF", err)
	}
	if k.kind != keyEOF {
		t.Fatalf("kind: got %+v want keyEOF", k)
	}
}

// F-FF: a non-EOF read error must be returned to the caller, not swallowed.
func TestReadKeyNonEOFError(t *testing.T) {
	boom := errors.New("dev/tty boom")
	tt := &tty{reader: &errReader{err: boom}}
	_, err := tt.readKey(0)
	if !errors.Is(err, boom) {
		t.Fatalf("err: got %v want wrapping of boom", err)
	}
	if errors.Is(err, io.EOF) {
		t.Fatalf("non-EOF error misclassified as EOF: %v", err)
	}
}
