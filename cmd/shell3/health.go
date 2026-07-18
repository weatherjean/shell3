//go:build unix

package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/weatherjean/shell3/internal/config"
	"github.com/weatherjean/shell3/internal/mcp"
)

// newHealthCommand builds `shell3 health` — a strict, read-only config check.
// It loads the config directory exactly like the bot would and reports every problem the
// running bot tolerates leniently: warnings such as a skipped skill file
// (bad/missing frontmatter) fail the check here, so `shell3 health` is the
// place to look when something silently didn't take effect.
func newHealthCommand() *cobra.Command {
	var configDir string
	cmd := &cobra.Command{
		Use:   "health",
		Short: "Check the config: load the config directory and fail on any warning",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			resolved, err := resolveConfig(configDir)
			if err != nil {
				return err
			}
			return runHealth(cmd, resolved)
		},
	}
	addConfigFlag(cmd, &configDir)
	return cmd
}

// runHealth loads the config at path and prints a verdict (SilenceUsage: a
// failure means the config is broken, not the invocation).
func runHealth(cmd *cobra.Command, path string) error {
	cmd.SilenceUsage = true
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "config: %s\n", path)
	lc, err := config.Load(path)
	if err != nil {
		return fmt.Errorf("health: %w", err)
	}
	defer lc.Close()
	warns := lc.Warnings()
	for _, w := range warns {
		fmt.Fprintln(out, "warning: "+w)
	}
	if len(warns) > 0 {
		return fmt.Errorf("health: config loaded with %d warning(s)", len(warns))
	}
	a := lc.FirstAgent()
	fmt.Fprintf(out, "agent: %s (model %s, %d skills, %d subagents)\n",
		a.Name, a.ModelName, len(a.Skills), len(a.Subagents))
	// Dry-run every discovered hook with a probe payload. A script failure
	// (nonzero exit, bad verdict JSON, timeout) surfaces as a fail-closed
	// verdict whose reason carries "hook error:"/"hook failed:" — that's a
	// broken script and fails health. A deliberate block/ask on the probe is
	// fine: the gate is just strict.
	agents := append([]string{a.Name}, a.Subagents...)
	brokenHooks := 0
	for _, name := range agents {
		if lc.ToolCallHookFor(name) == "" {
			continue
		}
		v := lc.RunToolCall(ctx, name, "health_probe", "", "{}", true)
		if v.Action == config.ActionBlock && strings.Contains(v.Reason, "hook error") {
			brokenHooks++
			fmt.Fprintf(out, "hook (%s tool-call): %s\n", name, v.Reason)
		}
	}
	for _, name := range agents {
		if outp := lc.RunToolResult(ctx, name, "health_probe", "{}", "probe"); strings.Contains(outp, "hook failed") {
			brokenHooks++
			fmt.Fprintf(out, "hook (%s tool-result): %s\n", name, outp)
		}
	}
	if brokenHooks > 0 {
		return fmt.Errorf("health: %d broken hook script(s)", brokenHooks)
	}
	// Connect every declared MCP server, exactly like the bot would at
	// startup. The running bot tolerates a down server (warning, tools
	// absent); health is the strict view, so any down server fails here.
	if servers := lc.MCPServers(); len(servers) > 0 {
		m := mcp.New(servers, nil)
		defer m.Close()
		m.Connect(ctx)
		down := 0
		for _, st := range m.Status() {
			if st.Up {
				fmt.Fprintf(out, "mcp %s: ok (%d tools)\n", st.Name, st.ToolCount)
			} else {
				down++
				fmt.Fprintf(out, "mcp %s: down: %s\n", st.Name, st.Err)
			}
		}
		if down > 0 {
			return fmt.Errorf("health: %d MCP server(s) down", down)
		}
	}
	fmt.Fprintln(out, "OK")
	return nil
}
