package kit

import (
	"strings"
	"testing"
)

func TestDecodeToolBlock(t *testing.T) {
	b := block{line: 10, endLine: 16, yaml: []byte(
		"tool: page-kind\n" +
			"description: Classify a site's stack\n" +
			"params:\n" +
			"  url:     {type: string, required: true}\n" +
			"  timeout: {type: int, default: 20}\n")}

	d, err := decodeBlock(b)
	if err != nil {
		t.Fatalf("decodeBlock: %v", err)
	}
	if d.kind != declTool || d.name != "page-kind" {
		t.Fatalf("decl = %+v", d)
	}
	if d.line != 10 || d.endLine != 16 {
		t.Fatalf("lines = %d..%d, want 10..16", d.line, d.endLine)
	}
	if p := d.params["url"]; p.Type != "string" || !p.Required {
		t.Fatalf("url param = %+v", p)
	}
	if p := d.params["timeout"]; p.Type != "int" || p.Default != 20 {
		t.Fatalf("timeout param = %+v", p)
	}
}

func TestDecodeAgentBlock(t *testing.T) {
	b := block{line: 3, endLine: 9, yaml: []byte(
		"agent: bookmarks\n" +
			"description: lead-gen\n" +
			"model: sonnet\n" +
			"workdir: ~/bookmarks\n" +
			"use: [bash, web]\n")}

	d, err := decodeBlock(b)
	if err != nil {
		t.Fatalf("decodeBlock: %v", err)
	}
	if d.kind != declAgent || d.name != "bookmarks" || d.model != "sonnet" {
		t.Fatalf("decl = %+v", d)
	}
	if len(d.use) != 2 || d.use[0] != "bash" || d.use[1] != "web" {
		t.Fatalf("use = %v", d.use)
	}
}

func TestDecodeWiringBlock(t *testing.T) {
	b := block{line: 1, endLine: 4, yaml: []byte(
		"shell3:\n  background: {max_concurrent: 8}\n")}
	d, err := decodeBlock(b)
	if err != nil {
		t.Fatalf("decodeBlock: %v", err)
	}
	if d.kind != declWiring || d.wiring["background"] == nil {
		t.Fatalf("decl = %+v", d)
	}
}

func TestDecodeUnknownKeyFails(t *testing.T) {
	b := block{line: 7, yaml: []byte("tool: x\ndescriptoin: typo\n")}
	_, err := decodeBlock(b)
	if err == nil {
		t.Fatal("want strict-decode error for unknown key")
	}
	if !strings.Contains(err.Error(), "line 7") {
		t.Fatalf("err = %q, want it to name line 7", err)
	}
}

func TestDecodeMultipleKindsFails(t *testing.T) {
	b := block{line: 2, yaml: []byte("tool: a\nagent: b\n")}
	if _, err := decodeBlock(b); err == nil {
		t.Fatal("want error when a block declares two kinds")
	}
}

func TestDecodeNoKindFails(t *testing.T) {
	b := block{line: 4, yaml: []byte("description: orphan\n")}
	if _, err := decodeBlock(b); err == nil {
		t.Fatal("want error when a block declares no kind")
	}
}
