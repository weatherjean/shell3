//go:build unix

package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/weatherjean/shell3/internal/store"
)

func newListProjectsCommand() *cobra.Command {
	var configPath string
	var page, pageSize int
	cmd := &cobra.Command{
		Use:   "list-projects",
		Short: "List projects (distinct) with workdir and last activity",
		RunE: func(cmd *cobra.Command, args []string) error {
			dbPath, err := canonicalDBPath(configPath)
			if err != nil {
				return fmt.Errorf("list-projects: resolve db: %w", err)
			}
			st, err := store.Open(dbPath)
			if err != nil {
				return fmt.Errorf("list-projects: open store: %w", err)
			}
			defer func() { _ = st.Close() }()
			ps, err := st.ListProjects(pageSize, page*pageSize)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			for _, p := range ps {
				fmt.Fprintf(out, "%s\t%s\t%d sessions\tlast %s\n", p.UUID, p.Workdir, p.SessionCount, p.LastActivity)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&configPath, "config", "c", "", "Config name or *.lua path (anchors the canonical DB; default: ~/.shell3)")
	cmd.Flags().IntVar(&page, "page", 0, "Zero-based page index")
	cmd.Flags().IntVar(&pageSize, "page-size", 20, "Projects per page")
	return cmd
}
