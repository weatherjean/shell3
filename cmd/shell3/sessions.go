//go:build unix

package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/weatherjean/shell3/internal/store"
)

func newListSessionsCommand() *cobra.Command {
	var configPath, projectID string
	var page, pageSize int
	cmd := &cobra.Command{
		Use:   "list-sessions",
		Short: "List conversation sessions (newest first), optionally scoped to a project.",
		RunE: func(cmd *cobra.Command, args []string) error {
			dbPath, err := canonicalDBPath(configPath)
			if err != nil {
				return fmt.Errorf("list-sessions: resolve db: %w", err)
			}
			st, err := store.Open(dbPath)
			if err != nil {
				return fmt.Errorf("list-sessions: open store: %w", err)
			}
			defer func() { _ = st.Close() }()
			sessions, err := st.ListSessionsPage(projectID, pageSize, page*pageSize)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			for _, s := range sessions {
				parent := "-"
				if s.ParentID != 0 {
					parent = fmt.Sprintf("%d", s.ParentID)
				}
				cfg := s.ConfigPath
				if cfg == "" {
					cfg = "-"
				}
				fmt.Fprintf(out, "%d\t%s\tparent:%s\t%d msgs\t%s\t%s\tcfg:%s\n",
					s.ID, s.Status, parent, s.NumMsgs,
					s.StartedAt.Format("2006-01-02T15:04:05Z"), s.Preview, cfg)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&configPath, "config", "c", "", "Path to shell3.lua (anchors the canonical DB; default: ~/.shell3).")
	cmd.Flags().StringVar(&projectID, "project-id", "", "Scope to one project UUID (default: all projects).")
	cmd.Flags().IntVar(&page, "page", 0, "Zero-based page index.")
	cmd.Flags().IntVar(&pageSize, "page-size", 50, "Sessions per page.")
	return cmd
}
