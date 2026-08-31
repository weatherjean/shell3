package kit

import (
	"strings"
	"testing"
)

const sample = `#---
# shell3:
#   background: {max_concurrent: 8}
#---

#---
# agent: main
# model: opus
#---
main_prompt() { cat <<'EOF'
hello
EOF
}

#---
# agent: bookmarks
# description: lead-gen
# model: sonnet
# workdir: ~/bookmarks
# use: [bash, web]
#---
bm_prompt() { cat <<'EOF'
find shops
EOF
}

#---
# tool: page-kind
# description: Classify a stack
# params:
#   url: {type: string, required: true}
#---
bm_page_kind() {
  curl -sL "$url"
}

#---
# shared: web
#---
#---
# tool: search
# description: Search the web
# params:
#   q: {type: string, required: true}
#---
web_search() { searx "$q"; }
`

func TestParseAssembles(t *testing.T) {
	k, err := Parse([]byte(sample))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if k.Wiring["background"] == nil {
		t.Fatalf("wiring = %+v", k.Wiring)
	}
	if len(k.Agents) != 2 {
		t.Fatalf("agents = %d, want 2", len(k.Agents))
	}

	main := k.Agents[0]
	if main.Name != "main" || main.PromptFunc != "main_prompt" || main.Model != "opus" {
		t.Fatalf("main = %+v", main)
	}

	bm := k.Agents[1]
	if bm.Workdir != "~/bookmarks" || len(bm.Use) != 2 {
		t.Fatalf("bm = %+v", bm)
	}
	if len(bm.Tools) != 1 || bm.Tools[0].Name != "page-kind" ||
		bm.Tools[0].Func != "bm_page_kind" {
		t.Fatalf("bm tools = %+v", bm.Tools)
	}
	if len(k.Shared) != 1 || k.Shared[0].Name != "web" {
		t.Fatalf("shared = %+v", k.Shared)
	}
	if len(k.Shared[0].Tools) != 1 || k.Shared[0].Tools[0].Func != "web_search" {
		t.Fatalf("shared tools = %+v", k.Shared[0].Tools)
	}
}

func TestParseToolBeforeScopeFails(t *testing.T) {
	src := []byte("#---\n# tool: orphan\n# description: x\n#---\nf() { :; }\n")
	if _, err := Parse(src); err == nil {
		t.Fatal("want error for a tool declared before any agent/shared scope")
	}
}

func TestParseDuplicateFuncNameFails(t *testing.T) {
	src := []byte(
		"#---\n# agent: a\n#---\ndup() { cat <<'EOF'\nhi\nEOF\n}\n" +
			"#---\n# agent: b\n# description: second\n#---\ndup() { cat <<'EOF'\nhi\nEOF\n}\n")
	if _, err := Parse(src); err == nil {
		t.Fatal("want error for a duplicate function name")
	}
}

func TestParseDuplicateAgentNameFails(t *testing.T) {
	src := []byte(
		"#---\n# agent: a\n#---\np() { cat <<'EOF'\nhi\nEOF\n}\n" +
			"#---\n# agent: a\n#---\nf2() { cat <<'EOF'\nhi\nEOF\n}\n")
	if _, err := Parse(src); err == nil {
		t.Fatal("want error for a duplicate agent name")
	}
}

func TestParseEmployeeNeedsDescription(t *testing.T) {
	src := []byte("#---\n# agent: main\n#---\nmain() { cat <<'EOF'\nhi\nEOF\n}\n" +
		"#---\n# agent: worker\n#---\nworker() { cat <<'EOF'\nwork\nEOF\n}\n")
	if _, err := Parse(src); err == nil || !strings.Contains(err.Error(), "needs a description") {
		t.Fatalf("want an employee description error, got %v", err)
	}
}

func TestParseNeedsAnAgent(t *testing.T) {
	src := []byte("#---\n# shell3: {models: {}}\n#---\n")
	if _, err := Parse(src); err == nil || !strings.Contains(err.Error(), "no agents") {
		t.Fatalf("want a no-agents error, got %v", err)
	}
}

func TestParseBlockWithNoFunctionFails(t *testing.T) {
	src := []byte("#---\n# agent: a\n#---\np() { cat <<'EOF'\nhi\nEOF\n}\n#---\n# tool: t\n# description: x\n#---\n")
	if _, err := Parse(src); err == nil {
		t.Fatal("want error for a declaration with no function under it")
	}
}

