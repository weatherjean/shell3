package config

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A command's function must exist, but health must NOT run it: unlike a gate
// (a decision function whose whole contract is to return a verdict), a command
// is an action — running one to check it would post the message, push the
// commit, or send the mail every time someone typed `shell3 health`.
func TestVerifyHooksChecksDefinitionWithoutRunning(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	src := minWiring + `
#---
# agent: main
# model: m1
# use: [bash]
#---
main_prompt() { cat <<'EOF'
You are a test agent.
EOF
}

#---
# command: publish
# description: has side effects
#---
cmd_publish() {
  touch "` + marker + `"
}
`
	writeTree(t, dir, map[string]string{KitFileName: src})
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	c.SetKitHooks(filepath.Join(dir, KitFileName), "main",
		KitHooks{Commands: map[string]string{"publish": "cmd_publish"}})

	if problems := c.VerifyHooks(context.Background()); len(problems) != 0 {
		t.Fatalf("problems = %v, want none", problems)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("VerifyHooks executed the command's body — it must only check that the function is defined")
	}
}

// A declaration whose function the shell cannot find is the failure this
// catches: at runtime a broken command is only a reply nobody reads, and a
// broken event subscriber is silent entirely.
func TestVerifyHooksReportsMissingFunction(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, nil)
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	c.SetKitHooks(filepath.Join(dir, KitFileName), "main",
		KitHooks{Commands: map[string]string{"ghost": "cmd_ghost_not_defined"}})

	problems := c.VerifyHooks(context.Background())
	if len(problems) == 0 || !strings.Contains(strings.Join(problems, "\n"), "cmd_ghost_not_defined") {
		t.Fatalf("problems = %v, want one naming the missing function", problems)
	}
}

// Event subscribers are checked the same way and for the same reason.
func TestVerifyHooksChecksEventSubscribers(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, nil)
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	c.SetKitHooks(filepath.Join(dir, KitFileName), "main",
		KitHooks{Events: map[string]EventSub{"main": {Func: "ev_missing", On: []string{"turn_done"}}}})

	problems := c.VerifyHooks(context.Background())
	if len(problems) == 0 || !strings.Contains(strings.Join(problems, "\n"), "ev_missing") {
		t.Fatalf("problems = %v, want one naming the missing subscriber", problems)
	}
}
