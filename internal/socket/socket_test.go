package socket

import (
	"path/filepath"
	"testing"
	"time"
)

func TestSendReceive(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "s.sock")

	got := make(chan []byte, 1)
	lis, err := Listen(sock, func(line []byte) {
		got <- line
	})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer lis.Close()

	if err := Send(sock, []byte(`{"kind":"agent_done"}`)); err != nil {
		t.Fatalf("send: %v", err)
	}
	select {
	case line := <-got:
		if string(line) != `{"kind":"agent_done"}` {
			t.Fatalf("got %q", line)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for delivery")
	}
}

func TestSend_DeadSocketErrors(t *testing.T) {
	if err := Send(filepath.Join(t.TempDir(), "nope.sock"), []byte("x")); err == nil {
		t.Fatal("expected error sending to nonexistent socket")
	}
}
