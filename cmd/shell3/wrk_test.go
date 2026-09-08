//go:build unix

package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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
