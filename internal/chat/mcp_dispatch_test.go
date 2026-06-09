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
	res := dispatchMCPTool(context.Background(), mcp, "chrome__navigate_page", `{"url":"x"}`)
	if res.output != "navigated" || res.isError || called != "chrome__navigate_page" {
		t.Fatalf("unexpected: res=%+v called=%q", res, called)
	}
}

func TestDispatchMCPToolNilSeam(t *testing.T) {
	res := dispatchMCPTool(context.Background(), nil, "chrome__navigate_page", `{}`)
	if res.output == "" || !res.isError {
		t.Fatalf("expected a typed error result when seam is nil, got %+v", res)
	}
}
