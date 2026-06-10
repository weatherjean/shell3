//go:build unix

package web

import "testing"

func TestVerifyInitData_RejectsTampered(t *testing.T) {
	// A syntactically-valid but unsigned/invalid initData must be rejected.
	if ok, _ := verifyInitData("user=%7B%22id%22%3A42%7D&hash=deadbeef", "bot-token", 42); ok {
		t.Fatal("tampered initData accepted")
	}
}
