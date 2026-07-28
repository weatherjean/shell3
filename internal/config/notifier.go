package config

import "fmt"

// Notifier is the reserved triage persona (notifier.md beside agent.md): one
// small model turn per background completion, judging whether the result is
// posted to the user, handed to the main agent, or stays silent. nil on
// LoadedConfig means the file is absent — the host then posts every completion
// raw (degraded mode). Its toolset is fixed by the host (read, list_files,
// send, wake), so the frontmatter carries only a model.
type Notifier struct {
	AgentCommon
}

// parseNotifierFile parses notifier.md: model is required; tools, mcp,
// description, and context are rejected — the persona's surface is fixed.
func parseNotifierFile(data []byte) (Notifier, error) {
	core, desc, err := parseAgentFile(data, "notifier", "notifier.md")
	if err != nil {
		return Notifier{}, err
	}
	if desc != "" {
		return Notifier{}, fmt.Errorf("notifier.md: description is only valid on subagents (agents/*.md)")
	}
	if core.ModelName == "" {
		return Notifier{}, fmt.Errorf("notifier.md: frontmatter needs a model")
	}
	g := core.Gates
	if g.Bash || g.BashBg || g.Edit || g.Media || g.Read || g.List {
		return Notifier{}, fmt.Errorf("notifier.md: tools are fixed (read, list_files, send, wake) — remove the tools: key")
	}
	if len(core.MCP) > 0 || core.MCPAll {
		return Notifier{}, fmt.Errorf("notifier.md: mcp is not available to the notifier")
	}
	if len(core.Context) > 0 {
		return Notifier{}, fmt.Errorf("notifier.md: context is only valid on the main agent (agent.md)")
	}
	return Notifier{AgentCommon: core}, nil
}
