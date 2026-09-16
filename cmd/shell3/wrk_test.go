//go:build unix

package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

func TestForegroundWorkflowCommandsRejectBeforeAdmission(t *testing.T) {
	t.Setenv("SHELL3_TOOL_CONTEXT", "foreground")
	for name, makeCmd := range map[string]func() *cobra.Command{
		"run": newWrkRunCommand, "beat": newWrkBeatCommand, "schedule run": newScheduleRunCommand,
	} {
		t.Run(name, func(t *testing.T) {
			cmd := makeCmd()
			cmd.SetArgs([]string{"does-not-exist"})
			err := cmd.Execute()
			if err == nil || !strings.Contains(err.Error(), "requires bash_bg") || !strings.Contains(err.Error(), "nothing was started") {
				t.Fatalf("foreground %s: %v", name, err)
			}
		})
	}
	// There is no gate on a normal terminal or managed background invocation.
	for _, mode := range []string{"", "background"} {
		t.Setenv("SHELL3_TOOL_CONTEXT", mode)
		if err := requireWorkflowHost(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReadWrkRequestDoesNotWaitForInteractiveInput(t *testing.T) {
	got, err := readWrkRequest(strings.NewReader("ignored"), true)
	if err != nil || got != "" {
		t.Fatalf("request = %q, err = %v", got, err)
	}
}

func TestReadWrkRequestReadsPipedInput(t *testing.T) {
	got, err := readWrkRequest(strings.NewReader("weather in Lenart\n"), false)
	if err != nil || got != "weather in Lenart" {
		t.Fatalf("request = %q, err = %v", got, err)
	}
}

func TestWrkRunExitsOnCancelledState(t *testing.T) {
	dir := t.TempDir()
	config := writeCLIFile(t, dir, "shell3.lisp", `(shell3 (version 1))`)
	workflow := writeCLIFile(t, dir, "cancel.wrk.lisp", `(task "cancel" (command work (run "printf '{}' > \"$TASK_ARTIFACTS/../cancel.json\"")))`)
	cmd := newWrkRunCommand()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	cmd.SetContext(ctx)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--config", config, "--state", filepath.Join(dir, "state"), workflow, "test"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("error=%v", err)
	}
	if strings.Count(out.String(), ": cancelled") != 1 {
		t.Fatalf("output=%s", out.String())
	}
}