// An agent block with a missing prompt function must NOT silently bind the
// following tool's implementation. Without a binding ceiling this parses
// cleanly and wrongly.
func TestParseMissingPromptFuncDoesNotStealNextFunc(t *testing.T) {
	src := []byte(
		"#---\n# agent: a\n#---\n" +
			"#---\n# tool: t\n# description: x\n#---\n" +
			"impl() { :; }\n")
	if _, err := Parse(src); err == nil {
		t.Fatal("want error: agent 'a' has no prompt function under it")
	}
}

func TestParseDuplicateToolNameInScopeFails(t *testing.T) {
	src := []byte(
		"#---\n# agent: a\n#---\np() { cat <<'EOF'\nhi\nEOF\n}\n" +
			"#---\n# tool: t\n# description: x\n#---\nf1() { :; }\n" +
			"#---\n# tool: t\n# description: y\n#---\nf2() { :; }\n")
	if _, err := Parse(src); err == nil {
		t.Fatal("want error for a duplicate declared tool name in one scope")
	}
}

func TestParseToolNeedsDescription(t *testing.T) {
	src := []byte(
		"#---\n# agent: a\n#---\np() { cat <<'EOF'\nhi\nEOF\n}\n" +
			"#---\n# tool: t\n#---\nf() { :; }\n")
	if _, err := Parse(src); err == nil {
		t.Fatal("want error for a tool with no description")
	}
}

func TestParseBadParamTypeFails(t *testing.T) {
	src := []byte("#---\n# agent: a\n#---\np() { cat <<'EOF'\nhi\nEOF\n}\n" +
		"#---\n# tool: t\n# description: x\n# params:\n#   n: {type: float}\n#---\ng() { :; }\n")
	if _, err := Parse(src); err == nil {
		t.Fatal("want error for an unsupported param type")
	}
}

func TestParseParamRequiredWithDefaultFails(t *testing.T) {
	src := []byte("#---\n# agent: a\n#---\np() { cat <<'EOF'\nhi\nEOF\n}\n" +
		"#---\n# tool: t\n# description: x\n# params:\n#   n: {type: int, required: true, default: 2}\n#---\ng() { :; }\n")
	if _, err := Parse(src); err == nil || !strings.Contains(err.Error(), "both required and have a default") {
		t.Fatalf("want a required/default conflict error, got %v", err)
	}
}

func TestParseParamDefaultTypeFails(t *testing.T) {
	for _, param := range []string{
		`{type: string, default: 2}`,
		`{type: int, default: "2"}`,
		`{type: bool, default: "true"}`,
	} {
		src := []byte("#---\n# agent: a\n#---\np() { cat <<'EOF'\nhi\nEOF\n}\n" +
			"#---\n# tool: t\n# description: x\n# params:\n#   value: " + param + "\n#---\ng() { :; }\n")
		if _, err := Parse(src); err == nil || !strings.Contains(err.Error(), "default has type") {
			t.Fatalf("%s: want a default type error, got %v", param, err)
		}
	}
}

func TestParseBadParamNameFails(t *testing.T) {
	src := []byte("#---\n# agent: a\n#---\np() { cat <<'EOF'\nhi\nEOF\n}\n" +
		"#---\n# tool: t\n# description: x\n# params:\n#   my-arg: {type: string}\n#---\nf() { :; }\n")
	if _, err := Parse(src); err == nil {
		t.Fatal("want error: a hyphenated param cannot be an environment variable")
	}
}

func TestParseParamShadowingPathFails(t *testing.T) {
	src := []byte("#---\n# agent: a\n#---\np() { cat <<'EOF'\nhi\nEOF\n}\n" +
		"#---\n# tool: t\n# description: x\n# params:\n#   PATH: {type: string}\n#---\nf() { :; }\n")
	if _, err := Parse(src); err == nil {
		t.Fatal("want error: a param must not shadow PATH")
	}
}

func TestParsePromptBodies(t *testing.T) {
	k, err := Parse([]byte(sample))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if k.Agents[0].Prompt != "hello" {
		t.Fatalf("main prompt = %q, want %q", k.Agents[0].Prompt, "hello")
	}
	if k.Agents[1].Prompt != "find shops" {
		t.Fatalf("bm prompt = %q", k.Agents[1].Prompt)
	}
}

func TestParsePromptWithoutHeredocFails(t *testing.T) {
	src := []byte("#---\n# agent: a\n#---\np() { echo hi; }\n")
	if _, err := Parse(src); err == nil {
		t.Fatal("want error for a prompt with no heredoc")
	}
}
