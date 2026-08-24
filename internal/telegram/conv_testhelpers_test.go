//go:build unix

package telegram

import (
	"testing"

	"github.com/weatherjean/shell3/internal/shell3"
)

// tconv is the test shorthand for "this bot's home room": the single-chat
// tests were written before rooms existed, and their bot always talks in the
// chat it was constructed with.
func tconv(b *Bot) *conversation { return b.conv(b.homeChat) }

// mustRuntime is a fake runtime whose model always answers "ok" — enough for
// routing tests, which care where a reply lands, not what it says.
func mustRuntime(t *testing.T) *shell3.Runtime {
	t.Helper()
	rt, _ := newFakeRuntime(t, "ok")
	return rt
}
