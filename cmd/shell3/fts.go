//go:build unix

package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/weatherjean/shell3/internal/store"
)

func newFTSCommand() *cobra.Command {
	var configPath, projectID string
	var page, pageSize int
	cmd := &cobra.Command{
		Use:   "fts [query]",
		Short: "Full-text search conversation history (read-only).",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dbPath, err := canonicalDBPath(configPath)
			if err != nil {
				return fmt.Errorf("fts: resolve db: %w", err)
			}
			st, err := store.Open(dbPath)
			if err != nil {
				return fmt.Errorf("fts: open store: %w", err)
			}
			defer func() { _ = st.Close() }()
			expr := store.BuildFTSExpr(args, false)
			res, err := st.HistorySearchExpr(expr, projectID, pageSize, page*pageSize)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			for _, h := range res.Hits {
				fmt.Fprintf(out, "%d\t%s\t%s\t%s\n", h.SessionID,
					h.CreatedAt.Format("2006-01-02T15:04:05Z"), h.Role, ftsSnippet(h.Content))
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&configPath, "config", "c", "", "Path to shell3.lua (anchors the canonical DB; default: ~/.shell3).")
	cmd.Flags().StringVar(&projectID, "project-id", "", "Scope to one project UUID (default: all projects).")
	cmd.Flags().IntVar(&page, "page", 0, "Zero-based page index.")
	cmd.Flags().IntVar(&pageSize, "page-size", 20, "Results per page.")
	return cmd
}

func ftsSnippet(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 160 {
		return s[:160] + "…"
	}
	return s
}
