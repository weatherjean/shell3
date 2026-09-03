//go:build unix

package wrk

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/weatherjean/shell3/internal/inbox"
	"github.com/weatherjean/shell3/internal/lispconfig"
	"github.com/weatherjean/shell3/internal/procutil"
)

const runVersion = 2

// ErrBeatOwned means another process or goroutine is already advancing the
// run. Callers may safely leave it for the current owner or a later retry.
var ErrBeatOwned = errors.New("wrk: another beat owns this run")

type StartOptions struct {
	StateRoot   string
	RunID       string
	Shell3Bin   string
	Request     string
	NotifyTo    string
	NotifyState string
	// ExpectedTask pins a caller's pre-admission task identity. It prevents a
	// changed wrkfile from creating a run somewhere other than its ledger path.
	ExpectedTask string
	// Timeout bounds the entire invocation, including time spent waiting for
	// external events. The shorter of this and the task's own timeout wins.
	Timeout time.Duration
	// RequiredOutput is a clean relative path beneath the run's artifacts
	// directory. A workflow cannot complete successfully without this file.
	RequiredOutput string
}

type Manifest struct {
	Version        int       `json:"version"`
	Task           string    `json:"task"`
	RunID          string    `json:"run_id"`
	Created        time.Time `json:"created"`
	Deadline       time.Time `json:"deadline,omitzero"`
	WorkDir        string    `json:"workdir"`
	Request        string    `json:"request"`
	ConfigHash     string    `json:"config_sha256"`
	DefinitionHash string    `json:"definition_sha256"`
	Shell3Bin      string    `json:"shell3_bin"`
	NotifyTo       string    `json:"notify_to,omitempty"`
	NotifyState    string    `json:"notify_state,omitempty"`
	RequiredOutput string    `json:"required_output,omitempty"`
}

type BeatResult struct {
	Task   string
	RunID  string
	Status string
	Ran    []string
}

// Start snapshots the validated Lisp inputs and creates a new immutable run.
func Start(configPath, definitionPath string, opts StartOptions) (string, error) {
	cfg, err := lispconfig.Load(configPath)
	if err != nil {
		return "", err
	}
	def, err := Load(definitionPath, cfg)
	if err != nil {
		return "", err
	}
	if opts.ExpectedTask != "" && def.Name != opts.ExpectedTask {
		return "", fmt.Errorf("wrk: task changed from %q to %q; restart the schedule owner", opts.ExpectedTask, def.Name)
	}
	workdir := def.Root
	if !filepath.IsAbs(workdir) {
		workdir = filepath.Join(filepath.Dir(def.Path), workdir)
	}
	workdir, err = filepath.Abs(workdir)
	if err != nil {
		return "", fmt.Errorf("wrk: resolve task root: %w", err)
	}
	if opts.StateRoot == "" {
		opts.StateRoot = filepath.Join(workdir, ".shell3_project", "wrk")
	}
	opts.StateRoot, err = filepath.Abs(opts.StateRoot)
	if err != nil {
		return "", fmt.Errorf("wrk: resolve state root: %w", err)
	}
	if opts.NotifyState != "" {
		opts.NotifyState, err = filepath.Abs(opts.NotifyState)
		if err != nil {
			return "", fmt.Errorf("wrk: resolve notification state: %w", err)
		}
	}
	if opts.RunID == "" {
		opts.RunID, err = newRunID()
		if err != nil {
			return "", err
		}
	}
	if !validRunID(opts.RunID) {
		return "", fmt.Errorf("wrk: invalid run id %q", opts.RunID)
	}
	if opts.Shell3Bin == "" {
		opts.Shell3Bin, err = os.Executable()
		if err != nil {
			return "", fmt.Errorf("wrk: resolve shell3 executable: %w", err)
		}
	}
	if opts.RequiredOutput != "" {
		clean := filepath.Clean(opts.RequiredOutput)
		if filepath.IsAbs(opts.RequiredOutput) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return "", errors.New("wrk: required output must be relative to the artifacts directory")
		}
		opts.RequiredOutput = clean
	}
	configSource, err := os.ReadFile(configPath)
	if err != nil {
		return "", fmt.Errorf("wrk: read config snapshot: %w", err)
	}
	definitionSource, err := os.ReadFile(definitionPath)
	if err != nil {
		return "", fmt.Errorf("wrk: read definition snapshot: %w", err)
	}
	runDir := filepath.Join(opts.StateRoot, def.Name, opts.RunID)
	if _, err := os.Stat(runDir); err == nil {
		return "", fmt.Errorf("wrk: run %s/%s already exists", def.Name, opts.RunID)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := os.MkdirAll(filepath.Join(runDir, "nodes"), 0o700); err != nil {
		return "", fmt.Errorf("wrk: create run: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(runDir, "artifacts"), 0o700); err != nil {
		return "", err
	}
	configHash := sha256.Sum256(configSource)
	definitionHash := sha256.Sum256(definitionSource)
	created := time.Now().UTC()
	deadline := time.Time{}
	for _, timeout := range []time.Duration{def.Timeout, opts.Timeout} {
		if timeout > 0 {
			candidate := created.Add(timeout)
			if deadline.IsZero() || candidate.Before(deadline) {
				deadline = candidate
			}
		}
	}
	manifest := Manifest{
		Version: runVersion, Task: def.Name, RunID: opts.RunID, Created: created, Deadline: deadline,
		WorkDir: workdir, Request: opts.Request, ConfigHash: hex.EncodeToString(configHash[:]),
		DefinitionHash: hex.EncodeToString(definitionHash[:]), Shell3Bin: opts.Shell3Bin,
		NotifyTo: opts.NotifyTo, NotifyState: opts.NotifyState, RequiredOutput: opts.RequiredOutput,
	}
	for path, data := range map[string][]byte{
		filepath.Join(runDir, "shell3.lisp"):   configSource,
		filepath.Join(runDir, "task.wrk.lisp"): definitionSource,
		filepath.Join(runDir, "request.md"):    []byte(opts.Request + "\n"),
	} {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			return "", err
		}
	}
	for _, node := range def.Nodes {
		if err := writeStatus(filepath.Join(runDir, "nodes", node.Name), "pending"); err != nil {
			return "", err
		}
	}
	if err := writeJSON(filepath.Join(runDir, "run.json"), manifest); err != nil {
		return "", err
	}
	if err := atomicWrite(filepath.Join(runDir, "status"), []byte("ready\n")); err != nil {
		return "", err
	}
	if err := registerRun(runDir, manifest); err != nil {
		return "", err
	}
	return runDir, nil
}

