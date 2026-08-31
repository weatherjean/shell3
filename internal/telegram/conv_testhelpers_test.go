//go:build unix

package telegram

import (
	"testing"

	"github.com/weatherjean/shell3/internal/shell3"
)

func tconv(b *Bot) *conversation { return b.conv(b.homeChat) }

func mustRuntime(t *testing.T) *shell3.Runtime {
	t.Helper()
	rt, _ := newFakeRuntime(t, "ok")
	return rt
}
