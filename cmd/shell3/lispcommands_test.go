package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/weatherjean/shell3/internal/wrk"
)

const cliLispConfig = `(shell3
  (version 1)
  (runner fake
    (command "/usr/bin/fake-agent")
    (result stdout))
  (agent builder (using fake)))
`

func writeCLIFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLispConfigCheckCommand(t *testing.T) {
	dir := t.TempDir()
	path := writeCLIFile(t, dir, "shell3.lisp", cliLispConfig)
	cmd := newLispConfigCheckCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{path})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, "ok (orchestrator=none, transport=console, 0 model(s), 0 skill(s), 1 runner(s), 1 agent(s), 0 schedule(s))") {
		t.Fatalf("output = %q", got)
	}
}

func TestLispConfigSkillPrintsEmbeddedBody(t *testing.T) {
	dir := t.TempDir()
	path := writeCLIFile(t, dir, "shell3.lisp", `(shell3
  (version 1)
  (skill web
    (description "Search")
    (instructions "Use the inspected browser.")))`)
	cmd := newLispConfigCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"skill", path, "web"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "Use the inspected browser.\n" {
		t.Fatalf("skill body = %q", got)
	}
}

func TestWrkCheckCommandEndToEnd(t *testing.T) {
	dir := t.TempDir()
	configPath := writeCLIFile(t, dir, "shell3.lisp", cliLispConfig)
	wrkPath := writeCLIFile(t, dir, "demo.wrk.lisp", `(task "demo"
  (parallel 2)
  (loop implement
    (using builder)
    (max 3)
    (prompt """
Implement one useful increment.
""")
    (until (sh "true"))))`)
	cmd := newWrkCheckCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--config", configPath, wrkPath})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, `ok (task "demo", 1 node(s), parallel 2)`) {
		t.Fatalf("output = %q", got)
	}
}

func TestWrkRunCommandEndToEnd(t *testing.T) {
	dir := t.TempDir()
	configPath := writeCLIFile(t, dir, "shell3.lisp", cliLispConfig)
	wrkPath := writeCLIFile(t, dir, "demo.wrk.lisp", `(task "demo"
  (loop implement
    (using builder)
    (max 2)
    (prompt "Implement one increment.")
    (until (sh "test -f \"$TASK_ARTIFACTS/done\""))))`)
	fake := writeCLIFile(t, dir, "fake-shell3.sh", `#!/usr/bin/env bash
set -euo pipefail
shift 2
run_dir=
artifacts=
while [ "$#" -gt 0 ]; do
  case "$1" in
    --run-dir) run_dir=$2; shift 2 ;;
    --slot)
      case "$2" in task-artifacts=*) artifacts=${2#task-artifacts=} ;; esac
      shift 2 ;;
    *) shift ;;
  esac
done
mkdir -p "$run_dir" "$artifacts"
cat > "$run_dir/prompt.md"
touch "$artifacts/done"
printf 'done\n'
`)
	if err := os.Chmod(fake, 0o700); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(dir, "state")
	cmd := newWrkRunCommand()
	var out, stderr bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{
		"--config", configPath, "--state", state, "--run-id", "test-run",
		"--shell3-bin", fake, wrkPath, "Build it",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("run: %v\n%s", err, stderr.String())
	}
	if !strings.Contains(out.String(), "completed demo (test-run)") {
		t.Fatalf("output = %q", out.String())
	}
	status, err := os.ReadFile(filepath.Join(state, "demo", "test-run", "nodes", "implement", "status"))
	if err != nil || strings.TrimSpace(string(status)) != "passed" {
		t.Fatalf("status = %q, err = %v", status, err)
	}
}

