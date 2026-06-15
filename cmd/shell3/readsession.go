//go:build unix

package main

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/weatherjean/shell3/internal/store"
)

func newReadSessionCommand() *cobra.Command {
	var page, pageSize int
	cmd := &cobra.Command{
		Use:   "read-session <session-id>",
		Short: "Dump a past session's full transcript in chronological order (read-only)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("read-session: invalid session id %q: %w", args[0], err)
			}
			dbPath, err := canonicalDBPath()
			if err != nil {
				return fmt.Errorf("read-session: resolve db: %w", err)
			}
			st, err := store.Open(dbPath)
			if err != nil {
				return fmt.Errorf("read-session: open store: %w", err)
			}
			defer func() { _ = st.Close() }()
			turns, err := st.SessionTurns(id)
			if err != nil {
				return fmt.Errorf("read-session: load turns: %w", err)
			}
			if pageSize <= 0 {
				pageSize = 50
			}
			lo := page * pageSize
			if lo < 0 {
				lo = 0
			}
			hi := lo + pageSize
			if lo > len(turns) {
				lo = len(turns)
			}
			if hi > len(turns) {
				hi = len(turns)
			}
			out := cmd.OutOrStdout()
			for _, t := range turns[lo:hi] {
				fmt.Fprintf(out, "=== %s | %s ===\n%s\n\n",
					t.CreatedAt.Format("2006-01-02T15:04:05Z"), t.Role, t.Content)
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&page, "page", 0, "Zero-based page index")
	cmd.Flags().IntVar(&pageSize, "page-size", 50, "Turns per page")
	return cmd
}
