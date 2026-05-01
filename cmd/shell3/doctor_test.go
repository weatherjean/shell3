package main

import (
	"bytes"
	"testing"

	"github.com/weatherjean/shell3/internal/bootstrap"
	"github.com/weatherjean/shell3/internal/config"
	"github.com/weatherjean/shell3/internal/paths"
)

func TestDoctorAllGreen(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()

	g := paths.NewGlobal(home)
	l := paths.NewLocal(cwd)
	bootstrap.EnsureGlobal(g)
	bootstrap.EnsureProject(l, g, cwd)

	cs, _ := config.LoadCredStore(home)
	cs.Set("test", "openai", map[string]string{"api_key": "sk-test"})

	var out bytes.Buffer
	code := runDoctor(home, cwd, &out)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\noutput:\n%s", code, out.String())
	}
	if !bytes.Contains(out.Bytes(), []byte("credentials")) {
		t.Errorf("output missing credentials check:\n%s", out.String())
	}
}

func TestDoctorMissingCredentials(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()

	g := paths.NewGlobal(home)
	l := paths.NewLocal(cwd)
	bootstrap.EnsureGlobal(g)
	bootstrap.EnsureProject(l, g, cwd)

	var out bytes.Buffer
	code := runDoctor(home, cwd, &out)
	if code == 0 {
		t.Fatalf("expected non-zero exit, got 0\noutput:\n%s", out.String())
	}
	if !bytes.Contains(out.Bytes(), []byte("no credentials")) {
		t.Errorf("expected 'no credentials' in output:\n%s", out.String())
	}
}

func TestDoctorMissingRef(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()

	g := paths.NewGlobal(home)
	bootstrap.EnsureGlobal(g)
	// No project bootstrap — no .ref file

	var out bytes.Buffer
	code := runDoctor(home, cwd, &out)
	if code == 0 {
		t.Fatalf("expected non-zero exit for missing .ref\noutput:\n%s", out.String())
	}
}
