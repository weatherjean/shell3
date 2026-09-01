package config

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/kit"
)

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
	writeTree(t, dir, map[string]string{kit.FileName: b.String()})
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	c.SetKitHooks(filepath.Join(dir, kit.FileName), "main", KitHooks{Commands: fns})
	return c
}

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

func TestRunCommandUnknownReportsNotFound(t *testing.T) {
	c := cmdCfg(t, map[string]string{"standup": "echo hi"})
	if _, err := c.RunCommand(context.Background(), "nope", ""); err != ErrNoSuchCommand {
		t.Fatalf("err = %v, want ErrNoSuchCommand", err)
	}
}
