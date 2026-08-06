//go:build unix

package telegram

import (
	"os"
	"testing"
)

// TestMain redirects $HOME to a throwaway directory so tests that build a real
// shell3.Runtime never create project dirs under the developer's real
// ~/.shell3/projects (NewRuntime resolves its global tree from
// os.UserHomeDir(), which honors $HOME on unix). Production uses the real home.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "shell3-test-home-")
	if err != nil {
		panic(err)
	}
	os.Setenv("HOME", home)
	code := m.Run()
	_ = os.RemoveAll(home)
	os.Exit(code)
}
