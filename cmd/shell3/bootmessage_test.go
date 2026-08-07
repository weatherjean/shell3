//go:build unix

package main

import (
	"io"
	"os"
	"runtime"
	"strings"
	"testing"
)

// captureBootSuccess runs printBootSuccess with stdout redirected and returns
// what it printed.
func captureBootSuccess(t *testing.T) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	printBootSuccess("/home/u/.shell3", "/home/u/.shell3/shell3.yaml", "/home/u/.shell3/.env", false)
	_ = w.Close()
	os.Stdout = old
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// TestBootSuccessMessage verifies the rendered success message carries the
// load-bearing pointers for reaching the interface.
func TestBootSuccessMessage(t *testing.T) {
	base := captureBootSuccess(t)
	for _, want := range []string{
		"/home/u/.shell3/shell3.yaml", // config paths
		"shell3 ask",                  // the local ask mode must be advertised
		"shell3 serve",                // how to run the interface
		"http://127.0.0.1:8765",       // where to reach it
		"docs/deploying.md",           // service + exposure are user-owned, and documented there
	} {
		if !strings.Contains(base, want) {
			t.Errorf("boot success message missing %q", want)
		}
	}
	// On Linux the finale carries the one-paste service+tailnet block, with
	// the systemd %h specifier intact (not eaten by the Printf helper).
	if runtime.GOOS == "linux" {
		for _, want := range []string{
			"systemctl --user enable --now shell3",
			"tailscale serve --bg 8765",
			"ExecStart=%h/.local/bin/shell3 serve",
		} {
			if !strings.Contains(base, want) {
				t.Errorf("boot success message missing %q", want)
			}
		}
	}
}
