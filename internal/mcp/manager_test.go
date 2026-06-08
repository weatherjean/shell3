package mcp

import (
	"context"
	"path/filepath"
	"testing"
)

// fakeClient implements the clientAPI seam in-process.
type fakeClient struct {
	started   int
	listCalls int
	callCalls int
	closed    bool
}

func (f *fakeClient) Start(ctx context.Context) error { f.started++; return nil }
func (f *fakeClient) ListTools(ctx context.Context) ([]ToolSchema, error) {
	f.listCalls++
	return []ToolSchema{{Name: "echo", Description: "d", InputSchema: map[string]any{"type": "object"}}}, nil
}
func (f *fakeClient) CallTool(ctx context.Context, name string, args map[string]any) (Result, error) {
	f.callCalls++
	return Result{Text: "ok:" + name}, nil
}
func (f *fakeClient) Close() error { f.closed = true; return nil }

func newTestManager(t *testing.T, fc *fakeClient) *Manager {
	t.Helper()
	m := NewManager([]Spec{{Name: "fake", Command: "x"}}, t.TempDir())
	m.newClient = func(Spec) clientAPI { return fc }
	return m
}

func TestManagerDefsArePrefixed(t *testing.T) {
	fc := &fakeClient{}
	m := newTestManager(t, fc)
	defs, err := m.ToolDefinitionsFor(context.Background(), []string{"fake"})
	if err != nil {
		t.Fatalf("ToolDefinitionsFor: %v", err)
	}
	if len(defs) != 1 || defs[0].Name != "fake__echo" {
		t.Fatalf("expected fake__echo, got %+v", defs)
	}
	// Discovery probe must have started and closed a throwaway client once.
	if fc.started != 1 || !fc.closed {
		t.Fatalf("probe lifecycle wrong: started=%d closed=%v", fc.started, fc.closed)
	}
}

func TestManagerDiscoveryUsesCacheSecondTime(t *testing.T) {
	dir := t.TempDir()
	fc1 := &fakeClient{}
	m1 := NewManager([]Spec{{Name: "fake", Command: "x"}}, dir)
	m1.newClient = func(Spec) clientAPI { return fc1 }
	if _, err := m1.ToolDefinitionsFor(context.Background(), []string{"fake"}); err != nil {
		t.Fatal(err)
	}
	if _, err := stat(filepath.Join(dir, "fake.tools.json")); err != nil {
		t.Fatalf("cache file not written: %v", err)
	}
	// New manager, same dir: must read cache, not probe.
	fc2 := &fakeClient{}
	m2 := NewManager([]Spec{{Name: "fake", Command: "x"}}, dir)
	m2.newClient = func(Spec) clientAPI { return fc2 }
	if _, err := m2.ToolDefinitionsFor(context.Background(), []string{"fake"}); err != nil {
		t.Fatal(err)
	}
	if fc2.started != 0 {
		t.Fatalf("expected cache hit (no probe), got started=%d", fc2.started)
	}
}

func TestManagerDispatchLazySessionReused(t *testing.T) {
	fc := &fakeClient{}
	m := newTestManager(t, fc)
	if _, err := m.ToolDefinitionsFor(context.Background(), []string{"fake"}); err != nil {
		t.Fatal(err)
	}
	startsAfterProbe := fc.started // 1 (probe). Probe client is closed.
	out, err := m.Dispatch(context.Background(), "fake__echo", `{"msg":"hi"}`)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if out != "ok:echo" {
		t.Fatalf("unexpected: %q", out)
	}
	_, _ = m.Dispatch(context.Background(), "fake__echo", `{}`)
	// Session is created once and reused: exactly one extra Start beyond probe.
	if fc.started != startsAfterProbe+1 {
		t.Fatalf("session not reused: started=%d", fc.started)
	}
	if fc.callCalls != 2 {
		t.Fatalf("expected 2 calls, got %d", fc.callCalls)
	}
	m.Shutdown()
	if !fc.closed {
		t.Fatalf("Shutdown did not close session")
	}
}

// TestManagerRealSubprocess exercises the full path: Manager -> real *Client ->
// real stdio subprocess (the re-exec fake server), with no injected fake.
func TestManagerRealSubprocess(t *testing.T) {
	dir := t.TempDir()
	m := NewManager([]Spec{fakeSpec()}, dir)
	defer m.Shutdown()

	defs, err := m.ToolDefinitionsFor(context.Background(), []string{"fake"})
	if err != nil {
		t.Fatalf("ToolDefinitionsFor: %v", err)
	}
	if len(defs) != 1 || defs[0].Name != "fake__echo" {
		t.Fatalf("expected fake__echo over real subprocess, got %+v", defs)
	}
	// Cache file must have been written by the discovery probe.
	if _, err := stat(filepath.Join(dir, "fake.tools.json")); err != nil {
		t.Fatalf("cache not written: %v", err)
	}

	out, err := m.Dispatch(context.Background(), "fake__echo", `{"msg":"world"}`)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if out != "echo:world" {
		t.Fatalf("unexpected dispatch result: %q", out)
	}
}

func TestManagerAllowlistFilters(t *testing.T) {
	fc := &fakeClient{}
	m := NewManager([]Spec{{Name: "fake", Command: "x", Tools: []string{"nope"}}}, t.TempDir())
	m.newClient = func(Spec) clientAPI { return fc }
	defs, err := m.ToolDefinitionsFor(context.Background(), []string{"fake"})
	if err != nil {
		t.Fatal(err)
	}
	if len(defs) != 0 {
		t.Fatalf("allowlist should exclude echo, got %+v", defs)
	}
}
