package kit

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mustParse(t *testing.T, src string) *Kit {
	t.Helper()
	k, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return k
}

func writeKit(t *testing.T, src string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "kit.sh")
	if err := os.WriteFile(p, []byte(src), 0o600); err != nil {
		t.Fatalf("write kit: %v", err)
	}
	return p
}

const execKit = `#---
# agent: a
#---
a_prompt() { cat <<'EOF'
hi
EOF
}

#---
# tool: greet
# description: greet someone
# params:
#   who:   {type: string, required: true}
#   times: {type: int, default: 2}
#   loud:  {type: bool, default: false}
#---
a_greet() {
  local i
  for ((i = 0; i < times; i++)); do
    if [ "$loud" = true ]; then echo "HELLO $who"; else echo "hello $who"; fi
  done
}

#---
# tool: boom
# description: always fails
#---
a_boom() { echo "on stderr" >&2; return 3; }
`

func TestBindArgsDefaultsAndRequired(t *testing.T) {
	k := mustParse(t, execKit)
	tool := k.Agents[0].Tools[0]

	env, err := tool.BindArgs(map[string]any{"who": "world"})
	if err != nil {
		t.Fatalf("BindArgs: %v", err)
	}
	got := strings.Join(env, " ")
	if !strings.Contains(got, "who=world") || !strings.Contains(got, "times=2") ||
		!strings.Contains(got, "loud=false") {
		t.Fatalf("env = %v", env)
	}

	if _, err := tool.BindArgs(map[string]any{}); err == nil {
		t.Fatal("want error for a missing required argument")
	}
	if _, err := tool.BindArgs(map[string]any{"who": "x", "nope": 1}); err == nil {
		t.Fatal("want error for an undeclared argument")
	}
}

// JSON numbers arrive as float64; an int param must not gain a decimal tail nor
// silently swallow a fractional value.
func TestBindArgsIntCoercion(t *testing.T) {
	k := mustParse(t, execKit)
	tool := k.Agents[0].Tools[0]

	env, err := tool.BindArgs(map[string]any{"who": "x", "times": float64(3)})
	if err != nil {
		t.Fatalf("BindArgs: %v", err)
	}
	if !strings.Contains(strings.Join(env, " "), "times=3") {
		t.Fatalf("env = %v, want times=3", env)
	}

	if _, err := tool.BindArgs(map[string]any{"who": "x", "times": 2.5}); err == nil {
		t.Fatal("want error for a fractional value on an int param")
	}
}

func TestRunnerRun(t *testing.T) {
	k := mustParse(t, execKit)
	path := writeKit(t, execKit)
	r := Runner{Path: path}

	out, err := r.Run(context.Background(), k.Agents[0].Tools[0],
		map[string]any{"who": "world"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out != "hello world\nhello world\n" {
		t.Fatalf("out = %q", out)
	}
}

func TestRunnerRunBoolAndOverride(t *testing.T) {
	k := mustParse(t, execKit)
	r := Runner{Path: writeKit(t, execKit)}

	out, err := r.Run(context.Background(), k.Agents[0].Tools[0],
		map[string]any{"who": "you", "times": float64(1), "loud": true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out != "HELLO you\n" {
		t.Fatalf("out = %q", out)
	}
}

// A failing tool must report why, not return silence.
func TestRunnerRunSurfacesStderr(t *testing.T) {
	k := mustParse(t, execKit)
	r := Runner{Path: writeKit(t, execKit)}

	_, err := r.Run(context.Background(), k.Agents[0].Tools[1], nil)
	if err == nil {
		t.Fatal("want error from a failing tool")
	}
	if !strings.Contains(err.Error(), "on stderr") {
		t.Fatalf("err = %q, want it to carry stderr", err)
	}
}

// Sourcing a kit must not execute anything: the file is definitions-only.
func TestRunnerSourcingHasNoSideEffects(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "touched")
	src := `#---
# agent: a
#---
a_prompt() { echo hi; }

#---
# tool: t
# description: writes the marker only when called
#---
a_t() { touch "` + marker + `"; echo done; }
`
	k := mustParse(t, src)
	r := Runner{Path: writeKit(t, src)}

	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("marker exists before the run")
	}
	if _, err := r.Run(context.Background(), k.Agents[0].Tools[0], nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("tool did not run: %v", err)
	}
}
