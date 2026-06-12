package shell3_test

import (
	"os"
	"testing"
)

// TestMain redirects $HOME to a throwaway directory for the whole package.
// Tests here build real Runtimes, and NewRuntime resolves its global ~/.shell3
// tree from os.UserHomeDir() (which honors $HOME on unix). Without this, every
// run would leave a fresh project dir under the developer's real
// ~/.shell3/projects. Production is unaffected: it uses the real home.
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
