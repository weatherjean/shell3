package chat

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestBashBgHandler_Name(t *testing.T) {
	h := BashBgHandler{}
	if h.Name() != "bash_bg" {
		t.Fatal("wrong name")
	}
}

func TestBashBgHandler_Execute_happyPath(t *testing.T) {
	wd := t.TempDir()
	var gotCmd string
	cfg := ToolConfig{
		WorkDir: wd,
		StartBashBg: func(command, workdir string, argv []string) (string, error) {
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
		StartBashBg: func(command, workdir string, argv []string) (string, error) {
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

func TestBashBgUsesStartCallback(t *testing.T) {
	var gotCmd string
	cfg := ToolConfig{
		WorkDir: t.TempDir(),
		StartBashBg: func(command, workdir string, argv []string) (string, error) {
			gotCmd = command
			return "bg1", nil
		},
		RunToolCall: nil, // ungated
	}
	out, err := BashBgHandler{}.Execute(context.Background(), "t", json.RawMessage(`{"command":"echo hi"}`), cfg)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if gotCmd != "echo hi" {
		t.Fatalf("callback got %q, want %q", gotCmd, "echo hi")
	}
	if !strings.Contains(out, "bg1") {
		t.Fatalf("output %q missing job id", out)
	}
}