// ResolveRun accepts task/run-id or a run id unique beneath stateRoot.
func ResolveRun(stateRoot, ref string) (string, error) {
	if strings.Contains(ref, "/") {
		parts := strings.Split(ref, "/")
		if len(parts) != 2 || !validRunID(parts[0]) || !validRunID(parts[1]) {
			return "", fmt.Errorf("wrk: run reference must be task/run-id")
		}
		return filepath.Join(stateRoot, parts[0], parts[1]), nil
	}
	if !validRunID(ref) {
		return "", fmt.Errorf("wrk: invalid run id %q", ref)
	}
	tasks, err := os.ReadDir(stateRoot)
	if err != nil {
		return "", fmt.Errorf("wrk: read state root: %w", err)
	}
	var matches []string
	for _, task := range tasks {
		if task.IsDir() {
			candidate := filepath.Join(stateRoot, task.Name(), ref)
			if _, err := os.Stat(filepath.Join(candidate, "run.json")); err == nil {
				matches = append(matches, candidate)
			}
		}
	}
	if len(matches) != 1 {
		return "", fmt.Errorf("wrk: run id %q matched %d runs; use task/run-id", ref, len(matches))
	}
	return matches[0], nil
}

// Beat advances one runnable wave or one loop attempt and then exits.
func Beat(ctx context.Context, runDir string) (BeatResult, error) {
	return beat(ctx, runDir, nil)
}

// BeatWithProgress advances one wave and mirrors foreground process output to progress.
func BeatWithProgress(ctx context.Context, runDir string, progress io.Writer) (BeatResult, error) {
	if progress != nil {
		progress = &progressWriter{w: progress}
	}
	return beat(ctx, runDir, progress)
}

