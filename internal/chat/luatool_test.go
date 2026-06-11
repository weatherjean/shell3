package chat

import (
	"context"
	"strings"
	"testing"
)

func TestDispatchCustomToolForeground(t *testing.T) {
	cfg := TurnConfig{
		WorkDir:         t.TempDir(),
		CustomToolNames: map[string]bool{"echoer": true},
		ResolveCustomTool: func(name, args string) (ResolvedTool, error) {
			return ResolvedTool{Command: `printf "%s" "$msg"`, Env: []string{"msg=hello-tool"}}, nil
		},
	}
	res := dispatchCustomTool(context.Background(), cfg, "echoer", `{"msg":"hello-tool"}`)
	if res.isError || strings.TrimSpace(res.output) != "hello-tool" {
		t.Fatalf("res = %+v", res)
	}
}

func TestDispatchCustomToolNonZeroExitIsError(t *testing.T) {
	cfg := TurnConfig{
		WorkDir:         t.TempDir(),
		CustomToolNames: map[string]bool{"boom": true},
		ResolveCustomTool: func(name, args string) (ResolvedTool, error) {
			return ResolvedTool{Command: `echo nope; exit 7`}, nil
		},
	}
	res := dispatchCustomTool(context.Background(), cfg, "boom", `{}`)
	if !res.isError || !strings.Contains(res.output, "exited 7") {
		t.Fatalf("res = %+v", res)
	}
}
