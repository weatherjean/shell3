package codeagent_test

import (
	"context"
	"testing"

	"github.com/weatherjean/shell3/internal/codeagent"
)

func TestExtractBashBlocks_None(t *testing.T) {
	blocks := codeagent.ExtractBashBlocks("Just some text with no code blocks.")
	if len(blocks) != 0 {
		t.Errorf("expected 0 blocks, got %d", len(blocks))
	}
}

func TestExtractBashBlocks_One(t *testing.T) {
	text := "I'll check the files.\n```bash\nls -la\n```\nDone."
	blocks := codeagent.ExtractBashBlocks(text)
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	if blocks[0] != "ls -la" {
		t.Errorf("got %q", blocks[0])
	}
}

func TestExtractBashBlocks_Multiple(t *testing.T) {
	text := "```bash\nwc -l foo.go\n```\nThen read it:\n```bash\ncat foo.go\n```"
	blocks := codeagent.ExtractBashBlocks(text)
	if len(blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(blocks))
	}
	if blocks[0] != "wc -l foo.go" {
		t.Errorf("block 0: got %q", blocks[0])
	}
	if blocks[1] != "cat foo.go" {
		t.Errorf("block 1: got %q", blocks[1])
	}
}

func TestExtractBashBlocks_NonBashFenced(t *testing.T) {
	text := "```go\nfmt.Println()\n```"
	blocks := codeagent.ExtractBashBlocks(text)
	if len(blocks) != 0 {
		t.Errorf("non-bash fenced block should not be extracted")
	}
}

func TestExecuteBlock_Echo(t *testing.T) {
	out := codeagent.ExecuteBlock(context.Background(), "echo hello", "/tmp")
	if out != "hello\n" {
		t.Errorf("got %q", out)
	}
}

func TestExecuteBlock_ExitError(t *testing.T) {
	out := codeagent.ExecuteBlock(context.Background(), "exit 1", "/tmp")
	if out == "" {
		t.Error("expected non-empty output on exit error")
	}
}

func TestExecuteBlock_Cancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled
	out := codeagent.ExecuteBlock(ctx, "sleep 10", "/tmp")
	if out == "" {
		t.Error("expected error output for cancelled context")
	}
}
