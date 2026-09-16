package chat

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestBashBgHandler_Execute_happyPath(t *testing.T) {
	wd := t.TempDir()
	var gotCmd string
	cfg := ToolConfig{
		WorkDir: wd,
		StartBashBg: func(command, workdir string, argv, env []string) (string, error) {
			if len(env) != 1 || env[0] != "SHELL3_TOOL_CONTEXT=background" {
				t.Fatalf("background context = %v", env)
			}
			gotCmd = command
			return "bg_1", nil
		},
	}
	h := BashBgHandler{}
	out, err := h.Execute(context.Background(), "1", json.RawMessage(`{"command":"sleep 30"}`), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if gotCmd != "sleep 30" {
		t.Fatalf("callback got cmd %q, want %q", gotCmd, "sleep 30")
	}
	if !strings.Contains(out, "bg_1") {
		t.Fatalf("expected job id in output, got %q", out)
	}
}

func TestBashBgPollInValidationAndAdmission(t *testing.T) {
	starts := 0
	var delay time.Duration
	cfg := ToolConfig{
		StartBashBg: func(command, workdir string, argv, env []string) (string, error) {
			t.Fatal("polled job used the unpolled start callback")
			return "", nil
		},
		StartBashBgPolled: func(command, workdir string, argv, env []string, pollIn time.Duration) (string, error) {
			if len(env) != 1 || env[0] != "SHELL3_TOOL_CONTEXT=background" {
				t.Fatalf("polled background context = %v", env)
			}
			starts++
			delay = pollIn
			return "bg-polled", nil
		},
	}
	for _, value := range []string{`""`, `null`, `2`, `"0s"`, `"-2m"`, `"59s"`, `"25h"`, `"later"`} {
		_, err := (BashBgHandler{}).Execute(t.Context(), "call", json.RawMessage(`{"command":"sleep 60","poll_in":`+value+`}`), cfg)
		if err == nil || starts != 0 {
			t.Fatalf("invalid delay %s admitted: starts=%d err=%v", value, starts, err)
		}
	}
	out, err := (BashBgHandler{}).Execute(t.Context(), "call", json.RawMessage(`{"command":"sleep 60","poll_in":"2m"}`), cfg)
	if err != nil || starts != 1 || delay != 2*time.Minute || !strings.Contains(out, "one-shot follow-up") {
		t.Fatalf("poll start: starts=%d delay=%s out=%q err=%v", starts, delay, out, err)
	}
	cfg.StartBashBgPolled = nil
	_, err = (BashBgHandler{}).Execute(t.Context(), "call", json.RawMessage(`{"command":"sleep 60","poll_in":"3m"}`), cfg)
	if err == nil || !strings.Contains(err.Error(), "command was not started") {
		t.Fatalf("unsupported host = %v", err)
	}
}

func TestBashBgHandler_Execute_requiresCallback(t *testing.T) {
	args := json.RawMessage(`{"command":"true"}`)
	_, err := BashBgHandler{}.Execute(context.Background(), "1", args, ToolConfig{WorkDir: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "not available") {
		t.Fatalf("expected not-available error, got %v", err)
	}
}

func TestBashBgHandler_Execute_badJSON(t *testing.T) {
	_, err := BashBgHandler{}.Execute(context.Background(), "1", json.RawMessage(`{not json`), ToolConfig{})
	if err == nil {
		t.Fatal("expected error on bad JSON")
	}
}

func TestBashBgHandler_Execute_emptyCommand(t *testing.T) {
	_, err := BashBgHandler{}.Execute(context.Background(), "1", json.RawMessage(`{"command":""}`), ToolConfig{WorkDir: t.TempDir()})
	if err == nil {
		t.Fatal("expected error on empty command")
	}
}

func TestBashBgHandler_Execute_workdirOverride(t *testing.T) {
	primary := t.TempDir()
	override := t.TempDir()
	var gotWorkdir string
	cfg := ToolConfig{
		WorkDir: primary,
		StartBashBg: func(command, workdir string, argv, env []string) (string, error) {
			gotWorkdir = workdir
			return "bg_2", nil
		},
	}
	args, _ := json.Marshal(map[string]string{"command": "sleep 30", "workdir": override})
	out, err := BashBgHandler{}.Execute(context.Background(), "1", args, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if gotWorkdir != override {
		t.Fatalf("callback got workdir %q, want %q", gotWorkdir, override)
	}
	if !strings.Contains(out, "bg_2") {
		t.Fatalf("expected job id in output, got %q", out)
	}
}

func TestBashBgRejectsUnknownFields(t *testing.T) {
	started := false
	cfg := ToolConfig{
		StartBashBg: func(command, workdir string, argv, env []string) (string, error) {
			started = true
			return "bg1", nil
		},
	}
	for _, args := range []string{
		`{"command":"true","direct":true}`,
		`{"command":"true","report":"raw"}`,
		`{"command":"true","note":"user waiting"}`,
	} {
		_, err := (BashBgHandler{}).Execute(context.Background(), "1", json.RawMessage(args), cfg)
		if err == nil || !strings.Contains(err.Error(), "unknown field") {
			t.Fatalf("%s: want unknown-field error, got %v", args, err)
		}
		if started {
			t.Fatalf("%s: the job must not run on a stale arg", args)
		}
	}
}
