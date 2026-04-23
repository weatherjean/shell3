package codeagent

import (
	"fmt"
	"os/exec"
	"strings"
)

// DepStatus is the result of checking one CLI tool.
type DepStatus struct {
	Name     string
	Command  string
	Found    bool
	Required bool
}

// LookupDep checks whether a single command exists in PATH.
func LookupDep(command string, required bool) DepStatus {
	_, err := exec.LookPath(command)
	return DepStatus{Command: command, Found: err == nil, Required: required}
}

// CheckDeps checks all tools shell3 code can use.
func CheckDeps() []DepStatus {
	specs := []struct {
		name     string
		cmd      string
		required bool
	}{
		{"git", "git", true},
		{"ripgrep", "rg", false},
		{"fd", "fd", false},
		{"jq", "jq", false},
		{"gum", "gum", false},
		{"bat", "bat", false},
		{"sd", "sd", false},
		{"yq", "yq", false},
	}
	deps := make([]DepStatus, len(specs))
	for i, s := range specs {
		deps[i] = LookupDep(s.cmd, s.required)
		deps[i].Name = s.name
	}
	return deps
}

// FormatInstallPrompt prints dep status and returns a prompt the user can
// paste into any AI agent to install missing tools.
func FormatInstallPrompt(deps []DepStatus, os string) string {
	var missing []DepStatus
	for _, d := range deps {
		if !d.Found {
			missing = append(missing, d)
		}
	}
	if len(missing) == 0 {
		return "All shell3 code dependencies are installed."
	}

	names := make([]string, len(missing))
	for i, d := range missing {
		names[i] = d.Name + " (" + d.Command + ")"
	}

	cmds := installCommands(missing, os)

	return fmt.Sprintf(
		"Please install the following tools needed for shell3 code:\n%s\n\n%s",
		"- "+strings.Join(names, "\n- "),
		cmds,
	)
}

func installCommands(missing []DepStatus, os string) string {
	cmds := make([]string, len(missing))
	for i, d := range missing {
		cmds[i] = brewName(d.Command)
	}
	joined := strings.Join(cmds, " ")

	switch os {
	case "macos":
		return "On macOS:\n  brew install " + joined
	case "ubuntu":
		return "On Ubuntu:\n  sudo apt install ripgrep fd-find jq\n  # gum/bat/sd/yq: cargo install or snap\n  # See each tool's README for Ubuntu install"
	default:
		return "Install via your package manager: " + joined
	}
}

func brewName(cmd string) string {
	if cmd == "fd" {
		return "fd"
	}
	if cmd == "rg" {
		return "ripgrep"
	}
	return cmd
}

// DetectOS returns "macos", "ubuntu", or "linux".
func DetectOS() string {
	out, err := exec.Command("uname").Output()
	if err != nil {
		return "unknown"
	}
	switch strings.TrimSpace(string(out)) {
	case "Darwin":
		return "macos"
	case "Linux":
		if _, err := exec.LookPath("apt"); err == nil {
			return "ubuntu"
		}
		return "linux"
	}
	return "unknown"
}
