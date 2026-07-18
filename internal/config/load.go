package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Load reads the config directory dir: shell3.yaml (required) + .env +
// agent.md (required) + agents/*.md + skills/*.md + cron/*.md + heartbeat.md
// + hooks/*.sh. Absent optional pieces disable their features. Every error
// names the file that caused it.
func Load(dir string) (*LoadedConfig, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	c, err := load(abs)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", abs, err)
	}
	return c, nil
}

func load(dir string) (*LoadedConfig, error) {
	yamlPath := filepath.Join(dir, "shell3.yaml")
	data, err := os.ReadFile(yamlPath)
	if err != nil {
		if os.IsNotExist(err) {
			if _, lerr := os.Stat(filepath.Join(dir, "shell3.lua")); lerr == nil {
				return nil, fmt.Errorf("shell3 now uses a config directory (shell3.yaml) — re-run 'shell3 boot'; shell3.lua is no longer read")
			}
			return nil, fmt.Errorf("no shell3.yaml — run 'shell3 boot' to create one")
		}
		return nil, err
	}
	secrets, err := loadDotEnv(filepath.Join(dir, ".env"))
	if err != nil {
		return nil, err
	}
	c := &LoadedConfig{Secrets: secrets, dir: dir}
	if err := c.parseYAML(data, secrets); err != nil {
		return nil, err
	}
	warn := func(w string) { c.warnings = append(c.warnings, w) }

	// agent.md — the main agent (required).
	agentData, err := os.ReadFile(filepath.Join(dir, "agent.md"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no agent.md — the main agent is a markdown file beside shell3.yaml")
		}
		return nil, err
	}
	c.agent, err = parseMainAgent(agentData)
	if err != nil {
		return nil, err
	}

	// agents/*.md — subagents, filename order. Presence = registered;
	// delegation is on iff at least one exists.
	if err := c.loadSubagents(dir); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(c.subagents))
	for _, sa := range c.subagents {
		names = append(names, sa.Name)
	}
	c.agent.Subagents = names

	// skills/ — global, main agent only. Absent dir = no skills.
	skills, err := scanSkillDir(filepath.Join(dir, "skills"), warn)
	if err != nil {
		return nil, fmt.Errorf("skills: %w", err)
	}
	c.agent.Skills = skills

	// cron/*.md + heartbeat.md.
	if err := c.loadCron(dir); err != nil {
		return nil, err
	}
	hbData, err := os.ReadFile(filepath.Join(dir, "heartbeat.md"))
	if err == nil {
		if c.heartbeat, err = parseHeartbeatFile(hbData); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	// hooks/*.sh.
	if c.hooks, err = discoverHooks(dir, c.subagents, warn); err != nil {
		return nil, fmt.Errorf("hooks: %w", err)
	}

	// Cross-reference validation.
	for _, core := range c.cores() {
		if _, ok := c.Model(core.ModelName); !ok {
			return nil, fmt.Errorf("agent %q references unknown model %q", core.Name, core.ModelName)
		}
		for _, server := range core.MCP {
			if !c.hasMCPServer(server) {
				return nil, fmt.Errorf("agent %q opts into unknown mcp server %q", core.Name, server)
			}
		}
	}
	var mediaRefs []struct{ block, model string }
	if c.stt != nil {
		mediaRefs = append(mediaRefs, struct{ block, model string }{"media.stt", c.stt.ModelRef})
	}
	if c.tts != nil {
		mediaRefs = append(mediaRefs, struct{ block, model string }{"media.tts", c.tts.ModelRef})
	}
	if c.describe != nil {
		mediaRefs = append(mediaRefs, struct{ block, model string }{"media.describe", c.describe.ModelRef})
	}
	if c.imagegen != nil {
		mediaRefs = append(mediaRefs, struct{ block, model string }{"media.imagegen", c.imagegen.ModelRef})
	}
	for _, ref := range mediaRefs {
		if _, ok := c.Model(ref.model); !ok {
			return nil, fmt.Errorf("%s references unknown model %q", ref.block, ref.model)
		}
	}
	for _, job := range c.cron {
		if _, ok := c.SubagentByName(job.Agent); !ok {
			return nil, fmt.Errorf("cron/%s.md: unknown agent %q (must be a subagent from agents/)", job.Name, job.Agent)
		}
	}
	return c, nil
}

// cores returns the main agent plus every subagent as the shared core shape,
// for validation loops.
func (c *LoadedConfig) cores() []AgentCommon {
	out := []AgentCommon{c.agent.AgentCommon}
	for _, sa := range c.subagents {
		out = append(out, sa.AgentCommon)
	}
	return out
}

func (c *LoadedConfig) hasMCPServer(name string) bool {
	for _, s := range c.mcpServers {
		if s.Name == name {
			return true
		}
	}
	return false
}

// loadSubagents reads agents/*.md in filename order. A non-.md file is
// ignored; a subdirectory is ignored.
func (c *LoadedConfig) loadSubagents(dir string) error {
	entries, err := os.ReadDir(filepath.Join(dir, "agents"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, "agents", e.Name()))
		if err != nil {
			return err
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		// "agent" is the main agent's fixed name; a subagent shadowing it
		// would silently win every name lookup (hooks, task dispatch).
		if name == "agent" {
			return fmt.Errorf("agents/agent.md: the name \"agent\" is reserved for the main agent (agent.md) — rename the file")
		}
		sa, err := parseSubagentFile(data, name, c.agent.ModelName)
		if err != nil {
			return err
		}
		c.subagents = append(c.subagents, sa)
	}
	return nil
}

// loadCron reads cron/*.md in filename order.
func (c *LoadedConfig) loadCron(dir string) error {
	entries, err := os.ReadDir(filepath.Join(dir, "cron"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, "cron", e.Name()))
		if err != nil {
			return err
		}
		job, err := parseCronFile(data, strings.TrimSuffix(e.Name(), ".md"))
		if err != nil {
			return err
		}
		c.cron = append(c.cron, job)
	}
	return nil
}
