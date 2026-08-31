package kit

import (
	"slices"
	"strings"
	"testing"
)

const capKit = `#---
# agent: main
#---
main_prompt() { cat <<'EOF'
I am the main agent.
EOF
}

#---
# agent: worker
# description: does worker jobs
# use: [bash, web]
# mcp: [tavily]
#---
worker_prompt() { cat <<'EOF'
I do one job.
EOF
}

#---
# tool: local
# description: a local verb
#---
worker_local() { echo local; }

#---
# shared: web
#---
#---
# tool: search
# description: search the web
# params:
#   q: {type: string, required: true}
#---
web_search() { echo "q=$q"; }
`

func TestResolveMainGetsDefaultBuiltins(t *testing.T) {
	k := mustParse(t, capKit)
	r, err := k.Resolve(k.Agents[0], true)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(r.Builtins) != len(Builtins) {
		t.Fatalf("main builtins = %v, want %v", r.Builtins, Builtins)
	}
}

func TestResolveEmployeeGetsOnlyDeclared(t *testing.T) {
	k := mustParse(t, capKit)
	r, err := k.Resolve(k.Agents[1], false)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(r.Builtins) != 1 || r.Builtins[0] != "bash" {
		t.Fatalf("builtins = %v, want [bash]", r.Builtins)
	}
	if len(r.Agent.MCP) != 1 || r.Agent.MCP[0] != "tavily" {
		t.Fatalf("mcp = %v, want [tavily]", r.Agent.MCP)
	}
	if len(r.Tools) != 2 {
		t.Fatalf("tools = %+v, want local + search", r.Tools)
	}
	if _, ok := r.ToolByName("search"); !ok {
		t.Fatal("shared group tool did not reach the agent")
	}
}

func TestResolveUnknownUseEntryFails(t *testing.T) {
	src := `#---
# agent: a
# use: [notathing]
#---
a_prompt() { cat <<'EOF'
hi
EOF
}
`
	if _, err := Parse([]byte(src)); err == nil {
		t.Fatal("want error for an unresolvable use: entry")
	}
}

func TestResolveCollisionFails(t *testing.T) {
	src := `#---
# agent: a
# use: [g]
#---
a_prompt() { cat <<'EOF'
hi
EOF
}

#---
# tool: dup
# description: local
#---
a_dup() { :; }

#---
# shared: g
#---
#---
# tool: dup
# description: shared
#---
g_dup() { :; }
`
	if _, err := Parse([]byte(src)); err == nil {
		t.Fatal("want error when a shared tool collides with a local one")
	}
}

func TestUseMCPPrefixIsADirectedError(t *testing.T) {
	src := `#---
# agent: main
# use: [mcp:tavily]
#---
main_prompt() { cat <<'EOF'
hi
EOF
}
`
	_, err := Parse([]byte(src))
	if err == nil || !strings.Contains(err.Error(), "mcp: list") {
		t.Fatalf("want a directed mcp: error, got %v", err)
	}
}

func TestToolDefsSchema(t *testing.T) {
	k := mustParse(t, capKit)
	r, err := k.Resolve(k.Agents[1], false)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	defs := r.ToolDefs()
	if len(defs) != 2 || defs[0].Name != "local" || defs[1].Name != "search" {
		t.Fatalf("defs = %+v, want sorted local, search", defs)
	}
	if defs[1].Description != "search the web" {
		t.Fatalf("search description = %q", defs[1].Description)
	}
	props, _ := defs[1].Parameters["properties"].(map[string]any)
	if _, ok := props["q"]; !ok {
		t.Fatalf("search params = %#v", defs[1].Parameters)
	}
}

func TestKitToolByName(t *testing.T) {
	k := mustParse(t, capKit)
	if tl, ok := k.ToolByName("local"); !ok || tl.Name != "local" {
		t.Fatalf("ToolByName(local) = %+v, %v; want the worker-scoped tool", tl, ok)
	}
	if tl, ok := k.ToolByName("search"); !ok || tl.Name != "search" {
		t.Fatalf("ToolByName(search) = %+v, %v; want the shared-group tool", tl, ok)
	}
	if _, ok := k.ToolByName("no-such-tool"); ok {
		t.Fatal("ToolByName should report false for an undeclared name")
	}
}

func TestKitToolByNameUsesDeclarationOrderAcrossAgents(t *testing.T) {
	src := `#---
# agent: alpha
#---
alpha_prompt() { cat <<'EOF'
a
EOF
}

#---
# tool: sync
# description: alpha's sync
#---
alpha_sync() { echo alpha; }

#---
# agent: beta
# description: second test agent
#---
beta_prompt() { cat <<'EOF'
b
EOF
}

#---
# tool: sync
# description: beta's sync
#---
beta_sync() { echo beta; }
	`
	k := mustParse(t, src)
	got, ok := k.ToolByName("sync")
	if !ok || got.Func != "alpha_sync" {
		t.Fatalf("ToolByName(sync) = %+v, %v; want first-declared alpha_sync", got, ok)
	}
}

func TestParseAgentContext(t *testing.T) {
	src := `#---
# agent: a
# context: [memory.md, notes/*.md]
#---
a_prompt() { cat <<'EOF2'
hi
EOF2
}
`
	k := mustParse(t, src)
	got := k.Agents[0].Context
	if len(got) != 2 || got[0] != "memory.md" || got[1] != "notes/*.md" {
		t.Fatalf("context = %v", got)
	}
}

func TestUseMediaIsANamedRemoval(t *testing.T) {
	k := &Kit{}
	_, err := k.Resolve(Agent{Name: "main", Use: []string{"media"}}, true)
	if err == nil {
		t.Fatal("use: [media] must be a load error, got nil")
	}
	for _, want := range []string{"read_media tool was removed", "using-llms"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error must name the replacement; missing %q in: %v", want, err)
		}
	}
}

func TestBuiltinsNoLongerCarryMedia(t *testing.T) {
	if slices.Contains(Builtins, "media") {
		t.Fatal("media is not a built-in any more")
	}
	r, err := (&Kit{}).Resolve(Agent{Name: "main"}, true)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"bash", "bash_bg", "edit", "history"}
	if !slices.Equal(r.Builtins, want) {
		t.Errorf("main defaults = %v, want %v", r.Builtins, want)
	}
}
