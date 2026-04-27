package config

import (
	"bytes"
	"strings"
	"testing"
)

func TestWrapUnwrap_RoundTrip(t *testing.T) {
	plain := []byte("version: 1\ninstances:\n  openai:\n    adapter: openai\n    fields:\n      api_key: sk-test\n")
	wrapped := Wrap(plain)
	if !bytes.HasPrefix(wrapped, []byte(obfuscateHeader)) {
		t.Fatalf("missing magic header: %q", wrapped[:minInt(len(wrapped), 64)])
	}
	if bytes.Contains(wrapped, []byte("sk-test")) {
		t.Fatalf("wrapped contained plaintext secret")
	}
	got, err := Unwrap(wrapped)
	if err != nil {
		t.Fatalf("Unwrap: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("round-trip mismatch:\n got:  %q\n want: %q", got, plain)
	}
}

func TestUnwrap_RejectsMissingHeader(t *testing.T) {
	_, err := Unwrap([]byte("not the magic header\nABCDEF=="))
	if err == nil || !strings.Contains(err.Error(), "header") {
		t.Fatalf("want header error, got %v", err)
	}
}

func TestUnwrap_RejectsCorruptBase64(t *testing.T) {
	bad := []byte(obfuscateHeader + "\n!!!not-base64!!!")
	if _, err := Unwrap(bad); err == nil {
		t.Fatalf("want decode error, got nil")
	}
}

func TestWrap_Empty(t *testing.T) {
	w := Wrap(nil)
	got, err := Unwrap(w)
	if err != nil {
		t.Fatalf("Unwrap: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want empty, got %q", got)
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
