//go:build unix

package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/weatherjean/shell3/internal/luacfg"
	"github.com/weatherjean/shell3/internal/mcp"
)

// newHealthCommand builds `shell3 health` — a strict, read-only config check.
// It loads shell3.lua exactly like the bot would and reports every problem the
// running bot tolerates leniently: warnings such as a skipped skill file
// (bad/missing frontmatter) fail the check here, so `shell3 health` is the
// place to look when something silently didn't take effect.
func newHealthCommand() *cobra.Command {
	var configPath string
	cmd := &cobra.Command{
		Use:   "health",
		Short: "Check the config: load shell3.lua and fail on any warning",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			resolved, err := resolveConfig(configPath)
			if err != nil {
				return err
			}
			return runHealth(cmd, resolved)
		},
	}
	addConfigFlag(cmd, &configPath)
	return cmd
}

// runHealth loads the config at path and prints a verdict (SilenceUsage: a
// failure means the config is broken, not the invocation).
func runHealth(cmd *cobra.Command, path string) error {
	cmd.SilenceUsage = true
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "config: %s\n", path)
	lc, err := luacfg.Load(path)
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
	// Connect every declared MCP server, exactly like the bot would at
	// startup. The running bot tolerates a down server (warning, tools
	// absent); health is the strict view, so any down server fails here.
	if servers := lc.MCPServers(); len(servers) > 0 {
		m := mcp.New(servers, nil)
		defer m.Close()
		m.Connect(cmd.Context())
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
