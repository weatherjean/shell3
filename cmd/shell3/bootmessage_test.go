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
func captureBootSuccess(t *testing.T, svc serviceState) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	printBootSuccess("/home/u/.shell3", "/home/u/.shell3/shell3.yaml", "/home/u/.shell3/.env", false, svc)
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
	svcOn := captureBootSuccess(t, serviceEnabled)
	for _, want := range []string{
		"/home/u/.shell3/shell3.yaml", // config paths
		"shell3 dev",                  // the local dev mode must be advertised
		serviceUnitName,               // service management commands
		"Sleep caveat",                // laptop-suspend warning
	} {
		if !strings.Contains(svcOn, want) {
			t.Errorf("service-enabled message missing %q", want)
		}
	}
	if strings.Contains(svcOn, "shell3 telegram\n") {
		t.Error("service-enabled message should not tell the user to start the bot manually")
	}

	svcOff := captureBootSuccess(t, serviceDeclined)
	for _, want := range []string{"shell3 telegram", "shell3 dev", "shell3 web"} {
		if !strings.Contains(svcOff, want) {
			t.Errorf("no-service message missing %q", want)
		}
	}
}
