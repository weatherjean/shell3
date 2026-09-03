package lispconfig

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveSecretUsesProcessEnvironment(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "shell3.lisp")
	t.Setenv("MODEL_KEY", "from-process")
	value, err := ResolveSecret(configPath, "MODEL_KEY")
	if err != nil || value != "from-process" {
		t.Fatalf("secret = %q, err=%v", value, err)
	}
}

func TestResolveSecretRejectsMissingAndEmpty(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "shell3.lisp")
	t.Setenv("MODEL_KEY", "")
	if _, err := ResolveSecret(configPath, "MODEL_KEY"); err == nil || !strings.Contains(err.Error(), "is empty") {
		t.Fatalf("empty error = %v", err)
	}
	if _, err := ResolveSecret(configPath, "MISSING_KEY"); err == nil || !strings.Contains(err.Error(), "is absent") {
		t.Fatalf("missing error = %v", err)
	}
}
