//go:build unix

package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/lispconfig"
)

func TestExecutorRunsTypedArgvProtocol(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-agent.sh")
	body := `#!/usr/bin/env bash
set -euo pipefail
workdir=$1
result=$2
model=$3
prompt=$(cat)
printf 'workdir=%s\nmodel=%s\nsecret=%s\nprompt=%s\n' "$workdir" "$model" "${RUNNER_MODEL_SECRET-unset}" "$prompt" > "$result"
printf '{"event":"finished"}\n'
printf 'diagnostic\n' >&2
`
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	configSource := `(shell3
  (version 1)
  (model main (api-key-env RUNNER_MODEL_SECRET) (id "main-model"))
  (runner fake
    (parameters (model string required))
    (command "` + script + `")
    (arguments workdir result-file model)
    (stderr log)
    (result (file result-file))
    (timeout "5s"))
  (agent builder
    (using fake)
    (model "test-model")))`
	cfg, err := lispconfig.Parse("shell3.lisp", []byte(configSource))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("RUNNER_MODEL_SECRET", "must-not-reach-worker")
	runDir := filepath.Join(dir, "run")
	var progress strings.Builder
	result, err := (Executor{Config: cfg}).Run(context.Background(), Request{
		Agent: "builder", Prompt: "do the thing", WorkDir: dir, RunDir: runDir, Progress: &progress,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"workdir=" + dir, "model=test-model", "secret=unset", "prompt=" + leafInstructions, "do the thing"} {
		if !strings.Contains(result.Text, want) {
			t.Fatalf("result %q does not contain %q", result.Text, want)
		}
	}
	stdout, err := os.ReadFile(filepath.Join(runDir, "stdout.log"))
	if err != nil || !strings.Contains(string(stdout), `"event":"finished"`) {
		t.Fatalf("stdout = %q, err = %v", stdout, err)
	}
	stderr, err := os.ReadFile(filepath.Join(runDir, "stderr.log"))
	if err != nil || string(stderr) != "diagnostic\n" {
		t.Fatalf("stderr = %q, err = %v", stderr, err)
	}
	if got := progress.String(); !strings.Contains(got, `{"event":"finished"}`) || !strings.Contains(got, "diagnostic") {
		t.Fatalf("progress did not mirror stdout and stderr: %q", got)
	}
}

func TestTaskEnvironmentMarksLeafWorker(t *testing.T) {
	got := strings.Join(taskEnvironment(map[string]string{"task-run": "run-1"}), "\n")
	for _, want := range []string{"SHELL3_WRK_WORKER=1", "TASK_RUN=run-1"} {
		if !strings.Contains(got, want) {
			t.Fatalf("environment missing %q: %q", want, got)
		}
	}
}

func TestRunnerEnvironmentOmitsHarnessSecrets(t *testing.T) {
	t.Setenv("MODEL_SECRET_FOR_TEST", "must-not-reach-worker")
	t.Setenv("TELEGRAM_SECRET_FOR_TEST", "must-not-reach-worker")
	t.Setenv("ORDINARY_VALUE_FOR_TEST", "preserved")
	cfg := &lispconfig.Config{
		Models:   map[string]lispconfig.Model{"main": {APIKeyEnv: "MODEL_SECRET_FOR_TEST"}},
		Telegram: &lispconfig.Telegram{TokenEnv: "TELEGRAM_SECRET_FOR_TEST"},
	}
	got := strings.Join(runnerEnvironment(cfg), "\n")
	for _, name := range []string{"MODEL_SECRET_FOR_TEST", "TELEGRAM_SECRET_FOR_TEST"} {
		if strings.Contains(got, name+"=") {
			t.Fatalf("%s leaked into runner environment", name)
		}
	}
	if !strings.Contains(got, "ORDINARY_VALUE_FOR_TEST=preserved") {
		t.Fatalf("ordinary environment was removed: %q", got)
	}
}

func TestExecutorReportsFailure(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "fail.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho broken >&2\nexit 7\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	source := `(shell3 (version 1)
  (runner fail (command "` + script + `") (result stdout))
  (agent a (using fail)))`
	cfg, err := lispconfig.Parse("shell3.lisp", []byte(source))
	if err != nil {
		t.Fatal(err)
	}
	result, err := (Executor{Config: cfg}).Run(context.Background(), Request{
		Agent: "a", Prompt: "x", WorkDir: dir, RunDir: filepath.Join(dir, "run"),
	})
	if err == nil || !strings.Contains(err.Error(), "exited with code 7") || !strings.Contains(err.Error(), "broken") {
		t.Fatalf("result = %+v, error = %v", result, err)
	}
}
