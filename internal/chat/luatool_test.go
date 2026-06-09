package chat

import (
	"context"
	"testing"
)

func TestCustomToolDispatcher(t *testing.T) {
	called := ""
	custom := func(_ context.Context, name, args string) (string, error) {
		called = name + ":" + args
		return "ok", nil
	}
	res := dispatchCustomTool(context.Background(), custom, "echo", `{"a":1}`)
	if res.output != "ok" || res.isError || called != `echo:{"a":1}` {
		t.Fatalf("dispatch: res=%+v called=%q", res, called)
	}
}

func TestGuardDecisionConstants(t *testing.T) {
	// Document the contract: 0=allow,1=block,2=cancel — matches luacfg.Decision.
	if guardAllow != 0 || guardBlock != 1 || guardCancel != 2 {
		t.Fatal("guard decision constants drifted from luacfg.Decision")
	}
}
