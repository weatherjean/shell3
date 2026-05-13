package hooks_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/weatherjean/shell3/pkg/hooks"
)

// fakeReleaser tracks pause/resume calls.
type fakeReleaser struct {
	paused  int
	resumed int
}

func (f *fakeReleaser) Pause() error  { f.paused++; return nil }
func (f *fakeReleaser) Resume() error { f.resumed++; return nil }

func TestCallHookTTYPausesAndResumes(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "hook.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}

	rel := &fakeReleaser{}
	r := hooks.NewRunner(hooks.Config{OnSessionStart: hooks.HookEntry{Command: script, NeedsTTY: true}})
	r.SetReleaser(rel)

	r.OnSessionStart(context.Background())

	if rel.paused != 1 {
		t.Errorf("Pause called %d times, want 1", rel.paused)
	}
	if rel.resumed != 1 {
		t.Errorf("Resume called %d times, want 1", rel.resumed)
	}
}

func TestNoReleaserSkipsPause(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "hook.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}

	// No releaser set — should not panic, hook should still run.
	r := hooks.NewRunner(hooks.Config{OnSessionStart: hooks.HookEntry{Command: script, NeedsTTY: true}})
	r.OnSessionStart(context.Background()) // must not panic
}
