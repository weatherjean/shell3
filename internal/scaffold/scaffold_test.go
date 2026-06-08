package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderBaseConfig(t *testing.T) {
	dir := t.TempDir()
	v := Values{Name: "main", BaseURL: "http://localhost:8787/v1", EnvKey: "MAIN_API_KEY", Model: "kimi-k2.6", Proxy: ""}
	if err := RenderBaseConfig(dir, v); err != nil {
		t.Fatalf("RenderBaseConfig: %v", err)
	}
	cfg, err := os.ReadFile(filepath.Join(dir, "shell3.lua"))
	if err != nil {
		t.Fatalf("read shell3.lua: %v", err)
	}
	for _, want := range []string{
		`shell3.model("main"`,
		`base_url       = "http://localhost:8787/v1"`,
		`shell3.env.secret("MAIN_API_KEY")`,
		`model          = "kimi-k2.6"`,
		`name  = "code"`,
		`name  = "plan"`,
		`-- run_proxy   = "npx`,
	} {
		if !strings.Contains(string(cfg), want) {
			t.Errorf("shell3.lua missing %q", want)
		}
	}
	if strings.Contains(string(cfg), "{{") {
		t.Errorf("shell3.lua still contains an unrendered template delimiter")
	}
	for _, p := range []string{
		"lib/tools.lua", "lib/guards.lua",
		"lib/skills/brainstorming.lua", "lib/skills/subagents.lua",
	} {
		if _, err := os.Stat(filepath.Join(dir, p)); err != nil {
			t.Errorf("missing %s: %v", p, err)
		}
	}
}

func TestRenderBaseConfigWithProxy(t *testing.T) {
	dir := t.TempDir()
	v := Values{Name: "main", BaseURL: "http://x/v1", EnvKey: "MAIN_API_KEY", Model: "m", Proxy: "npx codex-proxy --port 8787"}
	if err := RenderBaseConfig(dir, v); err != nil {
		t.Fatalf("RenderBaseConfig: %v", err)
	}
	cfg, _ := os.ReadFile(filepath.Join(dir, "shell3.lua"))
	if !strings.Contains(string(cfg), `run_proxy      = "npx codex-proxy --port 8787"`) {
		t.Errorf("proxy not wired into shell3.lua:\n%s", cfg)
	}
}

func TestRenderBaseConfigDoesNotClobber(t *testing.T) {
	dir := t.TempDir()
	v := Values{Name: "main", BaseURL: "http://x/v1", EnvKey: "MAIN_API_KEY", Model: "m"}
	if err := RenderBaseConfig(dir, v); err != nil {
		t.Fatalf("first render: %v", err)
	}
	cfgPath := filepath.Join(dir, "shell3.lua")
	if err := os.WriteFile(cfgPath, []byte("-- user edited\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := RenderBaseConfig(dir, v); err != nil {
		t.Fatalf("second render: %v", err)
	}
	got, _ := os.ReadFile(cfgPath)
	if string(got) != "-- user edited\n" {
		t.Errorf("RenderBaseConfig clobbered an existing shell3.lua")
	}
}
