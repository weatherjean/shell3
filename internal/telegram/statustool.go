//go:build unix

package telegram

import (
	"context"
	"fmt"
	"strings"

	"github.com/weatherjean/shell3/internal/shell3"
)

// registerStatusTool gives the agent a `status` tool reporting where its config
// lives and what is currently active, the orientation a self-editing agent needs
// before changing shell3.lua and calling reload. Telegram-only host tool.
func (b *Bot) registerStatusTool() {
	_ = b.sess.RegisterHostTool(shell3.HostTool{
		Name: "status",
		Description: "Report your runtime status: the absolute path of the shell3.lua " +
			"config file you can edit, your active agent and the agents available, the " +
			"model, the working directory, and any scheduled cron jobs. Call this to find " +
			"your config file before editing it (see the self-evolve skill).",
		Parameters: map[string]any{"type": "object", "properties": map[string]any{}},
		Handler:    b.statusToolHandler,
	})
}

func (b *Bot) statusToolHandler(ctx context.Context, argsJSON string) (string, error) {
	var sb strings.Builder

	cfgPath, err := b.rt.ConfigPath()
	if err != nil {
		cfgPath = "(could not resolve — run 'shell3 boot'?)"
	}
	fmt.Fprintf(&sb, "config: %s\n", cfgPath)

	snap := b.sess.Snapshot()
	fmt.Fprintf(&sb, "agent: %s (available: %s)\n",
		b.sess.ActiveAgent(), strings.Join(b.sess.AgentNames(), ", "))
	if snap.Model != "" {
		fmt.Fprintf(&sb, "model: %s\n", snap.Model)
	}

	wd := b.workDir
	if wd == "" {
		wd = "(default)"
	}
	fmt.Fprintf(&sb, "workdir: %s\n", wd)

	jobs := b.rt.Cron()
	if len(jobs) == 0 {
		sb.WriteString("cron: none")
	} else {
		names := make([]string, len(jobs))
		for i, j := range jobs {
			names[i] = j.Name
		}
		fmt.Fprintf(&sb, "cron: %d job(s) — %s", len(jobs), strings.Join(names, ", "))
	}
	return sb.String(), nil
}
