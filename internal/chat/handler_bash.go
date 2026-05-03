package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
)

// BashHandler executes a bash command and returns its combined stdout+stderr.
// It respects context cancellation — callers set timeouts before invoking Execute.
// Exit codes are not returned as errors; non-zero exit appends the error to output.
type BashHandler struct{}

func (BashHandler) Name() string { return "bash" }

func (BashHandler) Execute(ctx context.Context, id string, args json.RawMessage, cfg ToolConfig) (string, error) {
	command := parseBashArgs(string(args))
	c := exec.CommandContext(ctx, "bash", "-c", command)
	c.Dir = cfg.WorkDir
	var buf bytes.Buffer
	c.Stdout = &buf
	c.Stderr = &buf
	if err := c.Run(); err != nil {
		if buf.Len() == 0 {
			fmt.Fprintf(&buf, "error: %v\n", err)
		}
	}
	if buf.Len() == 0 {
		return "(no output)", nil
	}
	return buf.String(), nil
}

// parseBashArgs extracts the "command" field from bash tool JSON args.
// Takes string (not json.RawMessage) so it can be called from turn.go
// where tc.RawArgs is a string without a type conversion.
func parseBashArgs(raw string) string {
	var args struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return raw
	}
	return args.Command
}
