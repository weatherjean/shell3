package config

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// cmdCfg writes a kit declaring main plus the given commands and installs
// them the way agentsetup.LoadKit does. Keys are command names, values are the
// shell body of that command's function.
func cmdCfg(t *testing.T, cmds map[string]string) *LoadedConfig {
	t.Helper()
	var b strings.Builder
	b.WriteString(minWiring)
	b.WriteString(`
#---
# agent: main
# model: m1
# use: [bash]
#---
main_prompt() { cat <<'EOF'
You are a test agent.
EOF
}
`)
	fns := map[string]string{}
	for name, body := range cmds {
		fn := "cmd_" + name
		fns[name] = fn
		b.WriteString("\n#---\n# command: " + name + "\n# description: does " + name + "\n#---\n" +
			fn + "() {\n" + body + "\n}\n")
	}
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{KitFileName: b.String()})
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	c.SetKitHooks(filepath.Join(dir, KitFileName), "main", KitHooks{Commands: fns})
	return c
}

// A command's stdout is the reply text.
func TestRunCommandReturnsStdout(t *testing.T) {
	c := cmdCfg(t, map[string]string{"standup": `echo "3 commits"`})
	out, err := c.RunCommand(context.Background(), "standup", "")
	if err != nil {
		t.Fatalf("RunCommand: %v", err)
	}
	if out != "3 commits" {
		t.Errorf("out = %q, want %q", out, "3 commits")
	}
}

// Everything after the verb reaches the function as $ARG — the same
// environment-variable convention a tool:'s params use.
func TestRunCommandPassesArg(t *testing.T) {
	c := cmdCfg(t, map[string]string{"echo": `printf 'got:%s' "$ARG"`})
	out, err := c.RunCommand(context.Background(), "echo", "last week")
	if err != nil {
		t.Fatalf("RunCommand: %v", err)
	}
	if out != "got:last week" {
		t.Errorf("out = %q", out)
	}
}

// A failing command reports its stderr rather than posting a blank reply. A
// command cannot block anything, so there is no fail-closed question here —
// the error is the reply.
func TestRunCommandFailureReportsStderr(t *testing.T) {
	c := cmdCfg(t, map[string]string{"broken": `echo "no repo here" >&2; exit 1`})
	_, err := c.RunCommand(context.Background(), "broken", "")
	if err == nil {
		t.Fatal("want an error from a nonzero-exit command")
	}
	if !strings.Contains(err.Error(), "no repo here") {
		t.Errorf("err = %v, want it to carry the stderr", err)
	}
}

// An undeclared command is not this config's to answer; the caller falls back
// to its own unknown-command handling.
func TestRunCommandUnknownReportsNotFound(t *testing.T) {
	c := cmdCfg(t, map[string]string{"standup": "echo hi"})
	if _, err := c.RunCommand(context.Background(), "nope", ""); err != ErrNoSuchCommand {
		t.Fatalf("err = %v, want ErrNoSuchCommand", err)
	}
}

// HasCommand is what a front-end asks before routing a verb it does not know.
func TestHasCommand(t *testing.T) {
	c := cmdCfg(t, map[string]string{"standup": "echo hi"})
	if !c.HasCommand("standup") {
		t.Error("HasCommand(standup) = false")
	}
	if c.HasCommand("stop") {
		t.Error("HasCommand(stop) = true, want false")
	}
}
