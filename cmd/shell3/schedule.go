//go:build unix

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/bootstrap"
	"github.com/weatherjean/shell3/internal/inbox"
	"github.com/weatherjean/shell3/internal/lispconfig"
	"github.com/weatherjean/shell3/internal/paths"
	"github.com/weatherjean/shell3/internal/runs"
	scheduler "github.com/weatherjean/shell3/internal/schedule"
	"github.com/weatherjean/shell3/internal/wrk"
)

type scheduleResources struct {
	configPath string
	workDir    string
	cfg        *lispconfig.Config
	store      *runs.Store
	log        applog.Logger
	logCloser  interface{ Close() error }
}

func openScheduleResources(configPath, workDir string) (*scheduleResources, error) {
	var err error
	configPath, err = filepath.Abs(configPath)
	if err != nil {
		return nil, fmt.Errorf("schedule: resolve config: %w", err)
	}
	workDir, err = filepath.Abs(workDir)
	if err != nil {
		return nil, fmt.Errorf("schedule: resolve workdir: %w", err)
	}
	cfg, err := lispconfig.Load(configPath)
	if err != nil {
		return nil, err
	}
	if _, err := scheduler.Resolve(configPath, cfg); err != nil {
		return nil, err
	}
	local := paths.NewLocal(workDir)
	if err := bootstrap.EnsureProject(local); err != nil {
		return nil, err
	}
	log, logCloser, err := applog.Open(local.Errors)
	if err != nil {
		return nil, err
	}
	store, err := runs.Open(local.Root)
	if err != nil {
		_ = logCloser.Close()
		return nil, err
	}
	return &scheduleResources{configPath: configPath, workDir: workDir, cfg: cfg, store: store, log: log, logCloser: logCloser}, nil
}

func (r *scheduleResources) Close() {
	if r == nil {
		return
	}
	_ = r.store.Close()
	_ = r.logCloser.Close()
}

// newServiceCommand is the foreground headless host intended for launchd or
// systemd. Platform installation stays an explicit operator action.
func newServiceCommand() *cobra.Command {
	var configPath, workDir string
	var here bool
	cmd := &cobra.Command{
		Use:   "service",
		Short: "Run the persistent headless schedule and workflow host",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			resolved, err := resolveRuntimePaths(cmd, configPath, workDir, here)
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			resources, err := openScheduleResources(resolved.config, resolved.workDir)
			if err != nil {
				return err
			}
			defer resources.Close()
			mailbox := inbox.Store{Root: filepath.Join(resources.workDir, ".shell3_project")}
			listener, err := inbox.StartListener(ctx, mailbox)
			if err != nil {
				return err
			}
			defer listener.Close()
			router, err := wrk.StartRouter(ctx, mailbox.Root, listener.Hints(), resources.log)
			if err != nil {
				return err
			}
			defer router.Close()
			manager, err := scheduler.Start(ctx, resources.configPath, resources.workDir, resources.cfg, resources.store, resources.log)
			if err != nil {
				return err
			}
			defer manager.Close()
			fmt.Fprintf(cmd.OutOrStdout(), "shell3 service: %d schedule(s) active in %s\n", len(resources.cfg.Schedules), resources.workDir)
			<-ctx.Done()
			return nil
		},
	}
	addRuntimeFlags(cmd, &configPath, &workDir, &here)
	return cmd
}

func newScheduleCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "schedule", Short: "Run and inspect declared schedules"}
	cmd.AddCommand(newScheduleListCommand())
	cmd.AddCommand(newScheduleRunCommand())
	cmd.AddCommand(newScheduleHistoryCommand())
	return cmd
}

type scheduleListRecord struct {
	Name     string `json:"name"`
	Cron     string `json:"cron"`
	Timezone string `json:"timezone"`
	Wrkfile  string `json:"wrkfile"`
	Task     string `json:"task"`
	Output   string `json:"output"`
	Timeout  string `json:"timeout"`
	Overlap  string `json:"overlap"`
	Notify   string `json:"notify"`
}

func newScheduleListCommand() *cobra.Command {
	var configPath, workDir string
	var here bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "Print declared schedules as JSONL",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			resolved, err := resolveRuntimePaths(cmd, configPath, workDir, here)
			if err != nil {
				return err
			}
			cfg, err := lispconfig.Load(resolved.config)
			if err != nil {
				return err
			}
			jobs, err := scheduler.Resolve(resolved.config, cfg)
			if err != nil {
				return err
			}
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetEscapeHTML(false)
			for _, job := range jobs {
				declaration := job.Config
				record := scheduleListRecord{
					Name:     declaration.Name,
					Cron:     declaration.Cron,
					Timezone: declaration.Timezone,
					Wrkfile:  job.Wrkfile,
					Task:     job.Task,
					Output:   declaration.Output,
					Timeout:  declaration.Timeout.String(),
					Overlap:  declaration.Overlap,
					Notify:   declaration.Notify,
				}
				if err := enc.Encode(record); err != nil {
					return err
				}
			}
			return nil
		},
	}
	addRuntimeFlags(cmd, &configPath, &workDir, &here)
	return cmd
}

func newScheduleRunCommand() *cobra.Command {
	var configPath, workDir string
	var here bool
	cmd := &cobra.Command{
		Use:   "run <name>",
		Short: "Run one declared schedule immediately",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if os.Getenv("SHELL3_WRK_WORKER") == "1" {
				return errors.New("schedule: nested scheduled launch denied: dispatched agents are leaf workers")
			}
			resolved, err := resolveRuntimePaths(cmd, configPath, workDir, here)
			if err != nil {
				return err
			}
			resources, err := openScheduleResources(resolved.config, resolved.workDir)
			if err != nil {
				return err
			}
			defer resources.Close()
			executor, err := scheduler.NewExecutor(resources.configPath, resources.workDir, resources.cfg, resources.store, resources.log)
			if err != nil {
				return err
			}
			run, runErr := executor.Run(cmd.Context(), args[0], "manual", time.Now().UTC())
			if run.ID != "" {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetEscapeHTML(false)
				if err := enc.Encode(run); err != nil {
					return err
				}
			}
			return runErr
		},
	}
	addRuntimeFlags(cmd, &configPath, &workDir, &here)
	return cmd
}

func newScheduleHistoryCommand() *cobra.Command {
	var workDir, status string
	var here bool
	var limit int
	cmd := &cobra.Command{
		Use:   "history [name]",
		Short: "Print schedule run history as JSONL",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if status != "" && status != "running" && status != "done" && status != "failed" {
				return fmt.Errorf("schedule: status must be running, done, or failed")
			}
			if limit < 0 {
				return fmt.Errorf("schedule: limit must not be negative")
			}
			root, err := resolveWorkDir(cmd, workDir, here)
			if err != nil {
				return err
			}
			store, err := runs.Open(paths.NewLocal(root).Root)
			if err != nil {
				return err
			}
			defer store.Close()
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			history, err := store.ListScheduleRuns(name, status, limit)
			if err != nil {
				return err
			}
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetEscapeHTML(false)
			for _, run := range history {
				if err := enc.Encode(run); err != nil {
					return err
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&workDir, "workdir", "", "Directory containing schedule history (default ~/.shell3/workdir)")
	cmd.Flags().BoolVar(&here, "here", false, "Use the current directory")
	cmd.Flags().StringVar(&status, "status", "", "Filter by running, done, or failed")
	cmd.Flags().IntVar(&limit, "limit", 50, "Maximum records to print (0 for all)")
	return cmd
}
