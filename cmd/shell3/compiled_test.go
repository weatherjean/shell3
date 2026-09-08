//go:build unix

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/wrk"
)

func TestCompiledWorkflowUsesDurableRuntime(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "shell3")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	t.Run("nested runner cancellation", func(t *testing.T) { testNestedRunnerCancellation(t, bin) })
	cases := []struct {
		name, node, want string
		fail             bool
	}{
		{"success", `(command work (run "true"))`, "completed", false},
		{"command failure", `(command work (run "false"))`, "failed", true},
		{"worker failure", `(agent work (using a) (prompt "test"))`, "failed", true},
		{"timeout", `(command work (timeout "20ms") (run "sleep 30"))`, "failed", true},
		{"directory check", `(command work (run "mkdir $TASK_ARTIFACTS/output") (accept (file "output")))`, "failed", true},
		{"wait", `(wait approval (for (event "go")))`, "waiting", false},
		{"cancel", `(command work (run "printf '{}' > \"$TASK_ARTIFACTS/../cancel.json\""))`, "cancelled", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			config := writeCLIFile(t, dir, "shell3.lisp", `(shell3 (version 1) (runner fail (command "/bin/sh" "-c" "exit 7") (result stdout)) (agent a (using fail)))`)
			workflow := writeCLIFile(t, dir, "task.wrk.lisp", `(task "demo" `+tc.node+`)`)
			script, err := wrk.Compile(&wrk.Definition{Path: workflow}, config)
			if err != nil {
				t.Fatal(err)
			}
			launcher := writeCLIFile(t, dir, "run.sh", script)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "bash", launcher, "test")
			state := filepath.Join(dir, "state")
			cmd.Env = append(os.Environ(), "SHELL3_BIN="+bin, "SHELL3_WRK_STATE="+state, "SHELL3_WRK_RUN_ID=run")
			out, err := cmd.CombinedOutput()
			if (err != nil) != tc.fail || !strings.Contains(string(out), tc.want) {
				t.Fatalf("error=%v output=%s", err, out)
			}
			snapshot, err := wrk.Inspect(filepath.Join(state, "demo", "run"))
			if err != nil || snapshot.Status != tc.want {
				t.Fatalf("snapshot=%+v err=%v", snapshot, err)
			}
			if err := os.WriteFile(workflow, []byte(`(task "changed" (command work (run "true")))`), 0600); err != nil {
				t.Fatal(err)
			}
			cmd = exec.CommandContext(ctx, "bash", launcher, "test")
			cmd.Env = append(os.Environ(), "SHELL3_BIN="+bin, "SHELL3_WRK_STATE="+state)
			out, err = cmd.CombinedOutput()
			if err == nil || !strings.Contains(string(out), "compiled inputs changed") {
				t.Fatalf("changed input: %v %s", err, out)
			}
		})
	}
}

func testNestedRunnerCancellation(t *testing.T, bin string) {
	dir := t.TempDir()
	command := `trap '' TERM; sleep 30 & echo $! > "$TASK_ARTIFACTS/child.pid"; wait`
	config := writeCLIFile(t, dir, "shell3.lisp", fmt.Sprintf(`(shell3 (version 1) (runner stubborn (command "/bin/sh" "-c" %q) (result stdout)) (agent a (using stubborn)))`, command))
	workflow := writeCLIFile(t, dir, "task.wrk.lisp", `(task "nested" (agent work (using a) (prompt "run")))`)
	runDir, err := wrk.Start(config, workflow, wrk.StartOptions{StateRoot: filepath.Join(dir, "state"), Shell3Bin: bin})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := wrk.Beat(ctx, runDir); done <- err }()
	path := filepath.Join(runDir, "artifacts", "child.pid")
	var pid int
	deadline := time.Now().Add(5 * time.Second)
	for pid == 0 {
		if data, err := os.ReadFile(path); err == nil {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(data)))
		}
		if time.Now().After(deadline) {
			t.Fatal("external runner never started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	if err := wrk.Cancel(runDir); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("nested cancellation did not finish")
	}
	deadline = time.Now().Add(time.Second)
	for syscall.Kill(pid, 0) == nil {
		if time.Now().After(deadline) {
			t.Fatal("external runner descendant survived cancellation")
		}
		time.Sleep(10 * time.Millisecond)
	}
	snapshot, err := wrk.Inspect(runDir)
	if err != nil || snapshot.Status != "cancelled" {
		t.Fatalf("snapshot=%+v err=%v", snapshot, err)
	}
}
