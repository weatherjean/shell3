//go:build unix

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/weatherjean/shell3/internal/agentsetup"
	"github.com/weatherjean/shell3/internal/config"
	"github.com/weatherjean/shell3/internal/kit"
	"github.com/weatherjean/shell3/internal/mcp"
)

// newHealthCommand builds `shell3 health`, a strict read-only config check.
// It loads the config directory exactly as the bot would and fails on every
// problem the bot tolerates — a skipped skill file, say — so this is where to
// look when something silently did not take effect.
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

// runHealth loads the config at path and prints a verdict. SilenceUsage: a
// failure means the config is broken, not the invocation.
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
	warns := lc.Warnings()
	for _, w := range warns {
		fmt.Fprintln(out, "warning: "+w)
	}
	if len(warns) > 0 {
		return fmt.Errorf("health: config loaded with %d warning(s)", len(warns))
	}
	// Report the kit's agents and validate every declared tool, so a broken
	// manifest fails here rather than at 3am. agentNames feeds the dry-run.
	agentNames, err := checkKit(ctx, out, path, lc)
	if err != nil {
		return err
	}
	// Dry-run every hook with a probe. A script failure surfaces as a
	// fail-closed verdict whose reason carries "hook error:" — a broken
	// script, which fails health. A deliberate block is fine: the gate is
	// just strict.
	brokenHooks := 0
	for _, name := range agentNames {
		if lc.ToolCallHookFor(name) == "" {
			continue
		}
		v := lc.RunToolCall(ctx, name, "health_probe", "", "{}", true)
		if v.Action == config.ActionBlock && strings.Contains(v.Reason, "hook error") {
			brokenHooks++
			fmt.Fprintf(out, "hook (%s tool-call): %s\n", name, v.Reason)
		}
	}
	for _, name := range agentNames {
		if outp := lc.RunToolResult(ctx, name, "health_probe", "{}", "probe"); strings.Contains(outp, "hook failed") {
			brokenHooks++
			fmt.Fprintf(out, "hook (%s tool-result): %s\n", name, outp)
		}
	}
	// Commands and event subscribers are ACTION hooks, so health checks only
	// that they are defined: dry-running a command would post the message it
	// exists to post, every time.
	for _, p := range lc.VerifyHooks(ctx) {
		brokenHooks++
		fmt.Fprintf(out, "hook (%s)\n", p)
	}
	if brokenHooks > 0 {
		return fmt.Errorf("health: %d broken hook script(s)", brokenHooks)
	}
	// Connect every declared server as the bot would. The bot tolerates a
	// down one; health is the strict view, so it fails here.
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
	// The front-end's own start-up check, run here rather than discovered at
	// `shell3 telegram`: a block boot wrote with blank fields loads cleanly
	// and refuses to start. An ABSENT block is reported, not failed — an
	// ask-only config is legitimate. LAST on purpose: a blank chat id is a
	// state boot itself writes, and must not hide the diagnostics above.
	if tg := lc.Telegram(); !tg.Present {
		fmt.Fprintln(out, "telegram: absent — the bot front-end is unwired (add a telegram: block to run `shell3 telegram`)")
	} else if chatID, err := telegramHomeChat(tg); err != nil {
		fmt.Fprintf(out, "telegram: %v\n", err)
		return fmt.Errorf("health: %w", err)
	} else {
		fmt.Fprintf(out, "telegram: home chat %d\n", chatID)
		if tg.ChatID == "" {
			// Fell back to a user id, and a bot cannot open a DM, so this
			// delivers only once that person has written. A warning, not a
			// failure: true today and false tomorrow with no config change.
			fmt.Fprintf(out, "telegram: no chat_id set — cron results will DM user %d, "+
				"which only works once they have messaged the bot\n", chatID)
		}
	}

	fmt.Fprintln(out, "OK")
	return nil
}

// homeDir is what a ~/-prefixed workdir expands against, "" if the OS will
// not say, which leaves the path unexpanded rather than guessed.
func homeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}

// checkKit reports the kit's agents, cron jobs, skills and context files, and
// returns the agent names for the dry-run. Anything that makes the install
// untrustworthy — a kit that does not lint, an unusable skill, an over-cap
// context file — is an error.
func checkKit(ctx context.Context, out io.Writer, path string, lc *config.LoadedConfig) ([]string, error) {
	kitPath := filepath.Join(path, kit.FileName)
	res, cerr := kit.Check(ctx, kitPath)
	if cerr != nil {
		return nil, fmt.Errorf("health: %w", cerr)
	}
	for _, prob := range res.Problems {
		fmt.Fprintln(out, "kit: "+prob)
	}
	if !res.OK() {
		return nil, fmt.Errorf("health: %s has %d problem(s)", kit.FileName, len(res.Problems))
	}
	// The kit config.Load already parsed, not a second read that could
	// disagree with the config being reported on.
	k := lc.Kit()
	agentNames := make([]string, 0, len(k.Agents))
	for _, ka := range k.Agents {
		agentNames = append(agentNames, ka.Name)
	}
	// Install the gate and note as agentsetup.LoadKit does, so the dry-run
	// exercises them; config.Load lifts the wiring and nothing else.
	if h := agentsetup.KitHooksOf(k); !h.Empty() {
		main := ""
		if len(k.Agents) > 0 {
			main = k.Agents[0].Name
		}
		lc.SetKitHooks(kitPath, main, h)
	}
	fatCtx, badSkills := 0, 0
	for i, ka := range k.Agents {
		role := "employee"
		if i == 0 {
			role = "main"
		}
		r, rrerr := k.Resolve(ka, i == 0)
		if rrerr != nil {
			return nil, fmt.Errorf("health: %w", rrerr)
		}
		skillDir := filepath.Join(path, "skills")
		if i > 0 {
			skillDir = filepath.Join(path, "projects", ka.Name, "skills")
		}
		skills, skillWarns := config.ScanSkillsChecked(skillDir)
		for _, w := range skillWarns {
			badSkills++
			fmt.Fprintln(out, "skills: "+w)
		}
		fmt.Fprintf(out, "agent: %s (%s, model %s, %d tools, %d skills, %d tests)\n",
			ka.Name, role, ka.Model, len(r.Tools), len(skills), len(ka.Tests))
		// The load path validates context: not at all, which is how a 90 KB
		// brain file ran for weeks unnoticed. Resolved against the agent's
		// OWN workdir, as kitagent.go does.
		//
		// Only an OVER-CAP file fails — that one is losing content from the
		// prompt. A merely large file is reported and tolerated: it works,
		// just expensively, and going red on a legitimately big brain file
		// would train the operator to ignore this check.
		for _, w := range config.ContextSizeWarnings(agentsetup.AgentContextBase(path, homeDir(), ka.Workdir), ka.Context) {
			if w.OverCap {
				fatCtx++
			}
			fmt.Fprintf(out, "agent %s: %s\n", ka.Name, w)
		}
	}
	// Cron jobs are `cron:` blocks in the kit, so every check health used
	// to run here — an unknown agent, a job naming the removed tool: kind —
	// is a kit.Parse load error, which the Parse above already returned. All
	// that is left is reporting.
	for _, j := range k.Crons {
		fmt.Fprintf(out, "cron: %s (%s, agent %s)\n", j.Name, j.Schedule, j.Agent)
	}
	if badSkills > 0 {
		return nil, fmt.Errorf("health: %d unusable skill file(s) — they are silently absent from the prompt", badSkills)
	}
	if fatCtx > 0 {
		return nil, fmt.Errorf("health: %d oversized context file(s) — every turn re-reads them into the prompt", fatCtx)
	}
	return agentNames, nil
}
