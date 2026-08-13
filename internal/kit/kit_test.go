package kit

import "testing"

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
# agent: ampd-leads
# description: lead-gen
# model: sonnet
# workdir: ~/ampd-leads
# use: [bash, web]
#---
ampd_prompt() { cat <<'EOF'
find shops
EOF
}

#---
# tool: stack-check
# description: Classify a stack
# params:
#   url: {type: string, required: true}
#---
ampd_stack_check() {
  curl -sL "$url"
}

#---
# skill: qualify
#---
ampd_skill_qualify() { cat <<'EOF'
a real shop has a cart
EOF
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

	ampd := k.Agents[1]
	if ampd.Workdir != "~/ampd-leads" || len(ampd.Use) != 2 {
		t.Fatalf("ampd = %+v", ampd)
	}
	if len(ampd.Tools) != 1 || ampd.Tools[0].Name != "stack-check" ||
		ampd.Tools[0].Func != "ampd_stack_check" {
		t.Fatalf("ampd tools = %+v", ampd.Tools)
	}
	if len(ampd.Skills) != 1 || ampd.Skills[0].Func != "ampd_skill_qualify" {
		t.Fatalf("ampd skills = %+v", ampd.Skills)
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
		"#---\n# agent: a\n#---\ndup() { :; }\n" +
			"#---\n# agent: b\n#---\ndup() { :; }\n")
	if _, err := Parse(src); err == nil {
		t.Fatal("want error for a duplicate function name")
	}
}

func TestParseDuplicateAgentNameFails(t *testing.T) {
	src := []byte(
		"#---\n# agent: a\n#---\nf1() { :; }\n" +
			"#---\n# agent: a\n#---\nf2() { :; }\n")
	if _, err := Parse(src); err == nil {
		t.Fatal("want error for a duplicate agent name")
	}
}

func TestParseBlockWithNoFunctionFails(t *testing.T) {
	src := []byte("#---\n# agent: a\n#---\nf() { :; }\n#---\n# tool: t\n# description: x\n#---\n")
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
		"#---\n# agent: a\n#---\np() { :; }\n" +
			"#---\n# tool: t\n# description: x\n#---\nf1() { :; }\n" +
			"#---\n# tool: t\n# description: y\n#---\nf2() { :; }\n")
	if _, err := Parse(src); err == nil {
		t.Fatal("want error for a duplicate declared tool name in one scope")
	}
}

func TestParseToolNeedsDescription(t *testing.T) {
	src := []byte(
		"#---\n# agent: a\n#---\np() { :; }\n" +
			"#---\n# tool: t\n#---\nf() { :; }\n")
	if _, err := Parse(src); err == nil {
		t.Fatal("want error for a tool with no description")
	}
}

func TestParseBadParamTypeFails(t *testing.T) {
	src := []byte("#---\n# agent: a\n#---\nf() { :; }\n" +
		"#---\n# tool: t\n# description: x\n# params:\n#   n: {type: float}\n#---\ng() { :; }\n")
	if _, err := Parse(src); err == nil {
		t.Fatal("want error for an unsupported param type")
	}
}

func TestParseBadParamNameFails(t *testing.T) {
	src := []byte("#---\n# agent: a\n#---\np() { :; }\n" +
		"#---\n# tool: t\n# description: x\n# params:\n#   my-arg: {type: string}\n#---\nf() { :; }\n")
	if _, err := Parse(src); err == nil {
		t.Fatal("want error: a hyphenated param cannot be an environment variable")
	}
}

func TestParseParamShadowingPathFails(t *testing.T) {
	src := []byte("#---\n# agent: a\n#---\np() { :; }\n" +
		"#---\n# tool: t\n# description: x\n# params:\n#   PATH: {type: string}\n#---\nf() { :; }\n")
	if _, err := Parse(src); err == nil {
		t.Fatal("want error: a param must not shadow PATH")
	}
}
