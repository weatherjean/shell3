package agentsetup

import (
	"testing"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/luacfg"
)

// BridgeVerdict maps across two independent iota enums on a security
// boundary; verify every action maps correctly, an unknown action fails
// CLOSED (ActionBlock) rather than silently running, and the verdict's other
// fields ride across intact.
func TestBridgeVerdict(t *testing.T) {
	for _, c := range []struct {
		in   luacfg.ToolCallAction
		want chat.ToolCallAction
	}{
		{luacfg.ActionRun, chat.ActionRun},
		{luacfg.ActionBlock, chat.ActionBlock},
		{luacfg.ActionAsk, chat.ActionAsk},
	} {
		if got := BridgeVerdict(luacfg.ToolCallVerdict{Action: c.in}).Action; got != c.want {
			t.Errorf("BridgeVerdict(%v).Action = %v, want %v", c.in, got, c.want)
		}
	}
	if got := BridgeVerdict(luacfg.ToolCallVerdict{Action: luacfg.ToolCallAction(99)}).Action; got != chat.ActionBlock {
		t.Errorf("BridgeVerdict(unknown).Action = %v, want ActionBlock (fail closed)", got)
	}
	v := BridgeVerdict(luacfg.ToolCallVerdict{
		Action: luacfg.ActionRun, Argv: []string{"bash", "-c", "x"},
		Prompt: "p", Reason: "r", Passthrough: true,
	})
	if len(v.Argv) != 3 || v.Prompt != "p" || v.Reason != "r" || !v.Passthrough {
		t.Errorf("BridgeVerdict dropped fields: %+v", v)
	}
}
