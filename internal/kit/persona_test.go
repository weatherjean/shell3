package kit

import (
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
# use: [bash, web, mcp:tavily]
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

// The agent you talk to gets every built-in without declaring one — except
// media, which needs a multimodal model and so stays opt-in.
func TestResolveMainGetsDefaultBuiltins(t *testing.T) {
	k := mustParse(t, capKit)
	r, err := k.Resolve(k.Agents[0], true)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(r.Builtins) != len(mainDefaults) {
		t.Fatalf("main builtins = %v, want %v", r.Builtins, mainDefaults)
	}
	for _, b := range r.Builtins {
		if b == "media" {
			t.Fatal("media must not be granted implicitly — read_media needs a multimodal model")
		}
	}
}

// media reaches the main agent only when it is declared.
func TestResolveMainMediaOptIn(t *testing.T) {
	src := `#---
# agent: main
# use: [media]
#---
main_prompt() { cat <<'EOF2'
hi
EOF2
}
`
	k := mustParse(t, src)
	r, err := k.Resolve(k.Agents[0], true)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	var got bool
	for _, b := range r.Builtins {
		if b == "media" {
			got = true
		}
	}
	if !got {
		t.Fatalf("media not granted despite use: [media]; builtins = %v", r.Builtins)
	}
}

// An employee gets exactly what it names, and nothing else.
func TestResolveEmployeeGetsOnlyDeclared(t *testing.T) {
	k := mustParse(t, capKit)
	r, err := k.Resolve(k.Agents[1], false)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(r.Builtins) != 1 || r.Builtins[0] != "bash" {
		t.Fatalf("builtins = %v, want [bash]", r.Builtins)
	}
	if len(r.MCP) != 1 || r.MCP[0] != "tavily" {
		t.Fatalf("mcp = %v, want [tavily]", r.MCP)
	}
	if len(r.Tools) != 2 {
		t.Fatalf("tools = %+v, want local + search", r.Tools)
	}
	if _, ok := r.ToolByName("search"); !ok {
		t.Fatal("shared group tool did not reach the agent")
	}
}

// A typo in use: must fail loudly, not silently mean "no capability".
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
	k := mustParse(t, src)
	if _, err := k.Resolve(k.Agents[0], false); err == nil {
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
	k := mustParse(t, src)
	if _, err := k.Resolve(k.Agents[0], false); err == nil {
		t.Fatal("want error when a shared tool collides with a local one")
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

// Kit.ToolByName is the whole-kit search a cron tool: job uses (it names no
// agent, so there is no Resolved capability set to search) — it must find a
// tool declared under an employee's own scope AND one that only exists via a
// shared group, and report false for a name nothing declares.
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

// The duplicate-tool-name check in Resolve/Check is per-scope only (see
// TestSharedToolNameCollidesWithLocal): two agents may each legally declare a
// tool called "sync". ToolByName resolves that ambiguity silently
// (first-match-wins); ToolMatches must instead surface BOTH declaring scopes
// so a caller like `shell3 health`'s cron block can refuse instead of
// running whichever function happened to parse first.
func TestKitToolMatchesAmbiguousAcrossAgents(t *testing.T) {
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
	matches := k.ToolMatches("sync")
	if len(matches) != 2 {
		t.Fatalf("ToolMatches(sync) = %+v, want 2 matches (declared under both alpha and beta)", matches)
	}
	if matches[0].Scope == matches[1].Scope {
		t.Fatalf("both matches report the same scope %q; want one per declaring agent", matches[0].Scope)
	}
	for _, m := range matches {
		if !strings.Contains(m.Scope, "alpha") && !strings.Contains(m.Scope, "beta") {
			t.Fatalf("match scope %q names neither declaring agent", m.Scope)
		}
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
