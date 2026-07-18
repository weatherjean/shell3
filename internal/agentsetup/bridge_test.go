package agentsetup

import (
	"testing"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/config"
)

// BridgeVerdict maps across two independent iota enums on a security
// boundary; verify every action maps correctly, an unknown action fails
// CLOSED (ActionBlock) rather than silently running, and the verdict's other
// fields ride across intact.
func TestBridgeVerdict(t *testing.T) {
	for _, c := range []struct {
		in   config.ToolCallAction
		want chat.ToolCallAction
	}{
		{config.ActionRun, chat.ActionRun},
		{config.ActionBlock, chat.ActionBlock},
		{config.ActionAsk, chat.ActionAsk},
	} {
		if got := BridgeVerdict(config.ToolCallVerdict{Action: c.in}).Action; got != c.want {
			t.Errorf("BridgeVerdict(%v).Action = %v, want %v", c.in, got, c.want)
		}
	}
	if got := BridgeVerdict(config.ToolCallVerdict{Action: config.ToolCallAction(99)}).Action; got != chat.ActionBlock {
		t.Errorf("BridgeVerdict(unknown).Action = %v, want ActionBlock (fail closed)", got)
	}
	v := BridgeVerdict(config.ToolCallVerdict{
		Action: config.ActionRun, Argv: []string{"bash", "-c", "x"},
		Prompt: "p", Reason: "r", Passthrough: true,
	})
	if len(v.Argv) != 3 || v.Prompt != "p" || v.Reason != "r" || !v.Passthrough {
		t.Errorf("BridgeVerdict dropped fields: %+v", v)
	}
}