func TestWrkRunRejectsNestedLeafLaunch(t *testing.T) {
	t.Setenv("SHELL3_WRK_WORKER", "1")
	cmd := newWrkRunCommand()
	cmd.SetArgs([]string{"nested.wrk.lisp", "do it"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "nested workflow launch denied") {
		t.Fatalf("error = %v", err)
	}
}

func TestWrkBeatCommandAdvancesExistingRun(t *testing.T) {
	dir := t.TempDir()
	configPath := writeCLIFile(t, dir, "shell3.lisp", "(shell3 (version 1))\n")
	wrkPath := writeCLIFile(t, dir, "beat.wrk.lisp", `(task "beat-demo" (command finish (run "printf beat > beat.txt")))`)
	state := filepath.Join(dir, "state")
	if _, err := wrk.Start(configPath, wrkPath, wrk.StartOptions{StateRoot: state, RunID: "beat-run", Shell3Bin: "/bin/false"}); err != nil {
		t.Fatal(err)
	}
	cmd := newWrkBeatCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--state", state, "beat-run"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "beat-demo/beat-run: completed (finish)") {
		t.Fatalf("output = %q", out.String())
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "beat.txt")); string(got) != "beat" {
		t.Fatalf("beat output = %q", got)
	}
}

func TestWrkBeatRejectsNestedLeafLaunch(t *testing.T) {
	t.Setenv("SHELL3_WRK_WORKER", "1")
	cmd := newWrkBeatCommand()
	cmd.SetArgs([]string{"anything"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "nested workflow beat denied") {
		t.Fatalf("error = %v", err)
	}
}

func TestWrkSignalStatusAndCancelCommands(t *testing.T) {
	dir := t.TempDir()
	configPath := writeCLIFile(t, dir, "shell3.lisp", "(shell3 (version 1))\n")
	wrkPath := writeCLIFile(t, dir, "control.wrk.lisp", `(task "control" (wait approval (for (event "approved"))))`)
	state := filepath.Join(dir, "state")
	if _, err := wrk.Start(configPath, wrkPath, wrk.StartOptions{StateRoot: state, RunID: "controlled", Shell3Bin: "/bin/false"}); err != nil {
		t.Fatal(err)
	}

	signal := newWrkSignalCommand()
	var signalOut bytes.Buffer
	signal.SetOut(&signalOut)
	signal.SetArgs([]string{"--state", state, "controlled", "approved", "looks good"})
	if err := signal.Execute(); err != nil || !strings.Contains(signalOut.String(), "recorded approved") {
		t.Fatalf("signal: %v, %q", err, signalOut.String())
	}

	beat := newWrkBeatCommand()
	beat.SetOut(&bytes.Buffer{})
	beat.SetArgs([]string{"--state", state, "controlled"})
	if err := beat.Execute(); err != nil {
		t.Fatal(err)
	}
	status := newWrkStatusCommand()
	var statusOut bytes.Buffer
	status.SetOut(&statusOut)
	status.SetArgs([]string{"--state", state, "control/controlled"})
	if err := status.Execute(); err != nil || !strings.Contains(statusOut.String(), `"status": "completed"`) || !strings.Contains(statusOut.String(), `"event": "approved"`) {
		t.Fatalf("status: %v, %q", err, statusOut.String())
	}

	if _, err := wrk.Start(configPath, wrkPath, wrk.StartOptions{StateRoot: state, RunID: "cancel-me", Shell3Bin: "/bin/false"}); err != nil {
		t.Fatal(err)
	}
	cancel := newWrkCancelCommand()
	var cancelOut bytes.Buffer
	cancel.SetOut(&cancelOut)
	cancel.SetArgs([]string{"--state", state, "cancel-me"})
	if err := cancel.Execute(); err != nil || !strings.Contains(cancelOut.String(), "control/cancel-me: cancelled") {
		t.Fatalf("cancel: %v, %q", err, cancelOut.String())
	}
}

func TestWrkMutatingControlsRejectNestedLeaf(t *testing.T) {
	t.Setenv("SHELL3_WRK_WORKER", "1")
	for name, cmd := range map[string]*cobra.Command{
		"signal": newWrkSignalCommand(),
		"cancel": newWrkCancelCommand(),
	} {
		cmd.SetArgs([]string{"run", "event"})
		if name == "cancel" {
			cmd.SetArgs([]string{"run"})
		}
		if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "leaf workers") {
			t.Errorf("%s error = %v", name, err)
		}
	}
}
