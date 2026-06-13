//go:build unix

package proc

import (
	"os"
	"testing"
)

func TestAlive(t *testing.T) {
	if !Alive(os.Getpid()) {
		t.Errorf("Alive(self) = false, want true")
	}
	if Alive(2147483646) {
		t.Errorf("Alive(2147483646) = true, want false")
	}
	if Alive(0) {
		t.Errorf("Alive(0) = true, want false")
	}
}
