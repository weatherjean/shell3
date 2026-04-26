// Package hooks runs lifecycle hook shell commands with JSON stdin/stdout.
package hooks

import "gopkg.in/yaml.v3"

// HookEntry is a single lifecycle hook definition.
// In YAML it accepts either a plain string (command only) or a mapping:
//
//	on_tool_call: "bash .shell3/hooks/confirm.sh"          # string form
//	on_tool_call:                                           # mapping form
//	  command: "bash .shell3/hooks/confirm.sh"
//	  needs_tty: true
type HookEntry struct {
	Command  string `yaml:"command"`
	NeedsTTY bool   `yaml:"needs_tty"`
}

// UnmarshalYAML implements yaml.Unmarshaler so a plain string is treated as
// {Command: s, NeedsTTY: false}.
func (h *HookEntry) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		h.Command = value.Value
		return nil
	}
	type plain HookEntry
	return value.Decode((*plain)(h))
}

// Config holds hook entries for each lifecycle event.
type Config struct {
	OnSessionStart HookEntry `yaml:"on_session_start"`
	OnSessionEnd   HookEntry `yaml:"on_session_end"`
	OnTurnStart    HookEntry `yaml:"on_turn_start"`
	OnTurnEnd      HookEntry `yaml:"on_turn_end"`
	OnToolCall     HookEntry `yaml:"on_tool_call"`
	OnToolResult   HookEntry `yaml:"on_tool_result"`
	OnContextBuild HookEntry `yaml:"on_context_build"`
	OnError        HookEntry `yaml:"on_error"`
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

// TTYReleaser suspends and resumes the host TUI so subprocess hooks can
// use the real terminal. Pause must restore cooked mode and clear the
// live frame; Resume must re-enter raw mode and repaint. Implemented by
// patchapp.App.
type TTYReleaser interface {
	Pause() error
	Resume() error
}
