package config

import (
	"bytes"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// agentFrontmatter is the strict frontmatter schema shared by agent.md and
// agents/*.md. `mcp` accepts either the string "all" or a list of server
// names.
type agentFrontmatter struct {
	Model       string       `yaml:"model"`
	Tools       []string     `yaml:"tools"`
	MCP         stringOrList `yaml:"mcp"`
	Description string       `yaml:"description"`
	Prune       *bool        `yaml:"prune"`
}

// stringOrList decodes a YAML value that is either a scalar string or a list
// of strings.
type stringOrList struct {
	One  string
	Many []string
}

func (s *stringOrList) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		return node.Decode(&s.One)
	case yaml.SequenceNode:
		return node.Decode(&s.Many)
	}
	return fmt.Errorf("must be a string or a list of strings")
}

// parseAgentFile parses one agent markdown file (agent.md or agents/<name>.md)
// into the shared core. name is the agent name (filename-derived for
// subagents); label names the file in errors.
func parseAgentFile(data []byte, name, label string) (AgentCommon, string, error) {
	front, body, err := splitFrontmatter(data)
	if err != nil {
		return AgentCommon{}, "", fmt.Errorf("%s: %w", label, err)
	}
	var fm agentFrontmatter
	dec := yaml.NewDecoder(bytes.NewReader(front))
	dec.KnownFields(true)
	if err := dec.Decode(&fm); err != nil {
		return AgentCommon{}, "", fmt.Errorf("%s: frontmatter: %w", label, err)
	}
	if strings.TrimSpace(body) == "" {
		return AgentCommon{}, "", fmt.Errorf("%s: no prompt body after frontmatter", label)
	}
	core := AgentCommon{Name: name, ModelName: fm.Model, Prompt: body, Prune: fm.Prune}
	for _, tool := range fm.Tools {
		switch tool {
		case "bash":
			core.Gates.Bash = true
		case "bash_bg":
			core.Gates.BashBg = true
		case "edit":
			core.Gates.Edit = true
		case "media":
			core.Gates.Media = true
		default:
			return AgentCommon{}, "", fmt.Errorf("%s: unknown tool %q (valid: bash, bash_bg, edit, media)", label, tool)
		}
	}
	switch {
	case fm.MCP.One == "all":
		core.MCPAll = true
	case fm.MCP.One != "":
		return AgentCommon{}, "", fmt.Errorf("%s: mcp must be \"all\" or a list of server names, got %q", label, fm.MCP.One)
	default:
		core.MCP = fm.MCP.Many
	}
	return core, fm.Description, nil
}

// parseMainAgent parses agent.md: model is required (there is no implicit
// first-model default) and description is meaningless (it exists for
// task-tool routing, which never targets the main agent).
func parseMainAgent(data []byte) (Agent, error) {
	core, desc, err := parseAgentFile(data, "", "agent.md")
	if err != nil {
		return Agent{}, err
	}
	if desc != "" {
		return Agent{}, fmt.Errorf("agent.md: description is only valid on subagents (agents/*.md)")
	}
	if core.ModelName == "" {
		return Agent{}, fmt.Errorf("agent.md: frontmatter needs a model")
	}
	// The main agent's name is fixed: there is exactly one, addressed as
	// "agent" everywhere an agent name surfaces (status line, transcripts).
	core.Name = "agent"
	return Agent{AgentCommon: core}, nil
}

// parseSubagentFile parses one agents/<name>.md. description is required (the
// task tool routes on it); model defaults to the main agent's.
func parseSubagentFile(data []byte, name, mainModel string) (Subagent, error) {
	label := "agents/" + name + ".md"
	core, desc, err := parseAgentFile(data, name, label)
	if err != nil {
		return Subagent{}, err
	}
	if desc == "" {
		return Subagent{}, fmt.Errorf("%s: frontmatter needs a description (the task tool routes on it)", label)
	}
	if core.ModelName == "" {
		core.ModelName = mainModel
	}
	return Subagent{AgentCommon: core, Description: desc}, nil
}
