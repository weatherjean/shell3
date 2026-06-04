package luacfg

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestExampleParses(t *testing.T) {
	_, thisFile, _, _ := runtime.Caller(0)
	// thisFile is .../internal/luacfg/example_test.go; the canonical config
	// lives in the scaffold package's embedded defaults.
	root := filepath.Join(filepath.Dir(thisFile), "..", "..")
	examplePath := filepath.Join(root, "internal", "scaffold", "defaults", "shell3.lua")

	// Copy the example lua into a temp dir so we can provide a .env without
	// touching the working tree (shell3.env.secret reads .env from the workdir).
	tmp := t.TempDir()

	src, err := os.ReadFile(examplePath)
	if err != nil {
		t.Fatalf("read scaffold default shell3.lua: %v", err)
	}
	luaPath := filepath.Join(tmp, "shell3.lua")
	if err := os.WriteFile(luaPath, src, 0600); err != nil {
		t.Fatalf("write temp lua: %v", err)
	}

	// Write a .env with dummy values so env.secret("...") succeeds.
	dotenv := "OPENCODE_KEY=x\nBRAVE_API_KEY=y\n"
	if err := os.WriteFile(filepath.Join(tmp, ".env"), []byte(dotenv), 0600); err != nil {
		t.Fatalf("write temp .env: %v", err)
	}

	c, err := Load(luaPath, tmp)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer c.Close()

	if c.Active().Name == "" {
		t.Fatal("agent name is empty")
	}
	if len(c.Models) < 1 {
		t.Fatalf("expected >= 1 model, got %d", len(c.Models))
	}
	if len(c.Skills) != 5 {
		t.Fatalf("expected 5 skills, got %d", len(c.Skills))
	}
	if len(c.Tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(c.Tools))
	}

	// Two agents: "base" (first/active) and read-only "plan" companion.
	agents := c.Agents()
	if len(agents) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(agents))
	}
	if agents[0].Name != "base" {
		t.Errorf("first agent: want %q, got %q", "base", agents[0].Name)
	}
	if agents[1].Name != "plan" {
		t.Errorf("second agent: want %q, got %q", "plan", agents[1].Name)
	}
}
