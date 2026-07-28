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
func captureBootSuccess(t *testing.T, tunnelWired bool, svc serviceState) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	printBootSuccess("/home/u/.shell3", "/home/u/.shell3/shell3.yaml", "/home/u/.shell3/.env", false, tunnelWired, svc)
	_ = w.Close()
	os.Stdout = old
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// TestBootSuccessMessage verifies the rendered success message carries the
// load-bearing pointers for each service outcome.
func TestBootSuccessMessage(t *testing.T) {
	svcOn := captureBootSuccess(t, false, serviceEnabled)
	for _, want := range []string{
		"/home/u/.shell3/shell3.yaml", // config paths
		"shell3 ask",                  // the local ask mode must be advertised
		serviceUnitName,               // service management commands
		"Sleep caveat",                // laptop-suspend warning
		"http://127.0.0.1:8765",       // where to reach the running service
	} {
		if !strings.Contains(svcOn, want) {
			t.Errorf("service-enabled message missing %q", want)
		}
	}
	if strings.Contains(svcOn, "shell3 serve\n") {
		t.Error("service-enabled message should not tell the user to start the bot manually")
	}

	svcOff := captureBootSuccess(t, false, serviceDeclined)
	for _, want := range []string{"shell3 serve", "shell3 ask"} {
		if !strings.Contains(svcOff, want) {
			t.Errorf("no-service message missing %q", want)
		}
	}

	// With the tunnel wired, the message must say where the public URL
	// appears rather than telling the user to set web.tunnel themselves.
	tun := captureBootSuccess(t, true, serviceEnabled)
	for _, want := range []string{"tunnel.log", "journalctl"} {
		if !strings.Contains(tun, want) {
			t.Errorf("tunnel-wired message missing %q", want)
		}
	}
	if strings.Contains(tun, "Set web.tunnel") {
		t.Error("tunnel-wired message still tells the user to set web.tunnel")
	}
}
