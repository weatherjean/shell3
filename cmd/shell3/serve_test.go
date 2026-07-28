//go:build unix

package main

import (
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/shell3"
)

// Serving without a password would put a shell on an open port: the agent's
// first verb is bash. So `serve` refuses rather than warning, and says exactly
// what to add.
func TestServeRefusesWithoutAWebPassword(t *testing.T) {
	err := requireWebPassword(shell3.WebConfig{})
	if err == nil {
		t.Fatal("serve accepted a config with no web password")
	}
	for _, want := range []string{"web.password", envWebPassword} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q; it has to be actionable", err, want)
		}
	}
}

func TestServeAcceptsAConfiguredPassword(t *testing.T) {
	if err := requireWebPassword(shell3.WebConfig{Password: strings.Repeat("x", minPasswordLength)}); err != nil {
		t.Errorf("a configured password was refused: %v", err)
	}
}

// A short password is the operator's call — refusing would lock them out of a
// config that already works — but it is worth saying out loud, because this
// password is the only thing between the internet and a shell.
func TestShortPasswordWarnsButDoesNotRefuse(t *testing.T) {
	short := shell3.WebConfig{Password: "hunter2"}
	if err := requireWebPassword(short); err != nil {
		t.Errorf("a short password refused the start: %v", err)
	}
	if weakPasswordWarning(short) == "" {
		t.Error("a short password produced no warning")
	}
	long := shell3.WebConfig{Password: strings.Repeat("x", minPasswordLength)}
	if got := weakPasswordWarning(long); got != "" {
		t.Errorf("a long password warned anyway: %q", got)
	}
}

// Off-loopback without https means the password and the session cookie cross
// the network in clear. The operator chose warn-over-refuse, so this must at
// least be said.
func TestCleartextWarningAppliesOffLoopbackOnly(t *testing.T) {
	if got := cleartextWarning("0.0.0.0:8765"); got == "" {
		t.Error("no warning for a network-facing plain-http bind")
	}
	if got := cleartextWarning("127.0.0.1:8765"); got != "" {
		t.Errorf("warned about a loopback bind: %q", got)
	}
}
