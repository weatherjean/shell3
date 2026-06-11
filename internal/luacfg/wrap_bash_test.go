package luacfg

import (
	"context"
	"testing"
)

// loadWrap loads a config whose only policy is the given shell3.wrap_bash body
// (a Lua function literal). The agent is minimal; the test exercises WrapBash
// directly, not a turn.
func loadWrap(t *testing.T, wrapFn string) *LoadedConfig {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", `
shell3.model("m", { base_url="u", api_key="k", model="x" })
shell3.wrap_bash(`+wrapFn+`)
shell3.agent({ name="a", model="m", prompt="p", tools={ bash=true } })
`)
	c, err := Load(dir+"/shell3.lua", dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return c
}

// TestWrapBashAllowPassthrough: a hook returning the command verbatim allows it
// unchanged.
func TestWrapBashAllowPassthrough(t *testing.T) {
	c := loadWrap(t, `function(cmd) return cmd end`)
	if !c.HasWrapBash() {
		t.Fatal("HasWrapBash should be true after shell3.wrap_bash")
	}
	got, allowed, reason, err := c.WrapBash(context.Background(), "echo hi")
	if err != nil {
		t.Fatal(err)
	}
	if !allowed {
		t.Fatalf("expected allowed, blocked with reason %q", reason)
	}
	if got != "echo hi" {
		t.Fatalf("passthrough changed command: %q", got)
	}
}

// TestWrapBashRewrite: a hook returning a different string rewrites the command
// that runs.
func TestWrapBashRewrite(t *testing.T) {
	c := loadWrap(t, `function(cmd) return "echo SAFE" end`)
	got, allowed, _, err := c.WrapBash(context.Background(), "rm -rf /")
	if err != nil {
		t.Fatal(err)
	}
	if !allowed {
		t.Fatal("rewrite should be allowed")
	}
	if got != "echo SAFE" {
		t.Fatalf("expected rewritten command, got %q", got)
	}
}

// TestWrapBashBlockWithReason: nil + reason blocks and surfaces the reason.
func TestWrapBashBlockWithReason(t *testing.T) {
	c := loadWrap(t, `function(cmd) return nil, "no rm" end`)
	got, allowed, reason, err := c.WrapBash(context.Background(), "rm -rf /")
	if err != nil {
		t.Fatal(err)
	}
	if allowed {
		t.Fatal("nil return should block")
	}
	if reason != "no rm" {
		t.Fatalf("expected reason %q, got %q", "no rm", reason)
	}
	// The original command is returned unchanged on a block (caller ignores it).
	if got != "rm -rf /" {
		t.Fatalf("block should return the original command, got %q", got)
	}
}

// TestWrapBashFalseBlocks: a bare false (no reason) blocks.
func TestWrapBashFalseBlocks(t *testing.T) {
	c := loadWrap(t, `function(cmd) return false end`)
	_, allowed, _, err := c.WrapBash(context.Background(), "ls")
	if err != nil {
		t.Fatal(err)
	}
	if allowed {
		t.Fatal("false return should block")
	}
}

// TestWrapBashFailsClosedOnError: a hook that raises a Lua error blocks (fail
// closed) rather than running the command.
func TestWrapBashFailsClosedOnError(t *testing.T) {
	c := loadWrap(t, `function(cmd) error("boom") end`)
	_, allowed, reason, err := c.WrapBash(context.Background(), "ls")
	if err != nil {
		t.Fatal(err)
	}
	if allowed {
		t.Fatal("a hook error must fail CLOSED (block), got allowed")
	}
	if !contains(reason, "wrap_bash error") {
		t.Fatalf("expected a wrap_bash error reason, got %q", reason)
	}
}

// TestWrapBashFailsClosedOnBadReturn: a non-string, non-false/nil return (e.g. a
// number or boolean true) is a broken hook and must fail closed.
func TestWrapBashFailsClosedOnBadReturn(t *testing.T) {
	for _, body := range []string{
		`function(cmd) return 42 end`,   // number
		`function(cmd) return true end`, // boolean true is not a command
		`function(cmd) return {} end`,   // table
	} {
		c := loadWrap(t, body)
		_, allowed, reason, err := c.WrapBash(context.Background(), "ls")
		if err != nil {
			t.Fatal(err)
		}
		if allowed {
			t.Fatalf("bad return %q must fail closed (block), got allowed", body)
		}
		if !contains(reason, "wrap_bash error") {
			t.Fatalf("expected a wrap_bash error reason for %q, got %q", body, reason)
		}
	}
}
