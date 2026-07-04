package chat

import (
	"context"
	"strings"
	"testing"
)

func TestDispatchCustomToolForeground(t *testing.T) {
	cfg := TurnConfig{
		WorkDir:    t.TempDir(),
		AgentKnobs: AgentKnobs{CustomToolNames: map[string]bool{"echoer": true}},
		ResolveCustomTool: func(name, args string) (ResolvedTool, error) {
			return ResolvedTool{Command: `printf "%s" "$msg"`, Env: []string{"msg=hello-tool"}}, nil
		},
	}
	res := dispatchCustomTool(context.Background(), cfg, "echoer", `{"msg":"hello-tool"}`)
	if res.isError || strings.TrimSpace(res.output) != "hello-tool" {
		t.Fatalf("res = %+v", res)
	}
}

// TestDispatchCustomToolBackground verifies a background=true tool dispatches
// onto the in-process job runtime (StartBashBg) with its resolved command and
// env, and reports the job id — not a pid/log pointer.
func TestDispatchCustomToolBackground(t *testing.T) {
	var gotCommand string
	var gotEnv []string
	cfg := TurnConfig{
		WorkDir:    t.TempDir(),
		AgentKnobs: AgentKnobs{CustomToolNames: map[string]bool{"bg_echo": true}},
		ResolveCustomTool: func(name, args string) (ResolvedTool, error) {
			return ResolvedTool{Command: `printf "%s" "$msg"`, Env: []string{"msg=hi"}, Background: true}, nil
		},
		StartBashBg: func(command, workdir string, argv, env []string) (string, error) {
			gotCommand, gotEnv = command, env
			return "bg7", nil
		},
	}
	res := dispatchCustomTool(context.Background(), cfg, "bg_echo", `{"msg":"hi"}`)
	if res.isError || !strings.Contains(res.output, "bg7") {
		t.Fatalf("res = %+v", res)
	}
	if gotCommand != `printf "%s" "$msg"` || len(gotEnv) != 1 || gotEnv[0] != "msg=hi" {
		t.Fatalf("StartBashBg got command=%q env=%v", gotCommand, gotEnv)
	}
}

// TestDispatchCustomToolBackgroundUnavailable verifies the error when no job
// runtime is wired (StartBashBg nil).
func TestDispatchCustomToolBackgroundUnavailable(t *testing.T) {
	cfg := TurnConfig{
		WorkDir:    t.TempDir(),
		AgentKnobs: AgentKnobs{CustomToolNames: map[string]bool{"bg_echo": true}},
		ResolveCustomTool: func(name, args string) (ResolvedTool, error) {
			return ResolvedTool{Command: "true", Background: true}, nil
		},
	}
	res := dispatchCustomTool(context.Background(), cfg, "bg_echo", `{}`)
	if !res.isError || !strings.Contains(res.output, "not available") {
		t.Fatalf("res = %+v", res)
	}
}

func TestDispatchCustomToolNonZeroExitIsError(t *testing.T) {
	cfg := TurnConfig{
		WorkDir:    t.TempDir(),
		AgentKnobs: AgentKnobs{CustomToolNames: map[string]bool{"boom": true}},
		ResolveCustomTool: func(name, args string) (ResolvedTool, error) {
			return ResolvedTool{Command: `echo nope; exit 7`}, nil
		},
	}
	res := dispatchCustomTool(context.Background(), cfg, "boom", `{}`)
	if !res.isError || !strings.Contains(res.output, "exited 7") {
		t.Fatalf("res = %+v", res)
	}
}
