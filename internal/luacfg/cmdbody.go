package luacfg

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// runBodyCmd runs a command string with `bash -c` in the given working
// directory and returns its trimmed stdout. It backs the body_cmd/prompt_cmd
// config options: a skill body or agent/subagent prompt sourced from a shell
// command (typically `cat some-file.md`) instead of an inline Lua string.
//
// Resolution happens at load time only, so it deliberately uses
// context.Background() (there is no per-turn context here) and os/exec
// directly rather than the Lua-facing luaBash (which sets no cwd). The caller
// passes cwd = the config directory so relative paths resolve next to
// shell3.lua / .env / lib.
//
// It fails CLOSED: a non-zero exit returns an error (with captured stderr for
// diagnosis), and empty stdout (after trimming whitespace) is also an error —
// an empty prompt/body is never a valid resolution.
func runBodyCmd(workdir, command string) (string, error) {
	cmd := exec.CommandContext(context.Background(), "bash", "-c", command)
	cmd.Dir = workdir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return "", fmt.Errorf("%w: %s", err, msg)
		}
		return "", err
	}
	out := strings.TrimSpace(stdout.String())
	if out == "" {
		return "", fmt.Errorf("command produced no output")
	}
	return out, nil
}
