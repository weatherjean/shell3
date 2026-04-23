package codeagent_test

import (
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/codeagent"
)

func TestCheckDeps_GitPresent(t *testing.T) {
	deps := codeagent.CheckDeps()
	var git *codeagent.DepStatus
	for i := range deps {
		if deps[i].Command == "git" {
			git = &deps[i]
		}
	}
	if git == nil {
		t.Fatal("expected git in dep list")
	}
	if !git.Found {
		t.Error("git should be found on any dev machine")
	}
	if !git.Required {
		t.Error("git should be required")
	}
}

func TestCheckDeps_FakeNotFound(t *testing.T) {
	dep := codeagent.LookupDep("definitely-not-a-real-tool-xyz", false)
	if dep.Found {
		t.Error("fake tool should not be found")
	}
}

func TestFormatInstallPrompt_NothingMissing(t *testing.T) {
	deps := []codeagent.DepStatus{
		{Name: "git", Command: "git", Found: true, Required: true},
	}
	prompt := codeagent.FormatInstallPrompt(deps)
	if !strings.Contains(prompt, "All") {
		t.Errorf("expected 'All' message, got: %q", prompt)
	}
}

func TestFormatInstallPrompt_Missing(t *testing.T) {
	deps := []codeagent.DepStatus{
		{Name: "ripgrep", Command: "rg", Found: false, Required: false},
		{Name: "gum", Command: "gum", Found: false, Required: false},
	}
	prompt := codeagent.FormatInstallPrompt(deps)
	if !strings.Contains(prompt, "ripgrep") {
		t.Error("expected ripgrep in prompt")
	}
	if !strings.Contains(prompt, "brew") {
		t.Errorf("expected brew hint, got: %q", prompt)
	}
}
