package chat

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrHostToolNotFound is returned by a HostTool dispatcher when it does not
// recognize the called tool name, signaling dispatchCustomTool to fall through
// to the command-template (ResolveCustomTool) path. Any OTHER error from a
// HostTool is a real failure and is surfaced as-is.
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

// dispatchCustomTool resolves a custom-tool call to its bash command + env and
// runs it. Foreground tools block and return the command's output (a non-zero
// exit is surfaced as an error result, "exited N"). Background tools dispatch
// onto the in-process job runtime (StartBashBg, same as bash_bg) and return the
// job id; completion is delivered as a notice on a later turn. The
// resolved command is the trusted author template, so its text BYPASSES on_tool_call
// rewriting/denylisting (the tool call itself still fires the chain by name) — the
// model supplies only env values, never the command string.
func dispatchCustomTool(ctx context.Context, cfg TurnConfig, name, rawArgs string) toolResult {
	// Host-registered Go tools (pkg/shell3.RegisterHostTool) return a result
	// string directly, so they dispatch here without the resolve-and-exec path.
	if cfg.HostTool != nil {
		out, err := cfg.HostTool(ctx, name, rawArgs)
		switch {
		case err == nil:
			return classifyHandlerOutput(out)
		case !errors.Is(err, ErrHostToolNotFound):
			// A real host-tool failure — surface it.
			return errResult("error: " + err.Error())
		}
		// errors.Is(err, ErrHostToolNotFound): this dispatcher doesn't own the
		// name — fall through to the command-template path below.
	}
	if cfg.ResolveCustomTool == nil {
		return errResult(fmt.Sprintf("error: unknown tool %q", name))
	}
	rt, err := cfg.ResolveCustomTool(name, rawArgs)
	if err != nil {
		return errResult("error: " + err.Error())
	}
	if rt.Background {
		if cfg.StartBashBg == nil {
			return errResult("error: background tools are not available")
		}
		jobID, err := cfg.StartBashBg(rt.Command, cfg.WorkDir, []string{"bash", "-c", rt.Command}, rt.Env)
		if err != nil {
			return errResult("error: " + err.Error())
		}
		return okResult(fmt.Sprintf("started background tool %s\nYou'll get a completion notice on your next turn. Do not poll.", jobID))
	}
	timeout := time.Duration(DefaultBashTimeoutSeconds) * time.Second
	if rt.Timeout > 0 {
		t := rt.Timeout
		if t > MaxBashTimeoutSeconds {
			t = MaxBashTimeoutSeconds
		}
		timeout = time.Duration(t) * time.Second
	}
	out, code := runBashCapture(ctx, []string{"bash", "-c", rt.Command}, cfg.WorkDir, rt.Env, timeout)
	if code != 0 {
		return errResult(fmt.Sprintf("error: command exited %d\n%s", code, out))
	}
	return okResult(out)
}
