package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/kit"
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

func writeTree(t *testing.T, dir string, extra map[string]string) {
	t.Helper()
	if _, ok := extra[kit.FileName]; !ok {
		writeFile(t, dir, kit.FileName, minKit)
	}
	for name, body := range extra {
		writeFile(t, dir, name, body)
	}
}

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

type hookDecl struct {
	fn    string
	body  string
	forAg []string
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
