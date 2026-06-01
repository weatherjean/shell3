package luacfg

import "testing"

func TestConfirmDangerous(t *testing.T) {
	cases := map[string]Decision{
		"ls -la":           DecisionAllow,
		"rm -rf /tmp/x":    DecisionBlock,
		"sudo reboot":      DecisionBlock,
		"git push --force": DecisionBlock,
		"echo hi":          DecisionAllow,
	}
	g := GuardEntry{Builtin: "confirm_dangerous", prompt: false}
	for cmd, want := range cases {
		d, _ := runBuiltinGuard(g, "bash", map[string]any{"command": cmd})
		if d != want {
			t.Errorf("%q: got %v want %v", cmd, d, want)
		}
	}
	// non-shell tools always allowed
	if d, _ := runBuiltinGuard(g, "edit_file", map[string]any{}); d != DecisionAllow {
		t.Errorf("edit_file should be allowed by confirm_dangerous")
	}
}
