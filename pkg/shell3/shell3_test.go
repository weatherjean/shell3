package shell3_test

import (
	"testing"

	"github.com/weatherjean/shell3/pkg/shell3"
)

func TestNew_NoAuth_Errors(t *testing.T) {
	// Point at a temp dir with no auth.yaml — expect a clear error.
	tmp := t.TempDir()
	_, _, err := shell3.New(shell3.Options{HomeDir: tmp})
	if err == nil {
		t.Fatal("expected error when no auth configured, got nil")
	}
}
