package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/weatherjean/shell3/internal/lispconfig"
	"github.com/weatherjean/shell3/internal/paths"
	"github.com/weatherjean/shell3/internal/runner"
	"github.com/weatherjean/shell3/internal/wrk"
	"golang.org/x/term"
)

func newWrkCommand() *cobra.Command {
	c := &cobra.Command{
		Use:   "wrk",
		Short: "Check and operate durable agent workflows",
	}
	c.AddCommand(newWrkCheckCommand())
	c.AddCommand(newWrkCompileCommand())
	c.AddCommand(newWrkRunCommand())
	c.AddCommand(newWrkBeatCommand())
	c.AddCommand(newWrkStatusCommand())
	c.AddCommand(newWrkSignalCommand())
	c.AddCommand(newWrkCancelCommand())
	c.AddCommand(newWrkAgentCommand())
	return c
}

func newWrkRunCommand() *cobra.Command {
	var configPath, stateRoot, runID, shell3Bin, notifyTo, notifyState string
	c := &cobra.Command{
		Use:   "run <file.wrk.lisp> [request]",
		Short: "Start and drive a durable workflow until terminal or waiting",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if os.Getenv("SHELL3_WRK_WORKER") == "1" {
				return fmt.Errorf("wrk: nested workflow launch denied: dispatched agents are leaf workers")
			}
			request := ""
			if len(args) == 2 {
				request = args[1]
			} else {
				input := cmd.InOrStdin()
				interactive := false
				if file, ok := input.(*os.File); ok {
					interactive = term.IsTerminal(int(file.Fd()))
				}
				var err error
				request, err = readWrkRequest(input, interactive)
				if err != nil {
					return err
				}
			}
			runDir, err := wrk.Start(configPath, args[0], wrk.StartOptions{
				StateRoot: stateRoot, RunID: runID, Shell3Bin: shell3Bin, Request: request,
				NotifyTo: notifyTo, NotifyState: notifyState,
			})
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "started %s\n", runDir)
			for {
				beat, err := wrk.BeatWithProgress(cmd.Context(), runDir, cmd.OutOrStdout())
				fmt.Fprintf(cmd.OutOrStdout(), "beat %s/%s: %s", beat.Task, beat.RunID, beat.Status)
				if len(beat.Ran) > 0 {
					fmt.Fprintf(cmd.OutOrStdout(), " (%s)", strings.Join(beat.Ran, ", "))
				}
				fmt.Fprintln(cmd.OutOrStdout())
				if err != nil {
					return err
				}
				switch beat.Status {
				case "completed":
					fmt.Fprintf(cmd.OutOrStdout(), "completed %s (%s)\n", beat.Task, beat.RunID)
					return nil
				case "failed":
					return fmt.Errorf("wrk: workflow %s/%s failed", beat.Task, beat.RunID)
				case "waiting":
					return nil
				}
			}
		},
	}
	c.Flags().StringVarP(&configPath, "config", "c", "shell3.lisp", "Path to shell3.lisp")
	c.Flags().StringVar(&stateRoot, "state", "", "Override workflow state root")
	c.Flags().StringVar(&runID, "run-id", "", "Use an explicit run identifier")
	c.Flags().StringVar(&shell3Bin, "shell3-bin", "", "Override shell3 executable used by compiled Bash")
	c.Flags().StringVar(&notifyTo, "notify-to", "", "Notify this destination when the workflow finishes")
	c.Flags().StringVar(&notifyState, "notify-state", "", "State root used for the completion notification")
	return c
}

func readWrkRequest(input io.Reader, interactive bool) (string, error) {
	if interactive {
		return "", nil
	}
	data, err := io.ReadAll(io.LimitReader(input, (1<<20)+1))
	if err != nil {
		return "", err
	}
	if len(data) > 1<<20 {
		return "", fmt.Errorf("wrk: request exceeds %d bytes", 1<<20)
	}
	return strings.TrimSuffix(string(data), "\n"), nil
}

func newWrkBeatCommand() *cobra.Command {
	var stateRoot string
	c := &cobra.Command{
		Use:   "beat <run-id|task/run-id>",
		Short: "Advance one runnable workflow wave and exit",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if os.Getenv("SHELL3_WRK_WORKER") == "1" {
				return fmt.Errorf("wrk: nested workflow beat denied: dispatched agents are leaf workers")
			}
			runDir, err := wrk.ResolveRun(stateRoot, args[0])
			if err != nil {
				return err
			}
			beat, err := wrk.BeatWithProgress(cmd.Context(), runDir, cmd.OutOrStdout())
			fmt.Fprintf(cmd.OutOrStdout(), "%s/%s: %s", beat.Task, beat.RunID, beat.Status)
			if len(beat.Ran) > 0 {
				fmt.Fprintf(cmd.OutOrStdout(), " (%s)", strings.Join(beat.Ran, ", "))
			}
			fmt.Fprintln(cmd.OutOrStdout())
			if err != nil {
				return err
			}
			if beat.Status == "failed" {
				return fmt.Errorf("wrk: workflow %s/%s failed", beat.Task, beat.RunID)
			}
			return nil
		},
	}
	c.Flags().StringVar(&stateRoot, "state", paths.NewLocal(".").Wrk, "Workflow state root")
	return c
}

func newWrkStatusCommand() *cobra.Command {
	var stateRoot string
	c := &cobra.Command{
		Use:   "status <run-id|task/run-id>",
		Short: "Print a durable workflow state snapshot as JSON",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			runDir, err := wrk.ResolveRun(stateRoot, args[0])
			if err != nil {
				return err
			}
			snapshot, err := wrk.Inspect(runDir)
			if err != nil {
				return err
			}
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetEscapeHTML(false)
			enc.SetIndent("", "  ")
			return enc.Encode(snapshot)
		},
	}
	c.Flags().StringVar(&stateRoot, "state", paths.NewLocal(".").Wrk, "Workflow state root")
	return c
}

