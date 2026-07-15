package chat

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestTaskListHandler_CallsListJobs verifies the handler invokes cfg.ListJobs
// and returns its output.
func TestTaskListHandler_CallsListJobs(t *testing.T) {
	called := false
	cfg := ToolConfig{
		ListJobs: func() string {
			called = true
			return "background tasks:\n  sub1  subagent  done  depth=1"
		},
	}
	out, err := TaskListHandler{}.Execute(context.Background(), "t", nil, cfg)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !called {
		t.Fatal("ListJobs callback was not called")
	}
	if !strings.Contains(out, "sub1") {
		t.Errorf("output %q missing sub1", out)
	}
}

// TestTaskListHandler_NilCallback returns a safe fallback.
func TestTaskListHandler_NilCallback(t *testing.T) {
	cfg := ToolConfig{}
	out, err := TaskListHandler{}.Execute(context.Background(), "t", nil, cfg)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out == "" {
		t.Error("expected non-empty fallback output")
	}
}

// TestTaskStatusHandler_CallsJobStatus verifies the handler passes the id to
// cfg.JobStatus and returns its output.
func TestTaskStatusHandler_CallsJobStatus(t *testing.T) {
	var gotID string
	cfg := ToolConfig{
		JobStatus: func(id string) string {
			gotID = id
			return "task sub1: done (subagent, depth 1)"
		},
	}
	args := json.RawMessage(`{"id":"sub1"}`)
	out, err := TaskStatusHandler().Execute(context.Background(), "t", args, cfg)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if gotID != "sub1" {
		t.Errorf("JobStatus called with id=%q, want sub1", gotID)
	}
	if !strings.Contains(out, "sub1") {
		t.Errorf("output %q missing sub1", out)
	}
}

// TestTaskStatusHandler_MissingID returns an error string.
func TestTaskStatusHandler_MissingID(t *testing.T) {
	cfg := ToolConfig{
		JobStatus: func(id string) string { return "should not be called" },
	}
	args := json.RawMessage(`{}`)
	out, err := TaskStatusHandler().Execute(context.Background(), "t", args, cfg)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "required") {
		t.Errorf("output %q should mention id required", out)
	}
}

// TestTaskCancelHandler_CallsCancelJob verifies the handler passes the id to
// cfg.CancelJob and returns its output.
func TestTaskCancelHandler_CallsCancelJob(t *testing.T) {
	var gotID string
	cfg := ToolConfig{
		CancelJob: func(id string) string {
			gotID = id
			return "cancelled task " + id
		},
	}
	args := json.RawMessage(`{"id":"sub2"}`)
	out, err := TaskCancelHandler().Execute(context.Background(), "t", args, cfg)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if gotID != "sub2" {
		t.Errorf("CancelJob called with id=%q, want sub2", gotID)
	}
	if !strings.Contains(out, "sub2") {
		t.Errorf("output %q missing sub2", out)
	}
}

// TestTaskCancelHandler_MissingID returns an error string.
func TestTaskCancelHandler_MissingID(t *testing.T) {
	cfg := ToolConfig{
		CancelJob: func(id string) string { return "should not be called" },
	}
	args := json.RawMessage(`{}`)
	out, err := TaskCancelHandler().Execute(context.Background(), "t", args, cfg)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "required") {
		t.Errorf("output %q should mention id required", out)
	}
}
