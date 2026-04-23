package codeagent

import (
	"fmt"
	"os/exec"
	"strings"
)

// DepStatus is the result of checking one CLI tool.
type DepStatus struct {
	Name        string
	Command     string
	Description string
	Found       bool
	Required    bool
}

// LookupDep checks whether a single command exists in PATH.
func LookupDep(command string, required bool) DepStatus {
	_, err := exec.LookPath(command)
	return DepStatus{Command: command, Found: err == nil, Required: required}
}

// CheckDeps checks all tools shell3 code can use.
func CheckDeps() []DepStatus {
	specs := []struct {
		name        string
		cmd         string
		desc        string
		required    bool
	}{
		{"git", "git", "version control", true},
		{"ripgrep", "rg", "fast code search", false},
		{"fd", "fd", "fast file finder", false},
		{"jq", "jq", "JSON processor", false},
		{"gum", "gum", "interactive prompts", false},
		{"bat", "bat", "syntax-highlighted cat", false},
		{"sd", "sd", "find-and-replace", false},
		{"yq", "yq", "YAML processor", false},
	}
	deps := make([]DepStatus, len(specs))
	for i, s := range specs {
		deps[i] = LookupDep(s.cmd, s.required)
		deps[i].Name = s.name
		deps[i].Description = s.desc
	}
	return deps
}

// FormatInstallPrompt returns a prompt the user can paste into any AI agent to install missing tools.
func FormatInstallPrompt(deps []DepStatus) string {
	var missing []DepStatus
	for _, d := range deps {
		if !d.Found {
			missing = append(missing, d)
		}
	}
	if len(missing) == 0 {
		return "All shell3 code dependencies are installed."
	}

	items := make([]string, len(missing))
	for i, d := range missing {
		items[i] = fmt.Sprintf("%s (%s)", d.Name, d.Description)
	}

	return fmt.Sprintf(
		"Please install these missing shell3 code tools: %s. Use brew on macOS, winget on Windows, or the native package manager on Linux.",
		strings.Join(items, ", "),
	)
}

