//go:build unix

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/weatherjean/shell3/internal/config"
	"github.com/weatherjean/shell3/internal/kit"
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

	// A kit is the config when present: report its agents and validate every
	// declared tool, so a broken manifest fails here rather than at 3am.
	kitPath := filepath.Join(path, config.KitFileName)
	if _, statErr := os.Stat(kitPath); statErr == nil {
		res, cerr := kit.Check(ctx, kitPath)
		if cerr != nil {
			return fmt.Errorf("health: %w", cerr)
		}
		for _, prob := range res.Problems {
			fmt.Fprintln(out, "kit: "+prob)
		}
		if !res.OK() {
			return fmt.Errorf("health: %s has %d problem(s)", config.KitFileName, len(res.Problems))
		}
		src, rerr := os.ReadFile(kitPath)
		if rerr != nil {
			return fmt.Errorf("health: %w", rerr)
		}
		k, perr := kit.Parse(src)
		if perr != nil {
			return fmt.Errorf("health: %w", perr)
		}
		fatCtx := 0
		for i, ka := range k.Agents {
			role := "employee"
			if i == 0 {
				role = "main"
			}
			r, rrerr := k.Resolve(ka, i == 0)
			if rrerr != nil {
				return fmt.Errorf("health: %w", rrerr)
			}
			skillDir := filepath.Join(path, "skills")
			if i > 0 {
				skillDir = filepath.Join(path, "projects", ka.Name, "skills")
			}
			nSkills := len(config.ScanSkills(skillDir))
			fmt.Fprintf(out, "agent: %s (%s, model %s, %d tools, %d skills, %d tests)\n",
				ka.Name, role, ka.Model, len(r.Tools), nSkills, len(ka.Tests))
			// The kit load path never validated `context:` at all, which is
			// how a 90 KB brain file ran for weeks unnoticed. Resolve against
			// the agent's OWN workdir, exactly as kitagent.go does.
			//
			// Only an OVER-CAP file fails: that one is losing content from the
			// prompt, which is a defect. A merely large file is reported and
			// tolerated — it is working, just expensive, and health going red
			// on a legitimately big brain file would train the operator to
			// ignore this whole check.
			for _, w := range config.ContextSizeWarnings(agentContextBase(path, ka.Workdir), ka.Context) {
				if w.OverCap {
					fatCtx++
				}
				fmt.Fprintf(out, "agent %s: %s\n", ka.Name, w)
			}
		}
		// A cron tool: job names no agent, so it is validated against the
		// whole kit here rather than any agent's Resolved set — an unknown
		// tool, or one that fireTool could never satisfy (a required param
		// it never supplies), must fail now, not at 3am on the first tick.
		badTools := 0
		for _, j := range lc.Cron() {
			if j.Tool == "" {
				continue
			}
			matches := k.ToolMatches(j.Tool)
			if len(matches) == 0 {
				badTools++
				fmt.Fprintf(out, "cron: cron/%s.md names tool %q, which the kit does not declare\n", j.Name, j.Tool)
				continue
			}
			if len(matches) > 1 {
				// Two scopes may each legally declare the same tool name (the
				// duplicate check is per-scope, not kit-wide — see
				// Kit.ToolMatches). A cron tool job names no agent to
				// disambiguate, so ToolByName's first-match-wins pick would run
				// whichever function happened to parse first, silently, at 3am.
				badTools++
				scopes := make([]string, len(matches))
				for i, m := range matches {
					scopes[i] = m.Scope
				}
				fmt.Fprintf(out, "cron: cron/%s.md names tool %q, which is declared in more than one scope (%s) — rename one so resolution is unambiguous\n", j.Name, j.Tool, strings.Join(scopes, ", "))
				continue
			}
			t := matches[0].Tool
			for pname, p := range t.Params {
				if p.Required {
					badTools++
					fmt.Fprintf(out, "cron: cron/%s.md runs tool %q, which requires argument %q — a cron tool job passes no arguments\n", j.Name, j.Tool, pname)
				}
			}
		}
		if badTools > 0 {
			return fmt.Errorf("health: %d cron tool job problem(s)", badTools)
		}
		if fatCtx > 0 {
			return fmt.Errorf("health: %d oversized context file(s) — every turn re-reads them into the prompt", fatCtx)
		}
	} else {
		fmt.Fprintf(out, "agent: %s (model %s, %d skills, %d subagents)\n",
			a.Name, a.ModelName, len(a.Skills), len(a.Subagents))
		// Large-but-intact context files are reported here rather than as load
		// warnings, which health hardens into failures: same split as the kit
		// branch above, so both config shapes behave identically. An over-cap
		// file DID come through as a load warning and already failed above.
		for _, w := range config.ContextSizeWarnings(path, a.Context) {
			if !w.OverCap {
				fmt.Fprintf(out, "agent %s: %s\n", a.Name, w)
			}
		}
		// A tool: cron job under a markdown config (no kit at all) is
		// already refused by config.Load itself (its cron cross-reference
		// check), so there is nothing left to validate here.
	}
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
	// The telegram front-end's own start-up check, run here rather than found
	// out at `shell3 telegram`: health is documented as THE config check, and a
	// block boot wrote with blank fields loads cleanly but refuses to start. An
	// ABSENT block is not an error — an `shell3 ask`-only config is legitimate —
	// but say so. Deliberately LAST: a
	// blank chat id is a state boot itself writes ("fill it in later"), and it
	// must never hide the hook and MCP diagnostics above.
	if tg := lc.Telegram(); !tg.Present {
		fmt.Fprintln(out, "telegram: absent — the bot front-end is unwired (add a telegram: block to run `shell3 telegram`)")
	} else if chatID, err := telegramChatID(tg); err != nil {
		fmt.Fprintf(out, "telegram: %v\n", err)
		return fmt.Errorf("health: %w", err)
	} else {
		fmt.Fprintf(out, "telegram: chat %d\n", chatID)
	}

	fmt.Fprintln(out, "OK")
	return nil
}

// agentContextBase resolves the directory a kit agent's `context:` entries are
// read against — its own workdir when it declares one, the config dir
// otherwise. Mirrors agentsetup/kitagent.go's ctxBase so health inspects the
// same files the running agent loads; a divergence here would make health
// pass on a file the agent never reads.
func agentContextBase(configDir, workdir string) string {
	if workdir == "" {
		return configDir
	}
	if strings.HasPrefix(workdir, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, workdir[2:])
		}
	}
	if filepath.IsAbs(workdir) {
		return workdir
	}
	return filepath.Join(configDir, workdir)
}
