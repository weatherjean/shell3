package chat

import (
	"testing"

	"github.com/weatherjean/shell3/internal/fsx"
)

type fakeFS struct{ fsx.FileSystem }

func TestToolConfigFSDefaultsToOS(t *testing.T) {
	if _, ok := (ToolConfig{}).fs().(fsx.OS); !ok {
		t.Fatalf("nil FS should default to fsx.OS")
	}
}

func TestToolConfigFSHonorsInjected(t *testing.T) {
	f := fakeFS{}
	got := (ToolConfig{FS: f}).fs()
	if _, ok := got.(fakeFS); !ok {
		t.Fatalf("expected injected fakeFS, got %T", got)
	}
}
