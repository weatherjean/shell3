package kit

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// TestResult is the outcome of running one agent's declared tests.
type TestResult struct {
	Agent  string
	Ran    int
	Output string
	Failed bool
}

// RunTests executes an agent's `test:` functions under the harness. Each test
// runs in a subshell with $KIT_STUBS at the front of PATH, so a stubbed command
// never escapes into the next test.
func (k *Kit) RunTests(ctx context.Context, path string, a Agent, only string) (TestResult, error) {
	script, n := k.TestScript(a, only)
	if n == 0 {
		return TestResult{Agent: a.Name}, nil
	}

	// The script runs with the kit's directory as cwd, so the source line must
	// be absolute — a relative path would resolve against the wrong base.
	abs, err := filepath.Abs(path)
	if err != nil {
		return TestResult{}, fmt.Errorf("resolve kit path: %w", err)
	}

	tmp, err := os.MkdirTemp("", "shell3-kit-test-")
	if err != nil {
		return TestResult{}, fmt.Errorf("test scratch dir: %w", err)
	}
	defer os.RemoveAll(tmp)

	stubs := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(stubs, 0o700); err != nil {
		return TestResult{}, fmt.Errorf("test stub dir: %w", err)
	}

	full := fmt.Sprintf("source %s\n%s", shellQuote(abs), script)
	cmd := exec.CommandContext(ctx, "bash", "-c", full)
	cmd.Dir = filepath.Dir(abs)
	cmd.Env = append(os.Environ(),
		"KIT_TMP="+tmp,
		"KIT_STUBS="+stubs,
		"PATH="+stubs+string(os.PathListSeparator)+os.Getenv("PATH"),
	)

	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err = cmd.Run()

	res := TestResult{Agent: a.Name, Ran: n, Output: out.String()}
	if err != nil {
		res.Failed = true
	}
	return res, nil
}
