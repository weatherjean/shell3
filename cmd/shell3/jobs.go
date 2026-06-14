//go:build unix

package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/weatherjean/shell3/internal/store"
)

func newJobsCommand() *cobra.Command {
	var configPath, workdir string
	var page, pageSize int
	cmd := &cobra.Command{
		Use:   "jobs",
		Short: "List tracked background jobs for a workdir (read-only; dead jobs auto-pruned).",
		RunE: func(cmd *cobra.Command, args []string) error {
			dbPath, err := canonicalDBPath(configPath)
			if err != nil {
				return fmt.Errorf("jobs: resolve db: %w", err)
			}
			wd := workdir
			if wd == "" {
				if wd, err = os.Getwd(); err != nil {
					return fmt.Errorf("jobs: cwd: %w", err)
				}
			}
			st, err := store.Open(dbPath)
			if err != nil {
				return fmt.Errorf("jobs: open store: %w", err)
			}
			defer func() { _ = st.Close() }()
			jobs, err := st.ListJobs(wd, pageSize, page*pageSize)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			for _, j := range jobs {
				fmt.Fprintf(out, "%s\tpid:%d\t%s\t%s\n", j.ID, j.PID, j.Log, j.Cmd)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&configPath, "config", "c", "", "Config name or *.lua path (anchors the canonical DB; default: ~/.shell3).")
	cmd.Flags().StringVar(&workdir, "workdir", "", "Workdir whose jobs to list (default: current directory).")
	cmd.Flags().IntVar(&page, "page", 0, "Zero-based page index.")
	cmd.Flags().IntVar(&pageSize, "page-size", 50, "Jobs per page.")
	return cmd
}
