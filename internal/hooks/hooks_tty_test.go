package hooks_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/weatherjean/shell3/internal/hooks"
)

// fakeReleaser tracks release/restore calls.
type fakeReleaser struct {
	released int
	restored int
}

func (f *fakeReleaser) Release() error { f.released++; return nil }
func (f *fakeReleaser) Restore() error { f.restored++; return nil }

func TestCallHookTTYReleasesAndRestores(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "hook.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}

	rel := &fakeReleaser{}
	r := hooks.NewRunner(hooks.Config{OnSessionStart: hooks.HookEntry{Command: script, NeedsTTY: true}})
	r.SetReleaser(rel)

	r.OnSessionStart(context.Background())

	if rel.released != 1 {
		t.Errorf("Release called %d times, want 1", rel.released)
	}
	if rel.restored != 1 {
		t.Errorf("Restore called %d times, want 1", rel.restored)
	}
}

func TestNoReleaserSkipsRelease(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "hook.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}

	// No releaser set — should not panic, hook should still run.
	r := hooks.NewRunner(hooks.Config{OnSessionStart: hooks.HookEntry{Command: script, NeedsTTY: true}})
	r.OnSessionStart(context.Background()) // must not panic
}
