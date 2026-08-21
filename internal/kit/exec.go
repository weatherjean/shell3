package kit

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// paramName is the identifier shape a param must have: it becomes an
// environment variable, so it cannot carry hyphens or start with a digit.
var paramName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// bindArgs validates a tool call's arguments against the declared params and
// returns the environment the shell function runs with. Required params must be
// present; declared defaults fill the rest; undeclared arguments are an error
// (a tool's surface is exactly what it declares).
func (t Tool) bindArgs(args map[string]any) ([]string, error) {
	for name := range args {
		if _, declared := t.Params[name]; !declared {
			return nil, fmt.Errorf("tool %q: unknown argument %q", t.Name, name)
		}
	}

	names := make([]string, 0, len(t.Params))
	for name := range t.Params {
		names = append(names, name)
	}
	sort.Strings(names) // deterministic env order

	env := make([]string, 0, len(names))
	for _, name := range names {
		p := t.Params[name]
		v, given := args[name]
		if !given {
			if p.Required {
				return nil, fmt.Errorf("tool %q: missing required argument %q", t.Name, name)
			}
			if p.Default == nil {
				continue // unset param: the body sees an empty variable
			}
			v = p.Default
		}
		s, err := coerce(p.Type, v)
		if err != nil {
			return nil, fmt.Errorf("tool %q: argument %q: %w", t.Name, name, err)
		}
		env = append(env, name+"="+s)
	}
	return env, nil
}

// coerce renders a JSON-decoded value as the string its declared type implies.
// JSON numbers arrive as float64, so an int param must not pick up a ".000000"
// tail or silently truncate a fractional value.
func coerce(typ string, v any) (string, error) {
	switch typ {
	case "int":
		switch n := v.(type) {
		case float64:
			if n != math.Trunc(n) {
				return "", fmt.Errorf("want an integer, got %v", n)
			}
			return strconv.FormatInt(int64(n), 10), nil
		case int:
			return strconv.Itoa(n), nil
		case string:
			if _, err := strconv.Atoi(n); err != nil {
				return "", fmt.Errorf("want an integer, got %q", n)
			}
			return n, nil
		default:
			return "", fmt.Errorf("want an integer, got %T", v)
		}
	case "bool":
		switch b := v.(type) {
		case bool:
			return strconv.FormatBool(b), nil
		case string:
			if b != "true" && b != "false" {
				return "", fmt.Errorf("want true or false, got %q", b)
			}
			return b, nil
		default:
			return "", fmt.Errorf("want a boolean, got %T", v)
		}
	default:
		if s, ok := v.(string); ok {
			return s, nil
		}
		return fmt.Sprintf("%v", v), nil
	}
}

// Runner executes a kit's tool functions.
type Runner struct {
	// Path is the kit file sourced before the function is called.
	Path string
	// Bash is the interpreter; "" means "bash" from PATH.
	Bash string
	// Env is the base environment. nil means the minimal set (see baseEnv): a
	// tool inherits no secrets, and reads the one key it needs at point of use.
	Env []string
	// Dir is the working directory; "" means the caller's.
	Dir string
}

// passthroughEnv is what a tool inherits when Runner.Env is unset: enough to
// find programs and write scratch files, and nothing else. Inheriting the full
// environment would hand every tool every secret in the process.
var passthroughEnv = []string{"PATH", "HOME", "TMPDIR", "LANG", "TZ"}

// baseEnv builds the inherited environment for a run.
func (r Runner) baseEnv(lookup func(string) (string, bool)) []string {
	if r.Env != nil {
		return append([]string{}, r.Env...)
	}
	out := make([]string, 0, len(passthroughEnv))
	for _, k := range passthroughEnv {
		if v, ok := lookup(k); ok {
			out = append(out, k+"="+v)
		}
	}
	return out
}

// Run sources the kit and calls one tool function with its bound arguments in
// the environment. Sourcing is safe because a kit is definitions-only — Parse
// rejects any top-level statement, so nothing executes on source.
//
// Stdout is the tool's result. Stderr is folded into the error when the call
// fails, so a failing tool reports why rather than returning silence.
func (r Runner) Run(ctx context.Context, t Tool, args map[string]any) (string, error) {
	env, err := t.bindArgs(args)
	if err != nil {
		return "", err
	}
	sh := r.Bash
	if sh == "" {
		sh = "bash"
	}

	script := fmt.Sprintf("set -uo pipefail; source %s; %s", shellQuote(r.Path), t.Func)
	cmd := exec.CommandContext(ctx, sh, "-c", script)
	cmd.Dir = r.Dir
	cmd.Env = append(r.baseEnv(os.LookupEnv), env...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return stdout.String(), fmt.Errorf("tool %q failed: %s", t.Name, msg)
	}
	return stdout.String(), nil
}

// shellQuote single-quotes a path for safe interpolation into the source line.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
