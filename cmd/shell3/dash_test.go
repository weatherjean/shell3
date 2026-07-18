//go:build unix

package main

import "testing"

// isLoopbackBind gates the unauthenticated `shell3 dash` server: only loopback
// binds are allowed so it can never face the network.
func TestIsLoopbackBind(t *testing.T) {
	loopback := []string{"127.0.0.1:8765", "localhost:8765", "[::1]:8765", "127.0.0.1:0"}
	for _, a := range loopback {
		if !isLoopbackBind(a) {
			t.Errorf("%q should be a loopback bind", a)
		}
	}
	exposed := []string{":8765", "0.0.0.0:8765", "[::]:8765", "192.168.1.5:8765", "example.com:8765", "garbage", ""}
	for _, a := range exposed {
		if isLoopbackBind(a) {
			t.Errorf("%q should NOT be a loopback bind", a)
		}
	}
}
