//go:build unix

package main

import (
	"io"
	"os"
	"strings"
	"testing"
)

// captureBootSuccess runs printBootSuccess with stdout redirected and returns
// what it printed.
func captureBootSuccess(t *testing.T, tunnelWired bool) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	printBootSuccess("/home/u/.shell3", "/home/u/.shell3/shell3.yaml", "/home/u/.shell3/.env", false, tunnelWired)
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
	base := captureBootSuccess(t, false)
	for _, want := range []string{
		"/home/u/.shell3/shell3.yaml", // config paths
		"shell3 ask",                  // the local ask mode must be advertised
		"shell3 serve",                // how to run the interface
		"http://127.0.0.1:8765",       // where to reach it
	} {
		if !strings.Contains(base, want) {
			t.Errorf("boot success message missing %q", want)
		}
	}

	// With the tunnel wired, the message must say where the public URL
	// appears rather than telling the user to set web.tunnel themselves.
	tun := captureBootSuccess(t, true)
	if !strings.Contains(tun, "tunnel.log") {
		t.Error("tunnel-wired message missing \"tunnel.log\"")
	}
	if strings.Contains(tun, "Set web.tunnel") {
		t.Error("tunnel-wired message still tells the user to set web.tunnel")
	}
}
