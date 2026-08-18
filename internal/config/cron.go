package config

import (
	"bytes"
	"fmt"
	"strings"

	robcron "github.com/robfig/cron/v3"
	"gopkg.in/yaml.v3"
)

// parseCronFile parses one cron/<name>.md: frontmatter schedule (required,
// robfig/cron syntax) + exactly one of agent (a subagent name; the body is
// its prompt) or tool (a kit tool name; runs with no model turn) + optional
// direct/workdir.
func parseCronFile(data []byte, name string) (CronJob, error) {
	label := "cron/" + name + ".md"
	front, body, err := splitFrontmatter(data)
	if err != nil {
		return CronJob{}, fmt.Errorf("%s: %w", label, err)
	}
	var fm struct {
		Schedule string `yaml:"schedule"`
		Agent    string `yaml:"agent"`
		Tool     string `yaml:"tool"`
		Direct   bool   `yaml:"direct"`
		WorkDir  string `yaml:"workdir"`
	}
	dec := yaml.NewDecoder(bytes.NewReader(front))
	dec.KnownFields(true)
	if err := dec.Decode(&fm); err != nil {
		return CronJob{}, fmt.Errorf("%s: frontmatter: %w", label, err)
	}
	if fm.Schedule == "" {
		return CronJob{}, fmt.Errorf("%s: frontmatter needs a schedule", label)
	}
	// Same parser cron.New uses at arm time, so a schedule that loads is a
	// schedule that boots — `shell3 health` must never pass a config the
	// scheduler then fail-fasts on.
	if _, err := robcron.ParseStandard(fm.Schedule); err != nil {
		return CronJob{}, fmt.Errorf("%s: invalid schedule %q: %v", label, fm.Schedule, err)
	}
	// A job is either a prompt (agent:) or a tool call (tool:), never both
	// and never neither — a job with no move at all is a config mistake, not
	// a no-op.
	switch {
	case fm.Agent != "" && fm.Tool != "":
		return CronJob{}, fmt.Errorf("%s: frontmatter sets both agent: and tool: — exactly one of agent: or tool: (a job is either a prompt or a tool call)", label)
	case fm.Agent == "" && fm.Tool == "":
		return CronJob{}, fmt.Errorf("%s: frontmatter needs exactly one of agent: or tool: (a job is either a prompt or a tool call)", label)
	case fm.Agent != "" && strings.TrimSpace(body) == "":
		return CronJob{}, fmt.Errorf("%s: no prompt body after frontmatter", label)
	case fm.Tool != "" && fm.Direct:
		// direct: true only means something for an agent job (raw post, no
		// report turn) — a tool job already posts its own result with no
		// agent turn around it at all (see docs/kits.md), so direct: true on
		// one is a no-op that silently does nothing. KnownFields(true) already
		// refuses "both" and "neither" agent/tool with named errors; silently
		// swallowing this third key would be off-idiom next to those.
		return CronJob{}, fmt.Errorf("%s: frontmatter sets both tool: and direct: — direct only applies to an agent: job (a tool job already posts its own result with no agent turn)", label)
	}
	return CronJob{Name: name, Schedule: fm.Schedule, Agent: fm.Agent, Tool: fm.Tool,
		Prompt: body, WorkDir: fm.WorkDir, Direct: fm.Direct}, nil
}
