package config

import (
	"bytes"
	"fmt"
	"strings"

	robcron "github.com/robfig/cron/v3"
	"gopkg.in/yaml.v3"
)

// parseCronFile parses one cron/<name>.md: frontmatter schedule (required,
// robfig/cron syntax) + agent (required, a subagent name) + optional
// direct/workdir; the body is the job prompt.
func parseCronFile(data []byte, name string) (CronJob, error) {
	label := "cron/" + name + ".md"
	front, body, err := splitFrontmatter(data)
	if err != nil {
		return CronJob{}, fmt.Errorf("%s: %w", label, err)
	}
	var fm struct {
		Schedule string `yaml:"schedule"`
		Agent    string `yaml:"agent"`
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
	if fm.Agent == "" {
		return CronJob{}, fmt.Errorf("%s: frontmatter needs an agent (a subagent from agents/)", label)
	}
	if strings.TrimSpace(body) == "" {
		return CronJob{}, fmt.Errorf("%s: no prompt body after frontmatter", label)
	}
	return CronJob{Name: name, Schedule: fm.Schedule, Agent: fm.Agent,
		Prompt: body, WorkDir: fm.WorkDir, Direct: fm.Direct}, nil
}
