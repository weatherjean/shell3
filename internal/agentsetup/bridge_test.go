package agentsetup

import (
	"testing"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/luacfg"
)

// bridgeToolCallAction maps across two independent iota enums on a security
// boundary; verify every action maps correctly and an unknown action fails
// CLOSED (Block) rather than silently running.
func TestBridgeToolCallAction(t *testing.T) {
	for _, c := range []struct {
		in   luacfg.ToolCallAction
		want chat.ToolCallAction
	}{
		{luacfg.ActionRun, chat.Run},
		{luacfg.ActionBlock, chat.Block},
		{luacfg.ActionAsk, chat.Ask},
	} {
		if got := bridgeToolCallAction(c.in); got != c.want {
			t.Errorf("bridgeToolCallAction(%v) = %v, want %v", c.in, got, c.want)
		}
	}
	if got := bridgeToolCallAction(luacfg.ToolCallAction(99)); got != chat.Block {
		t.Errorf("bridgeToolCallAction(unknown) = %v, want Block (fail closed)", got)
	}
}