func newWrkSignalCommand() *cobra.Command {
	var stateRoot string
	c := &cobra.Command{
		Use:   "signal <run-id|task/run-id> <event> [message]",
		Short: "Persist an external event for a waiting workflow",
		Args:  cobra.RangeArgs(2, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			if os.Getenv("SHELL3_WRK_WORKER") == "1" {
				return fmt.Errorf("wrk: nested workflow signal denied: dispatched agents are leaf workers")
			}
			runDir, err := wrk.ResolveRun(stateRoot, args[0])
			if err != nil {
				return err
			}
			body := ""
			if len(args) == 3 {
				body = args[2]
			}
			event, err := wrk.Signal(runDir, args[1], body)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s/%s: recorded %s (%s)\n", filepath.Base(filepath.Dir(runDir)), filepath.Base(runDir), event.Name, event.ID)
			return nil
		},
	}
	c.Flags().StringVar(&stateRoot, "state", paths.NewLocal(".").Wrk, "Workflow state root")
	return c
}

func newWrkCancelCommand() *cobra.Command {
	var stateRoot string
	c := &cobra.Command{
		Use:   "cancel <run-id|task/run-id>",
		Short: "Durably cancel a workflow and interrupt its active beat",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if os.Getenv("SHELL3_WRK_WORKER") == "1" {
				return fmt.Errorf("wrk: nested workflow cancellation denied: dispatched agents are leaf workers")
			}
			runDir, err := wrk.ResolveRun(stateRoot, args[0])
			if err != nil {
				return err
			}
			if err := wrk.Cancel(runDir); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s/%s: cancelled\n", filepath.Base(filepath.Dir(runDir)), filepath.Base(runDir))
			return nil
		},
	}
	c.Flags().StringVar(&stateRoot, "state", paths.NewLocal(".").Wrk, "Workflow state root")
	return c
}

func newWrkAgentCommand() *cobra.Command {
	var configPath, agent, workdir, runDir string
	var rawSlots []string
	c := &cobra.Command{
		Use:    "_agent",
		Short:  "Execute one resolved external agent invocation",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			prompt, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), (4<<20)+1))
			if err != nil {
				return err
			}
			if len(prompt) > 4<<20 {
				return fmt.Errorf("wrk: prompt exceeds %d bytes", 4<<20)
			}
			cfg, err := lispconfig.Load(configPath)
			if err != nil {
				return err
			}
			slots := map[string]string{}
			for _, raw := range rawSlots {
				name, value, ok := strings.Cut(raw, "=")
				if !ok || name == "" {
					return fmt.Errorf("wrk: slot must be name=value, got %q", raw)
				}
				slots[name] = value
			}
			result, err := (runner.Executor{Config: cfg}).Run(cmd.Context(), runner.Request{
				Agent: agent, Prompt: string(prompt), WorkDir: workdir, RunDir: runDir, Slots: slots,
				Progress: cmd.ErrOrStderr(),
			})
			if err != nil {
				return err
			}
			_, err = fmt.Fprint(cmd.OutOrStdout(), result.Text)
			return err
		},
	}
	c.Flags().StringVar(&configPath, "config", "shell3.lisp", "Path to shell3.lisp")
	c.Flags().StringVar(&agent, "agent", "", "Configured agent name")
	c.Flags().StringVar(&workdir, "workdir", ".", "Agent working directory")
	c.Flags().StringVar(&runDir, "run-dir", "", "Invocation state directory")
	c.Flags().StringArrayVar(&rawSlots, "slot", nil, "Runtime value in name=value form")
	_ = c.MarkFlagRequired("agent")
	_ = c.MarkFlagRequired("run-dir")
	return c
}

func newWrkCompileCommand() *cobra.Command {
	var configPath, outputPath string
	c := &cobra.Command{
		Use:   "compile <file.wrk.lisp>",
		Short: "Compile a resolved wrkfile to executable Bash",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := lispconfig.Load(configPath)
			if err != nil {
				return err
			}
			definition, err := wrk.Load(args[0], cfg)
			if err != nil {
				return err
			}
			script, err := wrk.Compile(definition, configPath)
			if err != nil {
				return err
			}
			if outputPath == "" {
				_, err = fmt.Fprint(cmd.OutOrStdout(), script)
				return err
			}
			if err := os.WriteFile(outputPath, []byte(script), 0o700); err != nil {
				return fmt.Errorf("write compiled wrkfile: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s: compiled %s\n", outputPath, args[0])
			return nil
		},
	}
	c.Flags().StringVarP(&configPath, "config", "c", "shell3.lisp", "Path to shell3.lisp")
	c.Flags().StringVarP(&outputPath, "output", "o", "", "Write executable Bash to this path")
	return c
}

func newWrkCheckCommand() *cobra.Command {
	var configPath string
	c := &cobra.Command{
		Use:   "check <file.wrk.lisp>",
		Short: "Parse, resolve, and strictly validate a wrkfile",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := lispconfig.Load(configPath)
			if err != nil {
				return err
			}
			definition, err := wrk.Load(args[0], cfg)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s: ok (task %q, %d node(s), parallel %d)\n",
				args[0], definition.Name, len(definition.Nodes), definition.Parallel)
			return nil
		},
	}
	c.Flags().StringVarP(&configPath, "config", "c", "shell3.lisp", "Path to shell3.lisp")
	return c
}
