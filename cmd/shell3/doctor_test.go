package main

import (
	"bytes"
	"os"
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

	authYAML := `instances:
  - name: test
    base_url: https://api.openai.com/v1
    api_key: sk-test
    models:
      - id: gpt-4o
        context_window: 128000
`
	if err := os.WriteFile(g.Auth, []byte(authYAML), 0600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	code := runDoctor(home, cwd, &out)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\noutput:\n%s", code, out.String())
	}
	if !bytes.Contains(out.Bytes(), []byte("instances")) {
		t.Errorf("output missing instances check:\n%s", out.String())
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
	if !bytes.Contains(out.Bytes(), []byte("no instances")) {
		t.Errorf("expected 'no instances' in output:\n%s", out.String())
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