func beat(ctx context.Context, runDir string, progress io.Writer) (BeatResult, error) {
	lock, err := lockRun(runDir)
	if err != nil {
		return BeatResult{}, err
	}
	defer unlockRun(lock)
	manifest, def, err := loadRun(runDir)
	if err != nil {
		return BeatResult{}, err
	}
	result := BeatResult{Task: manifest.Task, RunID: manifest.RunID}
	runStatus, err := readStatus(runDir)
	if err != nil {
		return result, err
	}
	switch runStatus {
	case "completed", "failed", "cancelled":
		result.Status = runStatus
		return result, notifyTerminal(runDir, manifest, runStatus)
	case "ready", "waiting":
	default:
		return result, fmt.Errorf("wrk: run has invalid status %q", runStatus)
	}
	if isCancelled(runDir) {
		return finishCancelled(runDir, manifest, result)
	}
	deadline := manifest.Deadline
	if deadline.IsZero() && def.Timeout > 0 {
		deadline = manifest.Created.Add(def.Timeout)
	}
	if !deadline.IsZero() && !time.Now().Before(deadline) {
		result.Status = "failed"
		if err := atomicWrite(filepath.Join(runDir, "status"), []byte("failed\n")); err != nil {
			return result, err
		}
		if err := notifyTerminal(runDir, manifest, "failed"); err != nil {
			return result, fmt.Errorf("wrk: task timeout exceeded; notify: %w", err)
		}
		return result, fmt.Errorf("wrk: task timeout exceeded")
	}
	beatParent := ctx
	cancelDeadline := func() {}
	if !deadline.IsZero() {
		beatParent, cancelDeadline = context.WithDeadline(ctx, deadline)
	}
	defer cancelDeadline()
	beatCtx, cancelBeat := context.WithCancel(beatParent)
	watchDone := make(chan struct{})
	defer close(watchDone)
	defer cancelBeat()
	go watchCancellation(runDir, cancelBeat, watchDone)
	states := map[string]string{}
	for _, node := range def.Nodes {
		state, err := readStatus(filepath.Join(runDir, "nodes", node.Name))
		if err != nil {
			return result, err
		}
		switch state {
		case "pending", "running", "waiting", "passed", "failed":
		default:
			return result, fmt.Errorf("wrk: node %s has invalid status %q", node.Name, state)
		}
		if state == "running" {
			state = "pending"
			if err := writeStatus(filepath.Join(runDir, "nodes", node.Name), state); err != nil {
				return result, err
			}
		}
		states[node.Name] = state
	}
	if err := ingestSignals(runDir, manifest); err != nil {
		return result, err
	}
	events, err := loadEvents(runDir)
	if err != nil {
		return result, err
	}
	for _, node := range def.Nodes {
		if node.Kind != WaitNode || states[node.Name] != "waiting" {
			continue
		}
		if event := matchingEvent(events, node.Event); event != nil {
			nodeDir := filepath.Join(runDir, "nodes", node.Name)
			if err := writeJSON(filepath.Join(nodeDir, "event.json"), event); err != nil {
				return result, err
			}
			if err := writeStatus(nodeDir, "passed"); err != nil {
				return result, err
			}
			states[node.Name] = "passed"
		}
	}
	if status := terminalStatus(states); status != "" {
		var terminalErr error
		if status == "completed" {
			terminalErr = validateRequiredOutput(runDir, manifest)
			if terminalErr != nil {
				status = "failed"
			}
		}
		result.Status = status
		if err := atomicWrite(filepath.Join(runDir, "status"), []byte(status+"\n")); err != nil {
			return result, err
		}
		notifyErr := notifyTerminal(runDir, manifest, status)
		if terminalErr != nil {
			if notifyErr != nil {
				return result, fmt.Errorf("%w; notify: %v", terminalErr, notifyErr)
			}
			return result, terminalErr
		}
		return result, notifyErr
	}
	var runnable []Node
	for _, node := range def.Nodes {
		if states[node.Name] != "pending" {
			continue
		}
		ready := true
		for _, dep := range node.After {
			if states[dep] != "passed" {
				ready = false
				break
			}
		}
		if ready {
			runnable = append(runnable, node)
		}
	}
	selected := selectRunnable(runnable, def.Parallel)
	if len(selected) == 0 {
		result.Status = "waiting"
		if err := atomicWrite(filepath.Join(runDir, "status"), []byte("waiting\n")); err != nil {
			return result, err
		}
		return result, nil
	}
	for _, node := range selected {
		if node.Kind == WaitNode {
			nodeDir := filepath.Join(runDir, "nodes", node.Name)
			state := "waiting"
			if event := matchingEvent(events, node.Event); event != nil {
				state = "passed"
				if err := writeJSON(filepath.Join(nodeDir, "event.json"), event); err != nil {
					return result, err
				}
			}
			states[node.Name] = state
			if err := writeStatus(nodeDir, state); err != nil {
				return result, err
			}
			result.Ran = append(result.Ran, node.Name)
		}
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	for _, node := range selected {
		if node.Kind == WaitNode {
			continue
		}
		node := node
		wg.Add(1)
		go func() {
			defer wg.Done()
			progressf(progress, "[wrk] %s: starting\n", node.Name)
			state, runErr := executeNode(beatCtx, runDir, manifest, node, progress)
			progressf(progress, "[wrk] %s: %s\n", node.Name, state)
			mu.Lock()
			states[node.Name] = state
			result.Ran = append(result.Ran, node.Name)
			if runErr != nil && firstErr == nil {
				firstErr = runErr
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	if isCancelled(runDir) {
		return finishCancelled(runDir, manifest, result)
	}
	sort.Strings(result.Ran)
	result.Status = terminalStatus(states)
	if result.Status == "" {
		result.Status = "ready"
		for _, state := range states {
			if state == "waiting" {
				result.Status = "waiting"
			}
		}
	}
	if result.Status == "completed" {
		if err := validateRequiredOutput(runDir, manifest); err != nil {
			result.Status = "failed"
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	if err := atomicWrite(filepath.Join(runDir, "status"), []byte(result.Status+"\n")); err != nil {
		return result, err
	}
	if result.Status == "completed" || result.Status == "failed" {
		if err := notifyTerminal(runDir, manifest, result.Status); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return result, firstErr
}

func validateRequiredOutput(runDir string, manifest Manifest) error {
	if manifest.RequiredOutput == "" {
		return nil
	}
	path := filepath.Join(runDir, "artifacts")
	parts := strings.Split(filepath.Clean(manifest.RequiredOutput), string(filepath.Separator))
	for i, part := range parts {
		path = filepath.Join(path, part)
		info, err := os.Lstat(path)
		if err != nil {
			return fmt.Errorf("wrk: required output %s: %w", path, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("wrk: required output contains a symlink: %s", path)
		}
		if i < len(parts)-1 && !info.IsDir() {
			return fmt.Errorf("wrk: required output path component is not a directory: %s", path)
		}
		if i == len(parts)-1 && !info.Mode().IsRegular() {
			return fmt.Errorf("wrk: required output is not a regular file: %s", path)
		}
	}
	return nil
}

func executeNode(ctx context.Context, runDir string, manifest Manifest, node Node, progress io.Writer) (string, error) {
	nodeDir := filepath.Join(runDir, "nodes", node.Name)
	if err := writeStatus(nodeDir, "running"); err != nil {
		return "failed", err
	}
	nodeCtx := ctx
	cancel := func() {}
	if node.Timeout > 0 {
		nodeCtx, cancel = context.WithTimeout(ctx, node.Timeout)
	}
	defer cancel()
	switch node.Kind {
	case AgentNode, LoopNode:
		attempt := nextAttempt(nodeDir)
		progressf(progress, "[wrk] %s: attempt %d with @%s\n", node.Name, attempt, node.Using)
		attemptDir := filepath.Join(nodeDir, fmt.Sprintf("attempt-%d", attempt))
		prompt := node.Prompt + "\n\nOriginal request:\n" + manifest.Request
		if previous := readOptional(filepath.Join(nodeDir, fmt.Sprintf("verify-%d.log", attempt-1))); previous != "" {
			prompt += "\n\nLatest verifier output:\n" + previous
		}
		args := []string{"wrk", "_agent", "--config", filepath.Join(runDir, "shell3.lisp"), "--agent", node.Using,
			"--workdir", manifest.WorkDir, "--run-dir", attemptDir,
			"--slot", "task-id=" + manifest.Task, "--slot", "task-run=" + manifest.RunID,
			"--slot", "task-root=" + manifest.WorkDir, "--slot", "task-artifacts=" + filepath.Join(runDir, "artifacts"),
			"--slot", fmt.Sprintf("task-attempt=%d", attempt)}
		cmd := exec.CommandContext(nodeCtx, manifest.Shell3Bin, args...)
		cmd.Stdin = strings.NewReader(prompt)
		var output strings.Builder
		// _agent reserves stdout for the final result. Its live runner stream is
		// carried on stderr so mirroring stdout here would print the result twice.
		cmd.Stdout = &output
		errOut, err := os.OpenFile(filepath.Join(attemptDir, "dispatch.err"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			_ = os.MkdirAll(attemptDir, 0o700)
			errOut, err = os.OpenFile(filepath.Join(attemptDir, "dispatch.err"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		}
		if err != nil {
			return "failed", err
		}
		defer errOut.Close()
		if progress != nil {
			cmd.Stderr = io.MultiWriter(errOut, progress)
		} else {
			cmd.Stderr = errOut
		}
		procutil.ConfigureGroupCancel(cmd, 2*time.Second)
		runErr := cmd.Run()
		_ = os.WriteFile(filepath.Join(nodeDir, fmt.Sprintf("result-%d.md", attempt)), []byte(output.String()), 0o600)
		if ctx.Err() != nil {
			_ = writeStatus(nodeDir, "pending")
			return "pending", ctx.Err()
		}
		if runErr != nil {
			return retryOrFail(nodeDir, node, attempt, fmt.Errorf("node %s agent attempt %d: %w", node.Name, attempt, runErr))
		}
		check := node.Accept
		if node.Kind == LoopNode {
			check = node.Until
		}
		if check != nil {
			progressf(progress, "[wrk] %s: verifying attempt %d\n", node.Name, attempt)
			ok, err := runCheck(nodeCtx, runDir, manifest, nodeDir, check, fmt.Sprintf("verify-%d.log", attempt), attempt, progress)
			if err != nil || !ok {
				if node.Kind == LoopNode {
					return retryOrFail(nodeDir, node, attempt, err)
				}
				_ = writeStatus(nodeDir, "failed")
				return "failed", err
			}
		}
	case CommandNode:
		ok, err := runShell(nodeCtx, manifest.WorkDir, taskEnv(runDir, manifest, 1), node.Run, filepath.Join(nodeDir, "command.log"), progress)
		if ctx.Err() != nil {
			_ = writeStatus(nodeDir, "pending")
			return "pending", ctx.Err()
		}
		if err != nil || !ok {
			_ = writeStatus(nodeDir, "failed")
			return "failed", err
		}
		if node.Accept != nil {
			progressf(progress, "[wrk] %s: verifying\n", node.Name)
			ok, err = runCheck(nodeCtx, runDir, manifest, nodeDir, node.Accept, "accept.log", 1, progress)
			if err != nil || !ok {
				_ = writeStatus(nodeDir, "failed")
				return "failed", err
			}
		}
	}
	if err := writeStatus(nodeDir, "passed"); err != nil {
		return "failed", err
	}
	return "passed", nil
}

func retryOrFail(nodeDir string, node Node, attempt int, err error) (string, error) {
	state := "failed"
	if node.Kind == LoopNode && attempt < node.Max {
		state = "pending"
	}
	_ = writeStatus(nodeDir, state)
	if state == "pending" {
		return state, nil
	}
	return state, err
}

func runCheck(ctx context.Context, runDir string, manifest Manifest, nodeDir string, check *Check, logName string, attempt int, progress io.Writer) (bool, error) {
	if check.Kind == "file" {
		_, err := os.Stat(filepath.Join(runDir, "artifacts", check.Value))
		return err == nil, nil
	}
	return runShell(ctx, manifest.WorkDir, taskEnv(runDir, manifest, attempt), check.Value, filepath.Join(nodeDir, logName), progress)
}

func runShell(ctx context.Context, dir string, env []string, script, logPath string, progress io.Writer) (bool, error) {
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return false, err
	}
	defer log.Close()
	cmd := exec.CommandContext(ctx, "bash", "-c", script)
	output := io.Writer(log)
	if progress != nil {
		output = io.MultiWriter(log, progress)
	}
	cmd.Dir, cmd.Env, cmd.Stdout, cmd.Stderr = dir, append(os.Environ(), env...), output, output
	procutil.ConfigureGroupCancel(cmd, 2*time.Second)
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		return false, nil
	}
	return true, nil
}

type progressWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (w *progressWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.w.Write(p)
}

func progressf(w io.Writer, format string, args ...any) {
	if w != nil {
		_, _ = fmt.Fprintf(w, format, args...)
	}
}

func taskEnv(runDir string, manifest Manifest, attempt int) []string {
	return []string{"TASK_ID=" + manifest.Task, "TASK_RUN=" + manifest.RunID, "TASK_ROOT=" + manifest.WorkDir,
		"TASK_ARTIFACTS=" + filepath.Join(runDir, "artifacts"), fmt.Sprintf("TASK_ATTEMPT=%d", attempt)}
}

func selectRunnable(nodes []Node, parallel int) []Node {
	selected := nodes
	for _, node := range nodes {
		if node.Access == "write" {
			selected = []Node{node}
			break
		}
	}
	if len(selected) > parallel {
		selected = selected[:parallel]
	}
	return selected
}

func terminalStatus(states map[string]string) string {
	allPassed := true
	for _, state := range states {
		if state == "failed" {
			return "failed"
		}
		if state != "passed" {
			allPassed = false
		}
	}
	if allPassed {
		return "completed"
	}
	return ""
}

func loadRun(runDir string) (Manifest, *Definition, error) {
	var manifest Manifest
	data, err := os.ReadFile(filepath.Join(runDir, "run.json"))
	if err != nil {
		return manifest, nil, fmt.Errorf("wrk: read run manifest: %w", err)
	}
	if err := json.Unmarshal(data, &manifest); err != nil || manifest.Version != runVersion {
		return manifest, nil, fmt.Errorf("wrk: invalid run manifest")
	}
	if manifest.Task == "" || manifest.RunID == "" || manifest.WorkDir == "" || manifest.Shell3Bin == "" {
		return manifest, nil, fmt.Errorf("wrk: incomplete run manifest")
	}
	configPath := filepath.Join(runDir, "shell3.lisp")
	definitionPath := filepath.Join(runDir, "task.wrk.lisp")
	if hashFile(configPath) != manifest.ConfigHash || hashFile(definitionPath) != manifest.DefinitionHash {
		return manifest, nil, errors.New("wrk: immutable run snapshot hash mismatch")
	}
	cfg, err := lispconfig.Load(configPath)
	if err != nil {
		return manifest, nil, err
	}
	def, err := Load(definitionPath, cfg)
	return manifest, def, err
}

func notifyTerminal(runDir string, manifest Manifest, status string) error {
	if manifest.NotifyTo == "" {
		return nil
	}
	path := filepath.Join(runDir, "notify.json")
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	root := manifest.NotifyState
	if root == "" {
		root = filepath.Join(manifest.WorkDir, ".shell3_project")
	}
	event := "wrk.completed"
	switch status {
	case "failed":
		event = "wrk.failed"
	case "cancelled":
		event = "wrk.cancelled"
	}
	receipt, err := (inbox.Store{Root: root}).Notify(inbox.Request{To: manifest.NotifyTo, Source: "wrk:" + manifest.RunID,
		Event: event, Correlation: manifest.RunID, Body: fmt.Sprintf("workflow %s %s (run %s)", manifest.Task, status, manifest.RunID)})
	if err != nil {
		return err
	}
	return writeJSON(path, receipt)
}

// TerminalNoticePersisted reports whether a terminal run has durably recorded
// its completion notice. Runs without a notification destination are complete
// by definition.
func TerminalNoticePersisted(runDir string) (bool, error) {
	manifest, _, err := loadRun(runDir)
	if err != nil {
		return false, err
	}
	if manifest.NotifyTo == "" {
		return true, nil
	}
	if _, err := os.Stat(filepath.Join(runDir, "notify.json")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func lockRun(runDir string) (*os.File, error) {
	f, err := os.OpenFile(filepath.Join(runDir, "beat.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, ErrBeatOwned
	}
	return f, nil
}

func lockRunBlocking(runDir string) (*os.File, error) {
	f, err := os.OpenFile(filepath.Join(runDir, "beat.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

func unlockRun(f *os.File) { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }

func writeStatus(dir, status string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return atomicWrite(filepath.Join(dir, "status"), []byte(status+"\n"))
}

func readStatus(dir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(dir, "status"))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func finishCancelled(runDir string, manifest Manifest, result BeatResult) (BeatResult, error) {
	result.Status = "cancelled"
	if err := atomicWrite(filepath.Join(runDir, "status"), []byte("cancelled\n")); err != nil {
		return result, err
	}
	return result, notifyTerminal(runDir, manifest, "cancelled")
}

func watchCancellation(runDir string, cancel context.CancelFunc, done <-chan struct{}) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			if isCancelled(runDir) {
				cancel()
				return
			}
		}
	}
}

func atomicWrite(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(path, append(data, '\n'))
}

func readJSON(path string, value any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}

func hashFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func nextAttempt(nodeDir string) int {
	entries, _ := os.ReadDir(nodeDir)
	count := 0
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "attempt-") {
			count++
		}
	}
	return count + 1
}

func readOptional(path string) string {
	data, _ := os.ReadFile(path)
	return strings.TrimSpace(string(data))
}

func newRunID() (string, error) {
	var raw [6]byte
	if _, err := io.ReadFull(rand.Reader, raw[:]); err != nil {
		return "", err
	}
	return time.Now().UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(raw[:]), nil
}

func validRunID(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && !strings.ContainsRune("_.-", r) {
			return false
		}
	}
	return true
}
