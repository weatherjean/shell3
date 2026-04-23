package tools_test

import (
	"context"
	"testing"

	"github.com/weatherjean/shell3/internal/tools"
)

func TestBashTool_Echo(t *testing.T) {
	bash := tools.NewBashTool("/tmp", 10)
	result, err := bash.Execute(context.Background(), map[string]any{"command": "echo hello"})
	if err != nil {
		t.Fatal(err)
	}
	if result != "hello\n" {
		t.Errorf("got %q, want %q", result, "hello\n")
	}
}

func TestBashTool_ExitCode(t *testing.T) {
	bash := tools.NewBashTool("/tmp", 10)
	_, err := bash.Execute(context.Background(), map[string]any{"command": "exit 1"})
	if err == nil {
		t.Error("expected error for non-zero exit")
	}
}

func TestBashTool_Timeout(t *testing.T) {
	bash := tools.NewBashTool("/tmp", 1)
	_, err := bash.Execute(context.Background(), map[string]any{"command": "sleep 5"})
	if err == nil {
		t.Error("expected timeout error")
	}
}
