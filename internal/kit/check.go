package kit

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// CheckResult is one kit's validation outcome. Problems are human-readable
// lines, each naming the kit file's own line number where one is known.
type CheckResult struct {
	Path       string
	Problems   []string
	Shellcheck bool // whether shellcheck was available and ran
}

// OK reports whether the kit passed.
func (c CheckResult) OK() bool { return len(c.Problems) == 0 }

// Check validates a kit file three ways: it is valid bash (`bash -n`), it lints
// clean if shellcheck is installed, and its declarations parse. This is the
// whole point of the .sh container — one command covers the entire file, with
// no extraction and no line remapping.
func Check(ctx context.Context, path string) (CheckResult, error) {
	res := CheckResult{Path: path}

	src, err := os.ReadFile(path)
	if err != nil {
		return res, fmt.Errorf("read kit: %w", err)
	}

	if out, err := exec.CommandContext(ctx, "bash", "-n", path).CombinedOutput(); err != nil {
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if line != "" {
				res.Problems = append(res.Problems, "syntax: "+line)
			}
		}
		if len(res.Problems) == 0 {
			res.Problems = append(res.Problems, "syntax: "+err.Error())
		}
		// A file that is not valid bash cannot be meaningfully parsed further.
		return res, nil
	}

	if _, err := exec.LookPath("shellcheck"); err == nil {
		res.Shellcheck = true
		// SC1090/SC1091: sourcing is the caller's job, not the kit's.
		out, err := exec.CommandContext(ctx, "shellcheck",
			"--severity=warning", "--exclude=SC1090,SC1091", "--format=gcc", path).CombinedOutput()
		if err != nil {
			for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				if line != "" {
					res.Problems = append(res.Problems, "lint: "+line)
				}
			}
		}
	}

	if _, err := Parse(src); err != nil {
		res.Problems = append(res.Problems, "declaration: "+err.Error())
	}
	return res, nil
}
