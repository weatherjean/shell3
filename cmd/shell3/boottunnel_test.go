//go:build unix

package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"strings"
	"testing"
)

func TestCloudflaredAssetURL(t *testing.T) {
	// The current platform must resolve to an official release asset (the
	// build matrix covers every platform shell3 itself supports).
	url, _, err := cloudflaredAssetURL()
	if err != nil {
		t.Fatalf("no asset for the current platform: %v", err)
	}
	if !strings.HasPrefix(url, "https://github.com/cloudflare/cloudflared/releases/") {
		t.Fatalf("unexpected asset url %q", url)
	}
}

func TestCloudflaredFromTgz(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range map[string]string{"README.md": "hi", "cloudflared": "BINARY"} {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	tw.Close()
	gz.Close()

	r, err := cloudflaredFromTgz(&buf)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(r)
	if string(got) != "BINARY" {
		t.Fatalf("extracted %q, want the cloudflared entry", got)
	}
}
