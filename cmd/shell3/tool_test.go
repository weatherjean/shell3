//go:build unix

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const commandTestKit = `#---
# agent: main
#---
main_prompt() { cat <<'EOF'
test
EOF
}

#---
# tool: greet
# description: greet someone
# params:
#   name: {type: string, required: true}
#---
main_greet() { printf 'hello %s' "$name"; }

#---
# test: greet — greets a person
#---
main_test_greet() { assert_eq "$(tool greet name=Ada)" "hello Ada"; }
`

func writeCommandTestKit(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "shell3.sh")
	if err := os.WriteFile(path, []byte(commandTestKit), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func executeToolCommand(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := newToolCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestToolCommandRunEndToEnd(t *testing.T) {
	path := writeCommandTestKit(t)
	out, err := executeToolCommand(t, "run", path, "greet", `{"name":"Grace"}`)
	if err != nil {
		t.Fatal(err)
	}
	if out != "hello Grace" {
		t.Fatalf("output = %q, want %q", out, "hello Grace")
	}
}

func TestToolCommandRejectsInvalidPayload(t *testing.T) {
	path := writeCommandTestKit(t)
	if _, err := executeToolCommand(t, "run", path, "greet", `{bad`); err == nil || !strings.Contains(err.Error(), "not valid JSON") {
		t.Fatalf("error = %v, want invalid JSON", err)
	}
}

func TestToolCommandRunsDeclaredTests(t *testing.T) {
	path := writeCommandTestKit(t)
	out, err := executeToolCommand(t, "test", path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "1 test(s) passed") {
		t.Fatalf("output = %q", out)
	}
}

func TestToolCommandCheckReportsSyntaxFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "broken.sh")
	if err := os.WriteFile(path, []byte("broken() {\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := executeToolCommand(t, "check", path)
	if err == nil {
		t.Fatal("check accepted invalid bash")
	}
	if !strings.Contains(out, "syntax:") {
		t.Fatalf("output = %q, want syntax diagnostic", out)
	}
}
