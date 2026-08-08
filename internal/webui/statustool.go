//go:build unix

package webui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/weatherjean/shell3/internal/shell3"
)

// The agent's own eyes on its condition. The data already exists for the
// Status view; this renders the same report as text so the agent can check
// "is cron actually armed, is an MCP server down, did the last turn cache"
// instead of inferring its runtime state from config files — which the run
// history shows it doing, at length, wrongly.

// RegisterStatusTool gives sess a status tool returning the runtime-condition
// digest. Headless sessions (subagents, cron) are skipped like the other
// front-end host tools: their job is the task, not the installation.
func RegisterStatusTool(sess hostToolRegistrar, digest func() string) error {
	if sess.Headless() {
		return nil
	}
	return sess.RegisterHostTool(shell3.HostTool{
		Name: "status",
		Description: "Your own runtime condition: config warnings, whether the command gate and cron " +
			"scheduler are armed (with each job's last run), background-job slots, MCP server health, " +
			"the last turn's token usage (and its cache-hit share), and recent alerts. Check this " +
			"FIRST when something about the installation seems wrong — it reports the live process, " +
			"not the files on disk.",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
		Handler: func(context.Context, string) (string, error) {
			return digest(), nil
		},
	})
}

// statusDigest renders the runtime condition as compact text for the agent.
func (s *Server) statusDigest() string {
	r := s.buildStatus()
	var b strings.Builder

	fmt.Fprintf(&b, "shell3 %s · up %s · config %s\n", r.Version, (time.Duration(r.Uptime) * time.Second).String(), r.ConfigDir)
	fmt.Fprintf(&b, "agent: %s · model %s · context window %d\n", r.Agent.Name, r.Agent.Model, r.Agent.ContextWindow)

	gate := "NOT ARMED — the shell runs ungated"
	if r.GateArmed {
		gate = "armed"
	}
	fmt.Fprintf(&b, "command gate: %s\n", gate)

	if r.Usage != nil {
		line := fmt.Sprintf("last turn: %d prompt + %d completion tokens", r.Usage.Prompt, r.Usage.Completion)
		if r.Usage.Cached > 0 && r.Usage.Prompt > 0 {
			line += fmt.Sprintf(" (%d%% of prompt was cache-hit)", r.Usage.Cached*100/r.Usage.Prompt)
		}
		b.WriteString(line + "\n")
	}

	if len(r.Warnings) > 0 {
		fmt.Fprintf(&b, "config warnings (%d):\n", len(r.Warnings))
		for _, w := range r.Warnings {
			fmt.Fprintf(&b, "  ! %s\n", w)
		}
	} else {
		b.WriteString("config warnings: none\n")
	}

	// Cron: armed-vs-declared is the distinction that matters — a job list
	// with no scheduler behind it fires nothing.
	source, _ := s.cronFuncs()
	switch {
	case source == nil && len(r.Cron) == 0:
		b.WriteString("cron: no jobs declared\n")
	case source == nil:
		fmt.Fprintf(&b, "cron: %d job(s) DECLARED BUT NOT ARMED — nothing fires; a reload should arm them\n", len(r.Cron))
	default:
		fmt.Fprintf(&b, "cron: armed, %d job(s)\n", len(r.Cron))
	}
	for _, j := range r.Cron {
		last := "never run"
		if j.Last != "" {
			last = "last run " + j.Last
		}
		fmt.Fprintf(&b, "  - %s: %s → %s · %s\n", j.Name, j.Schedule, j.Agent, last)
	}

	fmt.Fprintf(&b, "background jobs: %d running of %d slots\n", r.Jobs.Running, r.Jobs.Capacity)

	if len(r.MCP) == 0 {
		b.WriteString("mcp: none configured\n")
	} else {
		for _, m := range r.MCP {
			if m.Connected {
				fmt.Fprintf(&b, "mcp %s: up, %d tools\n", m.Name, m.Tools)
			} else {
				fmt.Fprintf(&b, "mcp %s: DOWN (%s) — its tools are absent this session\n", m.Name, m.Error)
			}
		}
	}

	// Recent alerts are the failures the user was shown; the agent should
	// know about them too rather than hearing them second-hand.
	alerts := []notification{}
	for _, n := range s.recentNotices() {
		if n.Kind == "alert" {
			alerts = append(alerts, n)
		}
	}
	if len(alerts) > 0 {
		max := len(alerts)
		if max > 5 {
			alerts = alerts[max-5:]
		}
		fmt.Fprintf(&b, "recent alerts (%d shown):\n", len(alerts))
		for _, a := range alerts {
			fmt.Fprintf(&b, "  ⚠ %s %s: %s\n", a.At, a.Title, a.Body)
		}
	}

	return strings.TrimRight(b.String(), "\n")
}
