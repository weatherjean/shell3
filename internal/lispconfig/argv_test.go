package lispconfig

import (
	"slices"
	"testing"
)

func TestArgvResolvesLiteralsParametersAndRuntimeSlots(t *testing.T) {
	cfg, err := Parse("shell3.lisp", []byte(validConfig))
	if err != nil {
		t.Fatal(err)
	}
	argv, err := cfg.Argv("builder", map[string]string{
		"result-file": "/tmp/result", "workdir": "/work", "prompt-file": "/tmp/prompt",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"codex", "exec", "--json", "--output-last-message", "/tmp/result", "--cd", "/work", "--model", "gpt-test", "--profile", "automation", "-"}
	if !slices.Equal(argv, want) {
		t.Fatalf("argv = %#v\nwant = %#v", argv, want)
	}
}

func TestArgvOmitsUnsetOptionalParameter(t *testing.T) {
	src := `(shell3
  (version 1)
  (runner r
    (parameters (profile string optional))
    (command "run")
    (arguments (optional profile "--profile" profile))
    (result stdout))
  (agent a (using r)))`
	cfg, err := Parse("shell3.lisp", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	argv, err := cfg.Argv("a", map[string]string{"prompt-file": "/tmp/p"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(argv, []string{"run"}) {
		t.Fatalf("argv = %#v", argv)
	}
}
