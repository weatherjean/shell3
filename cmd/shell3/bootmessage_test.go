//go:build unix

package main

import (
	"io"
	"os"
	"strings"
	"testing"
)

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

func TestBootSuccessMessage(t *testing.T) {
	base := captureBootSuccess(t)
	for _, want := range []string{
		"/home/u/.shell3/shell3.sh",
		"shell3 ask",
		"shell3 telegram",
		"TELEGRAM_TOKEN",
		"docs/deploying.md",
	} {
		if !strings.Contains(base, want) {
			t.Errorf("boot success message missing %q", want)
		}
	}
}
