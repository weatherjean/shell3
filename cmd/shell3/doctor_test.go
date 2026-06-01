package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/weatherjean/shell3/internal/bootstrap"
	"github.com/weatherjean/shell3/internal/paths"
)

func TestDoctorAllGreen(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()

	g := paths.NewGlobal(home)
	l := paths.NewLocal(cwd)
	_ = bootstrap.EnsureGlobal(g)
	_, _ = bootstrap.EnsureProject(l, g, cwd)

	cfg := `shell3.model("test", { base_url = "https://api.openai.com/v1", api_key = "sk-test", model = "gpt-4o", context_window = 128000 })
shell3.agent({ name = "tester", model = "test", prompt = "p", tools = {} })
`
	if err := os.WriteFile(filepath.Join(cwd, "shell3.lua"), []byte(cfg), 0600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	code := runDoctor(home, cwd, &out)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\noutput:\n%s", code, out.String())
	}
	if !bytes.Contains(out.Bytes(), []byte("agent: tester")) {
		t.Errorf("output missing agent check:\n%s", out.String())
	}
}

func TestDoctorMissingCredentials(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()

	g := paths.NewGlobal(home)
	l := paths.NewLocal(cwd)
	_ = bootstrap.EnsureGlobal(g)
	_, _ = bootstrap.EnsureProject(l, g, cwd)

	var out bytes.Buffer
	code := runDoctor(home, cwd, &out)
	if code == 0 {
		t.Fatalf("expected non-zero exit, got 0\noutput:\n%s", out.String())
	}
	if !bytes.Contains(out.Bytes(), []byte("no shell3.lua found")) {
		t.Errorf("expected 'no shell3.lua found' in output:\n%s", out.String())
	}
}

func TestDoctorMissingRef(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()

	g := paths.NewGlobal(home)
	_ = bootstrap.EnsureGlobal(g)
	// No project bootstrap — no .ref file

	var out bytes.Buffer
	code := runDoctor(home, cwd, &out)
	if code == 0 {
		t.Fatalf("expected non-zero exit for missing .ref\noutput:\n%s", out.String())
	}
}
