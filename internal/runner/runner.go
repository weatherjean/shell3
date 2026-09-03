//go:build unix

// Package runner executes external agent harnesses from resolved shell3.lisp
// runner declarations.
package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/weatherjean/shell3/internal/lispconfig"
	"github.com/weatherjean/shell3/internal/procutil"
)

const (
	defaultTimeout = 30 * time.Minute
	waitDelay      = 2 * time.Second
	maxResultBytes = 4 << 20
	maxErrorBytes  = 16 << 10
)

const leafInstructions = `You are a leaf worker inside a shell3 wrk run. Perform the assigned work directly in the task root. Do not invoke shell3 wrk, dispatch another agent, or create a nested workflow; the parent run owns orchestration and verification.`

type Request struct {
	Agent    string
	Prompt   string
	WorkDir  string
	RunDir   string
	Slots    map[string]string
	Progress io.Writer
}

type Result struct {
	Text string
}

type Executor struct {
	Config *lispconfig.Config
}

func (e Executor) Run(ctx context.Context, req Request) (Result, error) {
	if e.Config == nil {
		return Result{}, errors.New("runner: configuration is required")
	}
	agent, ok := e.Config.Agents[req.Agent]
	if !ok {
		return Result{}, fmt.Errorf("runner: unknown agent %q", req.Agent)
	}
	if strings.TrimSpace(req.WorkDir) == "" || strings.TrimSpace(req.RunDir) == "" {
		return Result{}, errors.New("runner: workdir and run directory are required")
	}
	workdir, err := filepath.Abs(req.WorkDir)
	if err != nil {
		return Result{}, fmt.Errorf("runner: resolve workdir: %w", err)
	}
	runDir, err := filepath.Abs(req.RunDir)
	if err != nil {
		return Result{}, fmt.Errorf("runner: resolve run directory: %w", err)
	}
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		return Result{}, fmt.Errorf("runner: create run directory: %w", err)
	}
	result := Result{}
	promptPath := filepath.Join(runDir, "prompt.md")
	resultPath := filepath.Join(runDir, "result.md")
	stdoutPath := filepath.Join(runDir, "stdout.log")
	stderrPath := filepath.Join(runDir, "stderr.log")
	promptParts := []string{leafInstructions}
	promptParts = append(promptParts, req.Prompt)
	promptText := strings.Join(promptParts, "\n\n")
	if err := os.WriteFile(promptPath, []byte(promptText), 0o600); err != nil {
		return result, fmt.Errorf("runner: write prompt: %w", err)
	}
	_ = os.Remove(resultPath)
	slots := make(map[string]string, len(req.Slots)+4)
	for k, v := range req.Slots {
		slots[k] = v
	}
	slots["agent-name"] = req.Agent
	slots["prompt-file"] = promptPath
	slots["result-file"] = resultPath
	slots["workdir"] = workdir
	argv, err := e.Config.Argv(req.Agent, slots)
	if err != nil {
		return result, err
	}
	if len(argv) == 0 {
		return result, errors.New("runner: resolved an empty command")
	}

	prompt, err := os.Open(promptPath)
	if err != nil {
		return result, fmt.Errorf("runner: open prompt: %w", err)
	}
	defer prompt.Close()
	stdout, err := os.OpenFile(stdoutPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return result, fmt.Errorf("runner: create stdout log: %w", err)
	}
	defer stdout.Close()
	stderr, err := os.OpenFile(stderrPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return result, fmt.Errorf("runner: create stderr log: %w", err)
	}
	defer stderr.Close()

	configured := e.Config.Runners[agent.Runner]
	timeout := configured.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, argv[0], argv[1:]...)
	cmd.Dir = workdir
	cmd.Env = append(runnerEnvironment(e.Config), taskEnvironment(slots)...)
	cmd.Stdin = prompt
	progress := req.Progress
	if progress != nil {
		progress = &lockedWriter{w: progress}
		cmd.Stdout = io.MultiWriter(stdout, progress)
	} else {
		cmd.Stdout = stdout
	}
	switch configured.Stderr {
	case "merge":
		if progress != nil {
			cmd.Stderr = io.MultiWriter(stdout, progress)
		} else {
			cmd.Stderr = stdout
		}
	case "discard":
		cmd.Stderr = io.Discard
	default:
		if progress != nil {
			cmd.Stderr = io.MultiWriter(stderr, progress)
		} else {
			cmd.Stderr = stderr
		}
	}
	procutil.ConfigureGroupCancel(cmd, waitDelay)
	runErr := cmd.Run()
	exitCode := -1
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}
	_ = stdout.Sync()
	_ = stderr.Sync()
	if runErr != nil || exitCode != configured.SuccessExit {
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			return result, fmt.Errorf("runner: agent %q timed out after %s", req.Agent, timeout)
		}
		detail := tailFile(stderrPath, maxErrorBytes)
		if detail == "" {
			detail = tailFile(stdoutPath, maxErrorBytes)
		}
		if detail == "" && runErr != nil {
			detail = runErr.Error()
		}
		return result, fmt.Errorf("runner: agent %q exited with code %d: %s", req.Agent, exitCode, strings.TrimSpace(detail))
	}
	resultSource := stdoutPath
	if configured.Result == "file" {
		resultSource = resultPath
	}
	text, err := readLimited(resultSource, maxResultBytes)
	if err != nil {
		return result, fmt.Errorf("runner: read result: %w", err)
	}
	result.Text = text
	return result, nil
}

type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.w.Write(p)
}

func runnerEnvironment(cfg *lispconfig.Config) []string {
	secrets := map[string]bool{}
	if cfg != nil {
		for _, model := range cfg.Models {
			if model.APIKeyEnv != "" {
				secrets[model.APIKeyEnv] = true
			}
		}
		if cfg.Telegram != nil && cfg.Telegram.TokenEnv != "" {
			secrets[cfg.Telegram.TokenEnv] = true
		}
	}
	inherited := os.Environ()
	out := make([]string, 0, len(inherited))
	for _, entry := range inherited {
		name, _, _ := strings.Cut(entry, "=")
		if !secrets[name] {
			out = append(out, entry)
		}
	}
	return out
}

func taskEnvironment(slots map[string]string) []string {
	names := map[string]string{
		"task-artifacts": "TASK_ARTIFACTS",
		"task-attempt":   "TASK_ATTEMPT",
		"task-id":        "TASK_ID",
		"task-root":      "TASK_ROOT",
		"task-run":       "TASK_RUN",
	}
	out := make([]string, 0, len(names)+1)
	out = append(out, "SHELL3_WRK_WORKER=1")
	for slot, env := range names {
		if value, ok := slots[slot]; ok {
			out = append(out, env+"="+value)
		}
	}
	return out
}

func readLimited(path string, limit int64) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return "", err
	}
	if int64(len(data)) > limit {
		return "", fmt.Errorf("output exceeds %d bytes", limit)
	}
	return string(data), nil
}

func tailFile(path string, limit int64) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return ""
	}
	start := info.Size() - limit
	if start < 0 {
		start = 0
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return ""
	}
	data, _ := io.ReadAll(io.LimitReader(f, limit))
	return string(data)
}
