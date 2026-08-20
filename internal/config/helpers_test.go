package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// minWiring is the smallest valid `shell3:` block (one model), as it appears
// inside a kit's comment fence.
const minWiring = `#---
# shell3:
#   models:
#     m1:
#       base_url: https://api.example.com/v1
#       api_key: k
#       model: test-model
#---
`

// minKit is the smallest valid shell3.sh: wiring plus one agent.
const minKit = minWiring + `
#---
# agent: main
# model: m1
# use: [bash]
#---
main_prompt() { cat <<'EOF'
You are a test agent.
EOF
}
`

// writeTree writes a minimal valid config tree plus the given extra files
// (path → content, paths relative to dir, subdirs created).
func writeTree(t *testing.T, dir string, extra map[string]string) {
	t.Helper()
	if _, ok := extra[KitFileName]; !ok {
		writeFile(t, dir, KitFileName, minKit)
	}
	for name, body := range extra {
		writeFile(t, dir, name, body)
	}
}

// mustLoad writes a minimal tree (plus extras) and loads it. Fatal on error.
func mustLoad(t *testing.T, extra map[string]string) *LoadedConfig {
	t.Helper()
	dir := t.TempDir()
	writeTree(t, dir, extra)
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// kitWith renders a kit declaring main + explorer, plus the given gate/note
// functions. gates and notes map a shell function name to its body; each is
// declared for the agents named in its `for` list.
type hookDecl struct {
	fn    string   // function name
	body  string   // function body (shell statements)
	forAg []string // agents the declaration governs
}

func kitWith(gates, notes []hookDecl) string {
	var b strings.Builder
	b.WriteString(minWiring)
	b.WriteString(`
#---
# agent: main
# model: m1
# use: [bash]
#---
main_prompt() { cat <<'EOF'
You are a test agent.
EOF
}

#---
# agent: explorer
# description: explores
# model: m1
# use: [bash]
#---
explorer_prompt() { cat <<'EOF'
Explore.
EOF
}
`)
	for _, set := range []struct {
		kind  string
		decls []hookDecl
	}{{"gate", gates}, {"note", notes}} {
		for _, d := range set.decls {
			b.WriteString("\n#---\n# " + set.kind + ": [" + strings.Join(d.forAg, ", ") + "]\n#---\n")
			b.WriteString(d.fn + "() {\n" + d.body + "\n}\n")
		}
	}
	return b.String()
}
