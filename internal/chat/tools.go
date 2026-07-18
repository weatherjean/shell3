package chat

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ErrHostToolNotFound is returned by a HostTool dispatcher when it does not
// recognize the called tool name; dispatchHostTool turns it into the
// bash-first unknown-tool error. Any OTHER error from a HostTool is a real
// failure and is surfaced as-is.
var ErrHostToolNotFound = errors.New("host tool: name not handled")

// toolResult is the typed outcome of one tool call: the text recorded as the
// tool message plus whether it represents a failure. Every dispatch path in
// executeToolCalls produces one, so error-ness is carried as data instead of
// being re-derived by sniffing prefixes off the output string.
type toolResult struct {
	output  string
	isError bool
}

func okResult(out string) toolResult  { return toolResult{output: out} }
func errResult(out string) toolResult { return toolResult{output: out, isError: true} }

// unknownToolMsg is the error for a tool call whose name nothing owns. Models
// trained on other harnesses reflexively call read_file/grep/write_file; the
// nudge steers them back to bash/edit_file instead of leaving them guessing.
func unknownToolMsg(name string) string {
	return fmt.Sprintf("error: unknown tool %q — this agent is bash-first: read/list/search with bash (cat, sed -n, ls, rg) and create or modify files with edit_file", name)
}

// classifyHandlerOutput types a built-in handler's output string. Handlers
// report in-band failures to the model as "error: …" strings (so the text and
// the flag can never disagree); this is the single place that convention is
// interpreted. Hook, validation, and dispatcher failures never pass
// through here — they construct typed errResults directly.
func classifyHandlerOutput(out string) toolResult {
	if strings.HasPrefix(out, "error:") {
		return errResult(out)
	}
	return okResult(out)
}

// dispatchHostTool runs a host-registered Go tool (internal/shell3.RegisterHostTool)
// by name and returns its result string. Unknown names — no dispatcher, or a
// dispatcher that answers ErrHostToolNotFound — get the bash-first redirect
// error.
func dispatchHostTool(ctx context.Context, cfg TurnConfig, name, rawArgs string) toolResult {
	if cfg.HostTool == nil {
		return errResult(unknownToolMsg(name))
	}
	out, err := cfg.HostTool(ctx, name, rawArgs)
	switch {
	case err == nil:
		return classifyHandlerOutput(out)
	case errors.Is(err, ErrHostToolNotFound):
		return errResult(unknownToolMsg(name))
	default:
		return errResult("error: " + err.Error())
	}
}
