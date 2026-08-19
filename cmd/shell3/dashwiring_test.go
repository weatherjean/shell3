//go:build unix

package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/dash"
)

func startTestDash(t *testing.T) *dash.Server {
	t.Helper()
	srv := dash.New(0, dash.Sources{
		RunsRoot:  t.TempDir(),
		IndexHTML: func(string) string { return "<p>hi</p>" },
	}, applog.Noop{})
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	return srv
}

// dashMintURL prefers the base URL from dash_url.txt (the exposure agent's
// hand-off), falls back to the listener's own address when the file is
// missing or junk, and always appends a fresh valid token.
func TestDashMintURL(t *testing.T) {
	srv := startTestDash(t)
	dir := t.TempDir()
	urlFile := filepath.Join(dir, dashURLFileName)

	// No file: localhost fallback, and the minted URL actually opens.
	u := dashMintURL(urlFile, srv)
	if !strings.HasPrefix(u, "http://"+srv.Addr()+"/?t=") {
		t.Fatalf("fallback URL = %q", u)
	}
	resp, err := http.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("minted URL: code %d, want 200", resp.StatusCode)
	}

	// Exposure agent wrote a public base: it wins, trailing slash trimmed.
	if err := os.WriteFile(urlFile, []byte("https://snail.example.ts.net/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	u = dashMintURL(urlFile, srv)
	if !strings.HasPrefix(u, "https://snail.example.ts.net/?t=") {
		t.Fatalf("file base URL = %q", u)
	}

	// Junk content: fall back rather than hand out a broken link.
	if err := os.WriteFile(urlFile, []byte("not a url"), 0o644); err != nil {
		t.Fatal(err)
	}
	if u = dashMintURL(urlFile, srv); !strings.HasPrefix(u, "http://"+srv.Addr()+"/?t=") {
		t.Fatalf("junk-file URL = %q", u)
	}
}

func TestIsHTTPURL(t *testing.T) {
	for _, ok := range []string{"http://1.2.3.4:7333", "https://x.example/dash"} {
		if !isHTTPURL(ok) {
			t.Errorf("isHTTPURL(%q) = false", ok)
		}
	}
	for _, bad := range []string{"", "not a url", "ftp://x", "file:///etc/passwd", "http://"} {
		if isHTTPURL(bad) {
			t.Errorf("isHTTPURL(%q) = true", bad)
		}
	}
}

// seedDashURLFile owns loopback content (rewritten when the port changes)
// but never touches a tunnel URL the exposure agent wrote.
func TestSeedDashURLFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), dashURLFileName)

	if err := seedDashURLFile(f, "127.0.0.1:7333"); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(f); strings.TrimSpace(string(got)) != "http://127.0.0.1:7333" {
		t.Fatalf("seed wrote %q", got)
	}

	// Port changed: stale loopback is rewritten.
	if err := seedDashURLFile(f, "127.0.0.1:7401"); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(f); strings.TrimSpace(string(got)) != "http://127.0.0.1:7401" {
		t.Fatalf("stale loopback not rewritten: %q", got)
	}

	// Agent-owned tunnel URL: untouched.
	if err := os.WriteFile(f, []byte("https://snail.example.ts.net\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := seedDashURLFile(f, "127.0.0.1:7500"); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(f); strings.TrimSpace(string(got)) != "https://snail.example.ts.net" {
		t.Fatalf("tunnel URL clobbered: %q", got)
	}
}
