//go:build unix

package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/weatherjean/shell3/internal/paths"
	"github.com/weatherjean/shell3/internal/runs"
)

func newReadSessionCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "read-session <session-id>",
		Short: "Dump a past session's full transcript in chronological order (read-only)",
		Example: `  shell3 read-session 20260701T120000.000000000-abcd
  ls .shell3_project/runs/   # list session ids`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("read-session: get cwd: %w", err)
			}
			local := paths.NewLocal(cwd)
			st, err := runs.Open(local.Root)
			if err != nil {
				return fmt.Errorf("read-session: open runs: %w", err)
			}
			msgs, err := st.LoadMessages(id)
			if err != nil {
				return fmt.Errorf("read-session: load messages: %w", err)
			}
			if len(msgs) == 0 {
				// LoadMessages returns (nil, nil) for a missing file, so an empty
				// result is ambiguous: distinguish a genuinely empty session from a
				// nonexistent id and fail loudly on the latter.
				sessions, _ := st.ListSessions(0)
				found := false
				for _, s := range sessions {
					if s.ID == id {
						found = true
						break
					}
				}
				if !found {
					return fmt.Errorf("read-session: no session %q under %s", id, local.Root)
				}
			}
			out := cmd.OutOrStdout()
			for _, m := range msgs {
				fmt.Fprintf(out, "=== %s ===\n%s\n\n", m.Role, m.Content)
			}
			return nil
		},
	}
	return cmd
}
