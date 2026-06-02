package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/weatherjean/shell3/internal/paths"
	"github.com/weatherjean/shell3/internal/tui"
)

type runFlags struct {
	configPath string
	outPath    string
}

func newRunCommand() *cobra.Command {
	f := &runFlags{}
	cmd := &cobra.Command{
		Use:   "shell3 [message]",
		Short: "Run the shell3 chat agent",
		RunE: func(cmd *cobra.Command, args []string) error {
			input := strings.TrimSpace(strings.Join(args, " "))
			if input == "" && !term.IsTerminal(int(os.Stdin.Fd())) {
				b, err := io.ReadAll(os.Stdin)
				if err != nil {
					return fmt.Errorf("read stdin: %w", err)
				}
				input = strings.TrimSpace(string(b))
			}
			return runChat(cmd.Context(), f, input)
		},
	}
	cmd.Flags().StringVarP(&f.configPath, "config", "c", "", "Path to shell3.lua (default: ./shell3.lua, else ~/.shell3/shell3.lua)")
	cmd.Flags().StringVar(&f.outPath, "out", "", "Stream a JSONL audit log of this run to <path>. Enables headless mode.")
	return cmd
}

// resolveConfigPath returns the shell3.lua to load: the explicit flag, else
// ./shell3.lua if it exists, else ~/.shell3/shell3.lua if it exists. Returns
// an error when nothing is found.
func resolveConfigPath(flag, cwd, homeDir string) (string, error) {
	if flag != "" {
		return flag, nil
	}
	local := filepath.Join(cwd, "shell3.lua")
	if fileExists(local) {
		return local, nil
	}
	global := filepath.Join(homeDir, ".shell3", "shell3.lua")
	if fileExists(global) {
		return global, nil
	}
	return "", fmt.Errorf("no shell3.lua found — pass --config or create ~/.shell3/shell3.lua")
}

func runChat(ctx context.Context, f *runFlags, initialInput string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("get home directory: %w", err)
	}

	configPath, err := resolveConfigPath(f.configPath, cwd, homeDir)
	if err != nil {
		return err
	}

	headless := f.outPath != "" || (!term.IsTerminal(int(os.Stdin.Fd())) && initialInput != "")
	if headless {
		_ = os.Setenv("SHELL3_HEADLESS", "1")
		if f.outPath != "" {
			_ = os.Setenv("SHELL3_OUT", f.outPath)
		}
	}

	g := paths.NewGlobal(homeDir)
	log, logCloser := openAppLog(g.LogFile)
	defer logCloser.Close()

	cfg, _, cleanup, err := buildChatConfig(configPath, cwd, homeDir, f.outPath, headless, log)
	if err != nil {
		return err
	}
	defer cleanup()

	if initialInput != "" {
		return tui.RunOnce(ctx, cfg, initialInput)
	}
	return tui.RunInteractive(ctx, cfg)
}
