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
func captureBootSuccess(t *testing.T) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	printBootSuccess("/home/u/.shell3", "/home/u/.shell3/shell3.sh", "/home/u/.shell3/.env", false)
	_ = w.Close()
	os.Stdout = old
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// TestBootSuccessMessage verifies the rendered success message carries the
// load-bearing pointers for running the bot.
func TestBootSuccessMessage(t *testing.T) {
	base := captureBootSuccess(t)
	for _, want := range []string{
		"/home/u/.shell3/shell3.sh", // config paths
		"shell3 ask",                // the local ask mode must be advertised
		"shell3 telegram",           // how to run the front-end
		"TELEGRAM_TOKEN",            // where the token lives
		"docs/deploying.md",         // service management is user-owned, and documented there
	} {
		if !strings.Contains(base, want) {
			t.Errorf("boot success message missing %q", want)
		}
	}
}
