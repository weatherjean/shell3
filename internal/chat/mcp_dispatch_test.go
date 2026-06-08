package chat

import (
	"context"
	"testing"
)

func TestDispatchRoutesMCPName(t *testing.T) {
	called := ""
	mcp := func(ctx context.Context, name, args string) (string, error) {
		called = name
		return "navigated", nil
	}
	out := dispatchMCPTool(context.Background(), mcp, "chrome__navigate_page", `{"url":"x"}`)
	if out != "navigated" || called != "chrome__navigate_page" {
		t.Fatalf("unexpected: out=%q called=%q", out, called)
	}
}

func TestDispatchMCPToolNilSeam(t *testing.T) {
	out := dispatchMCPTool(context.Background(), nil, "chrome__navigate_page", `{}`)
	if out == "" {
		t.Fatalf("expected an error string when seam is nil")
	}
}
