package config

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
	"time"

	robcron "github.com/robfig/cron/v3"
	"gopkg.in/yaml.v3"
)

var hhmmRe = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

// parseCronFile parses one cron/<name>.md: frontmatter schedule (required,
// robfig/cron syntax) + agent (required, a subagent name) + optional
// notify/workdir; the body is the job prompt.
func parseCronFile(data []byte, name string) (CronJob, error) {
	label := "cron/" + name + ".md"
	front, body, err := splitFrontmatter(data)
	if err != nil {
		return CronJob{}, fmt.Errorf("%s: %w", label, err)
	}
	var fm struct {
		Schedule string `yaml:"schedule"`
		Agent    string `yaml:"agent"`
		Notify   bool   `yaml:"notify"`
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
		Prompt: body, WorkDir: fm.WorkDir, Notify: fm.Notify}, nil
}

// parseHeartbeatFile parses heartbeat.md: frontmatter every (required) +
// optional prompt preamble override + optional active window; the body is the
// checklist.
func parseHeartbeatFile(data []byte) (*Heartbeat, error) {
	front, body, err := splitFrontmatter(data)
	if err != nil {
		return nil, fmt.Errorf("heartbeat.md: %w", err)
	}
	var fm struct {
		Every  string `yaml:"every"`
		Prompt string `yaml:"prompt"`
		Active *struct {
			From string `yaml:"from"`
			To   string `yaml:"to"`
			TZ   string `yaml:"tz"`
		} `yaml:"active"`
	}
	dec := yaml.NewDecoder(bytes.NewReader(front))
	dec.KnownFields(true)
	if err := dec.Decode(&fm); err != nil {
		return nil, fmt.Errorf("heartbeat.md: frontmatter: %w", err)
	}
	if fm.Every == "" {
		return nil, fmt.Errorf("heartbeat.md: frontmatter needs every (e.g. 30m)")
	}
	every, err := time.ParseDuration(fm.Every)
	if err != nil || every <= 0 {
		return nil, fmt.Errorf("heartbeat.md: invalid every %q", fm.Every)
	}
	if strings.TrimSpace(body) == "" {
		return nil, fmt.Errorf("heartbeat.md: no checklist body after frontmatter")
	}
	hb := &Heartbeat{Every: every, Checklist: body, Prompt: fm.Prompt}
	if a := fm.Active; a != nil {
		if (a.From == "") != (a.To == "") {
			return nil, fmt.Errorf("heartbeat.md: active needs both from and to")
		}
		if a.From != "" && (!hhmmRe.MatchString(a.From) || !hhmmRe.MatchString(a.To)) {
			return nil, fmt.Errorf("heartbeat.md: active from/to must be HH:MM")
		}
		if a.TZ != "" {
			if _, err := time.LoadLocation(a.TZ); err != nil {
				return nil, fmt.Errorf("heartbeat.md: invalid active.tz %q", a.TZ)
			}
		}
		hb.ActiveFrom, hb.ActiveTo, hb.TZ = a.From, a.To, a.TZ
	}
	return hb, nil
}
