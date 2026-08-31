package shell3_test

import (
	"os"
	"path/filepath"
	"testing"
)

const baseWiring = `#---
# shell3:
#   models:
#     main:
#       base_url: https://api.x/v1
#       api_key: k
#       model: m-1
#       context_window: 1000
#---
`

// kitAgentDecl renders one `agent:` block plus its prompt function. opts are
// extra frontmatter lines (already unprefixed), body is the prompt text.
func kitAgentDecl(name, body string, opts ...string) string {
	out := "\n#---\n# agent: " + name + "\n# model: main\n"
	for _, o := range opts {
		out += "# " + o + "\n"
	}
	return out + "#---\n" + name + "_prompt() { cat <<'SHELL3_EOF'\n" + body + "\nSHELL3_EOF\n}\n"
}

var baseKit = baseWiring + kitAgentDecl("main", "hi")

func writeTreeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func writeBaseTree(t *testing.T, dir string, extra map[string]string) {
	t.Helper()
	files := map[string]string{"shell3.sh": baseKit}
	for k, v := range extra {
		files[k] = v
	}
	writeTreeFiles(t, dir, files)
}
