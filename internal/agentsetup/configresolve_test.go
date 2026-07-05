package agentsetup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExpandConfigName(t *testing.T) {
	home := "/home/u"
	cases := []struct {
		name string
		flag string
		want string
	}{
		{"empty passes through", "", ""},
		{"bare name resolves under ~/.shell3", "code", filepath.Join(home, ".shell3", "code.lua")},
		{"another bare name", "work", filepath.Join(home, ".shell3", "work.lua")},
		{"value ending in .lua is a literal path", "/abs/path/shell3.lua", "/abs/path/shell3.lua"},
		{"relative .lua path stays literal", "./foo.lua", "./foo.lua"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ExpandConfigName(tc.flag, home); got != tc.want {
				t.Errorf("ExpandConfigName(%q) = %q, want %q", tc.flag, got, tc.want)
			}
		})
	}
}

func TestResolveConfigPath(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	shell3Dir := filepath.Join(home, ".shell3")
	if err := os.MkdirAll(shell3Dir, 0o755); err != nil {
		t.Fatal(err)
	}

	// A bare name resolves to ~/.shell3/<name>.lua when that file exists.
	if err := os.WriteFile(filepath.Join(shell3Dir, "code.lua"), []byte("--"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveConfigPath("code", home)
	if err != nil {
		t.Fatalf("name: %v", err)
	}
	if want := filepath.Join(shell3Dir, "code.lua"); got != want {
		t.Errorf("name: got %q, want %q", got, want)
	}

	// A typo'd name fails with a clear message instead of a later DoFile error.
	if _, err := ResolveConfigPath("no-such-name", home); err == nil || !strings.Contains(err.Error(), "no such config") {
		t.Errorf("typo'd name: want 'no such config' error, got %v", err)
	}

	// A literal *.lua path is returned unchanged (when it exists).
	lit := filepath.Join(cwd, "custom.lua")
	if err := os.WriteFile(lit, []byte("--"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := ResolveConfigPath(lit, home); err != nil || got != lit {
		t.Errorf("literal path: got %q err %v, want %q", got, err, lit)
	}

	// A project-local ./shell3.lua must NOT be picked up anymore.
	if err := os.WriteFile(filepath.Join(cwd, "shell3.lua"), []byte("--"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveConfigPath("", home); err == nil {
		t.Error("empty flag: expected error (cwd shell3.lua must be ignored, ~/.shell3/shell3.lua absent)")
	}

	// With ~/.shell3/shell3.lua present, empty flag resolves to it.
	global := filepath.Join(shell3Dir, "shell3.lua")
	if err := os.WriteFile(global, []byte("--"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := ResolveConfigPath("", home); err != nil || got != global {
		t.Errorf("default: got %q err %v, want %q", got, err, global)
	}
}
