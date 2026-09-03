//go:build unix

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/weatherjean/shell3/internal/paths"
)

type runtimePaths struct {
	config  string
	workDir string
}

var userHomeDir = os.UserHomeDir

func defaultRuntimePaths() (runtimePaths, error) {
	home, err := userHomeDir()
	if err != nil {
		return runtimePaths{}, fmt.Errorf("shell3: resolve home directory: %w", err)
	}
	base := filepath.Join(home, ".shell3")
	return runtimePaths{
		config:  filepath.Join(base, "shell3.lisp"),
		workDir: filepath.Join(base, "workdir"),
	}, nil
}

func resolveRuntimePaths(cmd *cobra.Command, config, workDir string, here bool) (runtimePaths, error) {
	configSet := cmd.Flags().Changed("config")
	workDirSet := cmd.Flags().Changed("workdir")
	if here && (configSet || workDirSet) {
		return runtimePaths{}, errors.New("--here cannot be combined with --config or --workdir")
	}
	if !here && configSet != workDirSet {
		return runtimePaths{}, errors.New("--config and --workdir must be provided together")
	}
	if here {
		cwd, err := os.Getwd()
		if err != nil {
			return runtimePaths{}, fmt.Errorf("shell3: get working directory: %w", err)
		}
		config = filepath.Join(cwd, "shell3.lisp")
		workDir = cwd
	} else if !configSet {
		defaults, err := defaultRuntimePaths()
		if err != nil {
			return runtimePaths{}, err
		}
		config = defaults.config
		workDir = defaults.workDir
	}
	config, err := filepath.Abs(config)
	if err != nil {
		return runtimePaths{}, fmt.Errorf("shell3: resolve config: %w", err)
	}
	workDir, err = filepath.Abs(workDir)
	if err != nil {
		return runtimePaths{}, fmt.Errorf("shell3: resolve workdir: %w", err)
	}
	return runtimePaths{config: config, workDir: workDir}, nil
}

func addRuntimeFlags(cmd *cobra.Command, config, workDir *string, here *bool) {
	cmd.Flags().StringVarP(config, "config", "c", "", "Path to shell3.lisp (requires --workdir)")
	cmd.Flags().StringVar(workDir, "workdir", "", "Runtime and tool working directory (requires --config)")
	cmd.Flags().BoolVar(here, "here", false, "Use ./shell3.lisp and the current directory")
}

func resolveWorkDir(cmd *cobra.Command, workDir string, here bool) (string, error) {
	if here && cmd.Flags().Changed("workdir") {
		return "", errors.New("--here cannot be combined with --workdir")
	}
	if here {
		var err error
		workDir, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("shell3: get working directory: %w", err)
		}
	} else if !cmd.Flags().Changed("workdir") {
		defaults, err := defaultRuntimePaths()
		if err != nil {
			return "", err
		}
		workDir = defaults.workDir
	}
	workDir, err := filepath.Abs(workDir)
	if err != nil {
		return "", fmt.Errorf("shell3: resolve workdir: %w", err)
	}
	return workDir, nil
}

func resolveStateRoot(cmd *cobra.Command, stateRoot, workDir string, here bool) (string, error) {
	if cmd.Flags().Changed("state") {
		if here || cmd.Flags().Changed("workdir") {
			return "", errors.New("--state cannot be combined with --here or --workdir")
		}
		return filepath.Abs(stateRoot)
	}
	resolved, err := resolveWorkDir(cmd, workDir, here)
	if err != nil {
		return "", err
	}
	return paths.NewLocal(resolved).Root, nil
}
