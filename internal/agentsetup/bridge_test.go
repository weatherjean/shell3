package agentsetup

import (
	"testing"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/config"
)

func TestBridgeVerdict(t *testing.T) {
	for _, c := range []struct {
		in   config.ToolCallAction
		want chat.ToolCallAction
	}{
		{config.ActionRun, chat.ActionRun},
		{config.ActionBlock, chat.ActionBlock},
	} {
		if got := bridgeVerdict(config.ToolCallVerdict{Action: c.in}).Action; got != c.want {
			t.Errorf("bridgeVerdict(%v).Action = %v, want %v", c.in, got, c.want)
		}
	}
	if got := bridgeVerdict(config.ToolCallVerdict{Action: config.ToolCallAction(99)}).Action; got != chat.ActionBlock {
		t.Errorf("bridgeVerdict(unknown).Action = %v, want ActionBlock (fail closed)", got)
	}
	v := bridgeVerdict(config.ToolCallVerdict{
		Action: config.ActionRun, Argv: []string{"bash", "-c", "x"},
		Reason: "r", Passthrough: true,
	})
	if len(v.Argv) != 3 || v.Reason != "r" || !v.Passthrough {
		t.Errorf("bridgeVerdict dropped fields: %+v", v)
	}
}
