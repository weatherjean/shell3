//go:build unix

package main

import "testing"

// isLoopbackBind decides whether `shell3 serve` warns about facing the network:
// plain http carries the password in clear, so a non-loopback bind is called out.
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
