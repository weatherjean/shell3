package procutil

import (
	"os"
	"testing"
	"time"
)

func TestParentLifetimeClosesContext(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	ctx, stop, err := ParentContext(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if ctx.Err() != nil {
		t.Fatal("live parent was cancelled")
	}
	_ = w.Close()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("parent death did not cancel")
	}
}

func TestParentLifetimeRejectsRegularFile(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "regular")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, _, err := ParentContext(t.Context(), f); err == nil {
		t.Fatal("accepted ordinary file as lifetime pipe")
	}
}
