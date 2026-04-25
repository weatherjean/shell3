// Package hooks runs lifecycle hook shell commands with JSON stdin/stdout.
package hooks

// Config holds shell command paths for each lifecycle hook.
type Config struct {
	OnSessionStart string `yaml:"on_session_start"`
	OnSessionEnd   string `yaml:"on_session_end"`
	OnTurnStart    string `yaml:"on_turn_start"`
	OnTurnEnd      string `yaml:"on_turn_end"`
	OnToolCall     string `yaml:"on_tool_call"`
	OnToolResult   string `yaml:"on_tool_result"`
	OnContextBuild string `yaml:"on_context_build"`
	OnError        string `yaml:"on_error"`
}

type hookInput struct {
	Hook     string         `json:"hook"`
	Tool     string         `json:"tool,omitempty"`
	Params   map[string]any `json:"params,omitempty"`
	Messages any            `json:"messages,omitempty"`
}

type hookOutput struct {
	Action   string         `json:"action"`
	Reason   string         `json:"reason,omitempty"`
	Params   map[string]any `json:"params,omitempty"`
	Messages any            `json:"messages,omitempty"`
}

// TTYReleaser suspends and resumes the TUI so subprocess hooks can use the real terminal.
type TTYReleaser interface {
	Release() error
	Restore() error
}
