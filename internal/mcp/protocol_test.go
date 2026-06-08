package mcp

import (
	"encoding/json"
	"testing"
)

func TestParseToolsListResult(t *testing.T) {
	raw := json.RawMessage(`{"tools":[
		{"name":"navigate_page","description":"Go to a URL","inputSchema":{"type":"object","properties":{"url":{"type":"string"}},"required":["url"]}}
	]}`)
	got, err := parseToolsList(raw)
	if err != nil {
		t.Fatalf("parseToolsList: %v", err)
	}
	if len(got) != 1 || got[0].Name != "navigate_page" {
		t.Fatalf("unexpected tools: %+v", got)
	}
	if got[0].InputSchema["type"] != "object" {
		t.Fatalf("inputSchema not preserved: %+v", got[0].InputSchema)
	}
}

func TestParseCallResultFlattensText(t *testing.T) {
	raw := json.RawMessage(`{"content":[{"type":"text","text":"hello "},{"type":"text","text":"world"}],"isError":false}`)
	res, err := parseCallResult(raw)
	if err != nil {
		t.Fatalf("parseCallResult: %v", err)
	}
	if res.Text != "hello world" || res.IsError {
		t.Fatalf("unexpected result: %+v", res)
	}
}

func TestParseCallResultUsesStructuredWhenNoContent(t *testing.T) {
	raw := json.RawMessage(`{"content":[],"structuredContent":{"pages":2},"isError":true}`)
	res, err := parseCallResult(raw)
	if err != nil {
		t.Fatalf("parseCallResult: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError true")
	}
	if res.Text != `{"pages":2}` {
		t.Fatalf("expected structured JSON, got %q", res.Text)
	}
}
