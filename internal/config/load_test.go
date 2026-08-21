package config

import (
	"strings"
	"testing"
)

func TestLoadFullTree(t *testing.T) {
	c := mustLoad(t, map[string]string{".env": "KEY=val\n"})
	if m, ok := c.Model("m1"); !ok || m.ModelID != "test-model" {
		t.Fatalf("model = %+v ok=%v", m, ok)
	}
	if c.Secrets["KEY"] != "val" {
		t.Fatalf("secrets = %v", c.Secrets)
	}
	if len(c.Warnings()) != 0 {
		t.Fatalf("warnings = %v", c.Warnings())
	}
}

// The kit IS the config: a directory without one does not load, and the error
// names the file to create.
func TestLoadMissingKit(t *testing.T) {
	dir := t.TempDir()
	_, err := Load(dir)
	if err == nil || !strings.Contains(err.Error(), KitFileName) {
		t.Fatalf("empty dir err = %v, want one naming %s", err, KitFileName)
	}
	// A leftover shell3.yaml is not a config — the markdown format is gone,
	// and silently half-loading one would be worse than saying so.
	writeFile(t, dir, "shell3.yaml", "models:\n  m1:\n    base_url: u\n    model: x\n    api_key: k\n")
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), KitFileName) {
		t.Fatalf("shell3.yaml-only err = %v, want one naming %s", err, KitFileName)
	}
}

func TestLoadSecrets(t *testing.T) {
	c := mustLoad(t, map[string]string{
		".env": "MY_KEY=s3cret\n",
		KitFileName: `#---
# shell3:
#   models:
#     m1:
#       base_url: u
#       model: x
#       api_key: env:MY_KEY
#---

#---
# agent: main
# model: m1
#---
main_prompt() { cat <<'EOF'
hi
EOF
}
`,
	})
	m, _ := c.Model("m1")
	if m.APIKey != "s3cret" {
		t.Fatalf("api key = %q", m.APIKey)
	}
}
