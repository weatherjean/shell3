package chat

import (
	"context"
	"encoding/json"
	"testing"
)

func TestDocsHandler_Name(t *testing.T) {
	if (DocsHandler{}).Name() != "shell3_docs" {
		t.Fatal("wrong name")
	}
}

func TestDocsHandler_Execute_withDocs(t *testing.T) {
	h := DocsHandler{docs: "# shell3 docs"}
	out, err := h.Execute(context.Background(), "1", json.RawMessage(`{}`), ToolConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if out != "# shell3 docs" {
		t.Fatalf("got %q", out)
	}
}

func TestDocsHandler_Execute_noDocs(t *testing.T) {
	h := DocsHandler{}
	out, _ := h.Execute(context.Background(), "1", json.RawMessage(`{}`), ToolConfig{})
	if out != "Documentation not available." {
		t.Fatalf("expected fallback, got %q", out)
	}
}
