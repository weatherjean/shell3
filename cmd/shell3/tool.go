package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/weatherjean/shell3/internal/kit"
)

// newToolCommand builds `shell3 tool`: the kit author's loop — check a kit
// parses and lints, run one tool against a real payload with no model in the
// way, and run the kit's declared tests.
func newToolCommand() *cobra.Command {
	c := &cobra.Command{
		Use:   "tool",
		Short: "Check, run, and test the tools declared in a kit",
		Long: "Author's toolchain for kit files. `check` validates syntax, lint,\n" +
			"and declarations; `run` invokes one tool with a JSON payload and no\n" +
			"session; `test` runs the kit's declared tests under the harness.",
	}
	c.AddCommand(newToolCheckCommand(), newToolRunCommand(), newToolTestCommand())
	return c
}

func newToolCheckCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "check <kit.sh>",
		Short: "Validate a kit's syntax, lint, and declarations",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := kit.Check(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			for _, p := range res.Problems {
				fmt.Fprintln(out, p)
			}
			if !res.OK() {
				return fmt.Errorf("%s: %d problem(s)", args[0], len(res.Problems))
			}
			note := ""
			if !res.Shellcheck {
				note = " (shellcheck not installed — lint skipped)"
			}
			fmt.Fprintf(out, "%s: ok%s\n", args[0], note)
			return nil
		},
	}
}

func newToolRunCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "run <kit.sh> <tool> [json]",
		Short: "Invoke one declared tool with a JSON payload",
		Long: "Runs a single tool outside any session: no model, no tokens, no\n" +
			"conversation. This is the probe half of the author's loop.",
		Args: cobra.RangeArgs(2, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			tool, err := findTool(args[0], args[1])
			if err != nil {
				return err
			}
			payload := map[string]any{}
			if len(args) == 3 && args[2] != "" {
				if err := json.Unmarshal([]byte(args[2]), &payload); err != nil {
					return fmt.Errorf("payload is not valid JSON: %w", err)
				}
			}

			out, runErr := kit.Runner{Path: args[0]}.Run(cmd.Context(), tool, payload)
			fmt.Fprint(cmd.OutOrStdout(), out)
			return runErr
		},
	}
}

func newToolTestCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "test <kit.sh> [tool]",
		Short: "Run a kit's declared tests",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			k, err := loadKit(args[0])
			if err != nil {
				return err
			}
			only := ""
			if len(args) == 2 {
				only = args[1]
			}

			out := cmd.OutOrStdout()
			total, failed := 0, false
			for _, a := range k.Agents {
				res, err := k.RunTests(cmd.Context(), args[0], a, only)
				if err != nil {
					return err
				}
				if res.Ran == 0 {
					continue
				}
				fmt.Fprintf(out, "# %s\n%s", a.Name, res.Output)
				total += res.Ran
				failed = failed || res.Failed
			}
			if total == 0 {
				fmt.Fprintln(out, "no tests declared")
				return nil
			}
			if failed {
				return fmt.Errorf("%s: tests failed", args[0])
			}
			fmt.Fprintf(out, "%d test(s) passed\n", total)
			return nil
		},
	}
}

func loadKit(path string) (*kit.Kit, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read kit: %w", err)
	}
	k, err := kit.Parse(src)
	if err != nil {
		return nil, err
	}
	return k, nil
}

// findTool resolves a declared tool name across every agent and shared group in
// the kit. Names are unique within a scope; the first match wins, which is what
// a caller naming a tool means.
func findTool(path, name string) (kit.Tool, error) {
	k, err := loadKit(path)
	if err != nil {
		return kit.Tool{}, err
	}
	t, ok := k.ToolByName(name)
	if !ok {
		return kit.Tool{}, fmt.Errorf("kit %s declares no tool %q", path, name)
	}
	return t, nil
}
