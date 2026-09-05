package lispconfig

import (
	"strings"
	"testing"
)

func TestResolveSecretUsesProcessEnvironment(t *testing.T) {
	t.Setenv("MODEL_KEY", "from-process")
	value, err := ResolveSecret("MODEL_KEY")
	if err != nil || value != "from-process" {
		t.Fatalf("secret = %q, err=%v", value, err)
	}
}

func TestResolveSecretRejectsMissingAndEmpty(t *testing.T) {
	t.Setenv("MODEL_KEY", "")
	if _, err := ResolveSecret("MODEL_KEY"); err == nil || !strings.Contains(err.Error(), "is empty") {
		t.Fatalf("empty error = %v", err)
	}
	if _, err := ResolveSecret("MISSING_KEY"); err == nil || !strings.Contains(err.Error(), "is absent") {
		t.Fatalf("missing error = %v", err)
	}
}
