package strutil

import (
	"bytes"
	"strings"
	"testing"
)

func TestCaptureBoundsLargeWrites(t *testing.T) {
	c := Capture{Limit: 100}
	payload := []byte("head" + strings.Repeat("x", 1<<20) + "tail")
	for range 10 {
		if n, err := c.Write(payload); n != len(payload) || err != nil {
			t.Fatalf("write = %d, %v", n, err)
		}
	}
	if len(c.head)+len(c.tail) > c.Limit || cap(c.head)+cap(c.tail) > 2*c.Limit {
		t.Fatal("capture retained oversized buffers")
	}
	if c.Len() != 10*len(payload) || !strings.HasPrefix(c.String(), "head") || !strings.HasSuffix(c.String(), "tail") {
		t.Fatalf("unexpected capture: %q (%d bytes)", c.String(), c.Len())
	}
}

func TestCaptureChunkBoundaries(t *testing.T) {
	for _, size := range []int{0, 3, 10, 11, 100} {
		payload := bytes.Repeat([]byte("abcd"), size)
		whole := Capture{Limit: 20}
		_, _ = whole.Write(payload)
		chunked := Capture{Limit: 20}
		for _, b := range payload {
			_, _ = chunked.Write([]byte{b})
		}
		if whole.String() != chunked.String() {
			t.Fatalf("size %d: whole %q, chunked %q", size, whole.String(), chunked.String())
		}
		if len(payload) <= 20 && whole.String() != string(payload) {
			t.Fatalf("short output changed: %q", whole.String())
		}
	}
}
