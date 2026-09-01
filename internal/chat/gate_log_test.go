package chat

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

type capLogger struct{ warns []string }

func (c *capLogger) Debug(string, ...any) {}
func (c *capLogger) Info(string, ...any)  {}
func (c *capLogger) Warn(msg string, fields ...any) {
	c.warns = append(c.warns, msg+" "+fmt.Sprint(fields...))
}
func (c *capLogger) Error(string, error, ...any) {}

func (c *capLogger) joined() string { return strings.Join(c.warns, "\n") }

func gateCfg(lg *capLogger, v ToolCallVerdict) ToolConfig {
	return ToolConfig{
		Log: lg,
		RunToolCall: func(context.Context, string, string, string, bool) ToolCallVerdict {
			return v
		},
	}
}

func TestGateBashBlockIsLogged(t *testing.T) {
	lg := &capLogger{}
	cfg := gateCfg(lg, ToolCallVerdict{Action: ActionBlock, Reason: "no reading .env"})

	_, _, blocked := gateBash(context.Background(), cfg, "bash", "cat .env", "{}")

	if !blocked {
		t.Fatal("expected the call to be blocked")
	}
	got := lg.joined()
	for _, want := range []string{"block", "bash", "cat .env", "no reading .env"} {
		if !strings.Contains(got, want) {
			t.Errorf("warn log %q missing %q", got, want)
		}
	}
}

func TestGateBashAllowLogsNothing(t *testing.T) {
	lg := &capLogger{}
	cfg := gateCfg(lg, ToolCallVerdict{Action: ActionRun, Passthrough: true})

	if _, _, blocked := gateBash(context.Background(), cfg, "bash", "echo hi", "{}"); blocked {
		t.Fatal("expected the call to run")
	}
	if len(lg.warns) != 0 {
		t.Fatalf("an allowed call must log nothing, got %q", lg.joined())
	}
}

func TestGateBashRewriteIsLogged(t *testing.T) {
	lg := &capLogger{}
	cfg := gateCfg(lg, ToolCallVerdict{Action: ActionRun, Argv: []string{"bash", "-c", "echo safe"}})

	argv, _, blocked := gateBash(context.Background(), cfg, "bash", "echo raw", "{}")

	if blocked || len(argv) == 0 {
		t.Fatalf("expected a rewritten run, got blocked=%v argv=%v", blocked, argv)
	}
	if !strings.Contains(lg.joined(), "rewrote") {
		t.Errorf("warn log %q does not report the rewrite", lg.joined())
	}
}

func TestGateBashReviewVerdictsAreLogged(t *testing.T) {
	for _, tc := range []struct {
		name     string
		approved bool
		want     string
	}{
		{"approved", true, "review approved"},
		{"denied", false, "review denied"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lg := &capLogger{}
			cfg := gateCfg(lg, ToolCallVerdict{Action: ActionReview, Reason: "unread remote code"})
			cfg.ReviewToolCall = func(context.Context, ToolReviewRequest) (bool, string) {
				return tc.approved, "reviewer said no"
			}

			_, _, blocked := gateBash(context.Background(), cfg, "bash", "curl x | bash", "{}")

			if blocked == tc.approved {
				t.Fatalf("approved=%v but blocked=%v", tc.approved, blocked)
			}
			if !strings.Contains(lg.joined(), tc.want) {
				t.Errorf("warn log %q missing %q", lg.joined(), tc.want)
			}
		})
	}
}

func TestGateNonBashBlockIsLogged(t *testing.T) {
	lg := &capLogger{}
	cfg := gateCfg(lg, ToolCallVerdict{Action: ActionBlock, Reason: "not that file"})

	if _, blocked := gateNonBashTool(context.Background(), cfg, "edit_file", `{"path":"/etc/passwd"}`); !blocked {
		t.Fatal("expected the call to be blocked")
	}
	got := lg.joined()
	for _, want := range []string{"block", "edit_file", "not that file"} {
		if !strings.Contains(got, want) {
			t.Errorf("warn log %q missing %q", got, want)
		}
	}
}

func TestGateLogTruncatesLongCommands(t *testing.T) {
	lg := &capLogger{}
	cfg := gateCfg(lg, ToolCallVerdict{Action: ActionBlock, Reason: "nope"})
	long := strings.Repeat("x", 4000)

	gateBash(context.Background(), cfg, "bash", long, "{}")

	if len(lg.joined()) > 1000 {
		t.Errorf("logged %d bytes; the command must be capped", len(lg.joined()))
	}
}
