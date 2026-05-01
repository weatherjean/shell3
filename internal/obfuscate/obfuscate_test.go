package obfuscate_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/obfuscate"
)

func TestWrapUnwrap_RoundTrip(t *testing.T) {
	plain := []byte("version: 1\ninstances:\n  openai:\n    adapter: openai\n    fields:\n      api_key: sk-test\n")
	wrapped := obfuscate.Wrap(plain)
	if !bytes.HasPrefix(wrapped, []byte(obfuscate.Header)) {
		t.Fatalf("missing magic header: %q", wrapped[:min(len(wrapped), 64)])
	}
	if bytes.Contains(wrapped, []byte("sk-test")) {
		t.Fatalf("wrapped contained plaintext secret")
	}
	got, err := obfuscate.Unwrap(wrapped)
	if err != nil {
		t.Fatalf("Unwrap: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("round-trip mismatch:\n got:  %q\n want: %q", got, plain)
	}
}

func TestUnwrap_RejectsMissingHeader(t *testing.T) {
	_, err := obfuscate.Unwrap([]byte("not the magic header\nABCDEF=="))
	if err == nil || !strings.Contains(err.Error(), "header") {
		t.Fatalf("want header error, got %v", err)
	}
}

func TestUnwrap_RejectsCorruptBase64(t *testing.T) {
	bad := []byte(obfuscate.Header + "\n!!!not-base64!!!")
	if _, err := obfuscate.Unwrap(bad); err == nil {
		t.Fatalf("want decode error, got nil")
	}
}

func TestWrap_Empty(t *testing.T) {
	w := obfuscate.Wrap(nil)
	got, err := obfuscate.Unwrap(w)
	if err != nil {
		t.Fatalf("Unwrap: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want empty, got %q", got)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
