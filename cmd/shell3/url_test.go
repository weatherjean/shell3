//go:build unix

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, dir, webBlock string) {
	t.Helper()
	yaml := "models:\n  main:\n    base_url: \"http://x/v1\"\n\nweb:\n" + webBlock
	if err := os.WriteFile(filepath.Join(dir, "shell3.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestResolvePublicURL covers the precedence: fixed web.url beats the tunnel
// file, the tunnel file beats the local address, and base_url must never be
// mistaken for web.url.
func TestResolvePublicURL(t *testing.T) {
	dir := t.TempDir()

	writeConfig(t, dir, "  addr: 127.0.0.1:9999\n")
	u, note, err := resolvePublicURL(dir)
	if err != nil || u != "http://127.0.0.1:9999" || note != "" {
		t.Errorf("local-only: got %q %q %v", u, note, err)
	}

	writeConfig(t, dir, "  addr: 127.0.0.1:9999\n  tunnel: \"cloudflared tunnel --url http://{addr}\"\n")
	u, note, _ = resolvePublicURL(dir)
	if u != "http://127.0.0.1:9999" || note == "" {
		t.Errorf("tunnel wired, no file: got %q, want local addr with a caveat", u)
	}

	if err := os.WriteFile(filepath.Join(dir, "tunnel.url"), []byte("https://abc.trycloudflare.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	u, note, _ = resolvePublicURL(dir)
	if u != "https://abc.trycloudflare.com" || !strings.Contains(note, "last tunnel start") {
		t.Errorf("tunnel file: got %q %q", u, note)
	}

	writeConfig(t, dir, "  addr: 127.0.0.1:9999\n  url: \"https://shell3.example.com\"\n")
	u, note, _ = resolvePublicURL(dir)
	if u != "https://shell3.example.com" || note != "" {
		t.Errorf("fixed url: got %q %q", u, note)
	}

	if _, _, err := resolvePublicURL(t.TempDir()); err == nil {
		t.Error("missing config should error")
	}
}
