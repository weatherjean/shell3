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

func TestDashMintURL(t *testing.T) {
	srv := startTestDash(t)
	dir := t.TempDir()
	urlFile := filepath.Join(dir, dashURLFileName)

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

	if err := os.WriteFile(urlFile, []byte("https://snail.example.ts.net/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	u = dashMintURL(urlFile, srv)
	if !strings.HasPrefix(u, "https://snail.example.ts.net/?t=") {
		t.Fatalf("file base URL = %q", u)
	}

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

func TestSeedDashURLFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), dashURLFileName)

	if err := seedDashURLFile(f, "127.0.0.1:7333"); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(f); strings.TrimSpace(string(got)) != "http://127.0.0.1:7333" {
		t.Fatalf("seed wrote %q", got)
	}

	if err := seedDashURLFile(f, "127.0.0.1:7401"); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(f); strings.TrimSpace(string(got)) != "http://127.0.0.1:7401" {
		t.Fatalf("stale loopback not rewritten: %q", got)
	}

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
