package shell3_test

import (
	"os"
	"path/filepath"
	"testing"
)

// baseYAML + baseAgentMD are the minimal valid config tree every runtime test
// starts from.
const baseYAML = `models:
  main:
    base_url: https://api.x/v1
    api_key: k
    model: m-1
    context_window: 1000
`

const baseAgentMD = "---\nmodel: main\n---\nhi\n"

// writeTreeFiles writes the given files (path → content, relative to dir,
// subdirs created) into dir.
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

// writeBaseTree writes the minimal valid config tree into dir plus extras.
func writeBaseTree(t *testing.T, dir string, extra map[string]string) {
	t.Helper()
	files := map[string]string{"shell3.yaml": baseYAML, "agent.md": baseAgentMD}
	for k, v := range extra {
		files[k] = v
	}
	writeTreeFiles(t, dir, files)
}
