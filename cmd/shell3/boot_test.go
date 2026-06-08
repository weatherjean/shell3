package main

import (
	"strings"
	"testing"
)

func TestMergeEnvAddsMissingKeysOnly(t *testing.T) {
	existing := "FOO=bar\nMAIN_API_KEY=old\n"
	out := mergeEnv(existing, [][2]string{
		{"MAIN_API_KEY", "new"},
		{"BRAVE_API_KEY", "xyz"},
	})
	if !strings.Contains(out, "MAIN_API_KEY=old") {
		t.Errorf("must not overwrite existing key; got:\n%s", out)
	}
	if strings.Contains(out, "MAIN_API_KEY=new") {
		t.Errorf("must not append a duplicate for an existing key; got:\n%s", out)
	}
	if !strings.Contains(out, "BRAVE_API_KEY=xyz") {
		t.Errorf("must append missing key; got:\n%s", out)
	}
	if !strings.Contains(out, "FOO=bar") {
		t.Errorf("must preserve unrelated keys; got:\n%s", out)
	}
}

func TestMergeEnvFromEmpty(t *testing.T) {
	out := mergeEnv("", [][2]string{{"MAIN_API_KEY", "k"}, {"BRAVE_API_KEY", ""}})
	if !strings.Contains(out, "MAIN_API_KEY=k") || !strings.Contains(out, "BRAVE_API_KEY=") {
		t.Errorf("missing expected keys; got:\n%s", out)
	}
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("env file must end with newline; got:\n%q", out)
	}
}

func TestEnvKeyForName(t *testing.T) {
	if got := envKeyForName("main"); got != "MAIN_API_KEY" {
		t.Errorf("envKeyForName(main) = %q, want MAIN_API_KEY", got)
	}
	if got := envKeyForName("kimi-k2"); got != "KIMI_K2_API_KEY" {
		t.Errorf("envKeyForName(kimi-k2) = %q, want KIMI_K2_API_KEY", got)
	}
	// Degenerate handles must still yield a valid identifier.
	if got := envKeyForName("@@@"); got != "MAIN_API_KEY" {
		t.Errorf("envKeyForName(@@@) = %q, want MAIN_API_KEY (empty -> fallback)", got)
	}
	if got := envKeyForName(""); got != "MAIN_API_KEY" {
		t.Errorf("envKeyForName(empty) = %q, want MAIN_API_KEY", got)
	}
	if got := envKeyForName("123model"); got != "_123MODEL_API_KEY" {
		t.Errorf("envKeyForName(123model) = %q, want _123MODEL_API_KEY (leading digit)", got)
	}
}
