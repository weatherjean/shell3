package config

import (
	"os"
	"path/filepath"
	"testing"
)

const wiringKit = `#---
# shell3:
#   models:
#     m1: {base_url: "http://x", api_key: k, model: mm}
#---

#---
# agent: main
# model: m1
#---
main_prompt() { cat <<'EOF2'
hi
EOF2
}
`

// A directory holding only shell3.sh is a complete config: the kit carries its
// own wiring.
func TestLoadKitOnlyDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "shell3.sh"), []byte(wiringKit), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := c.Model("m1"); !ok {
		t.Fatalf("models = %+v, want m1 lifted from the kit's shell3: block", c.Models)
	}
}

func TestLoadKitWithoutWiringFails(t *testing.T) {
	dir := t.TempDir()
	src := "#---\n# agent: main\n#---\nmain_prompt() { cat <<'EOF2'\nhi\nEOF2\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "shell3.sh"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("want error: a kit with no shell3: block declares no models")
	}
}

func TestLoadNoConfigAtAllFails(t *testing.T) {
	if _, err := Load(t.TempDir()); err == nil {
		t.Fatal("want error for a directory with no shell3.sh")
	}
}
