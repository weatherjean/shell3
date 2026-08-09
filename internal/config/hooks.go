package config

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// hookSet maps each governed agent to its hook script paths, keyed by agent:
// "" is the main agent (hooks/tool-call.sh); a subagent name maps its
// hooks/<name>.tool-call.sh. Absent key = that agent runs ungated. There is
// no fallback or chaining between keys — each agent is governed by exactly
// one script per kind, or none.
type hookSet struct {
	call   map[string]string
	result map[string]string
}

// hookTimeout bounds one hook script run; a script still running after this
// fails closed.
const hookTimeout = 10 * time.Second

// hookOutputCap bounds captured hook stdout/stderr. A verdict is tiny JSON;
// anything past this is a runaway script (e.g. an accidental `cat` of a big
// file), and an unbounded buffer would balloon memory until the timeout.
const hookOutputCap = 1 << 20 // 1 MiB

// cappedBuffer keeps the first max bytes written and silently drops the rest;
// it never errors, so a chatty script isn't killed mid-write with EPIPE.
type cappedBuffer struct {
	buf bytes.Buffer
	max int
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if room := b.max - b.buf.Len(); room > 0 {
		if len(p) < room {
			room = len(p)
		}
		b.buf.Write(p[:room])
	}
	return len(p), nil
}

func (b *cappedBuffer) Bytes() []byte  { return b.buf.Bytes() }
func (b *cappedBuffer) String() string { return b.buf.String() }

// discoverHooks scans <dir>/hooks for the fixed filenames: tool-call.sh /
// tool-result.sh (main agent) and <name>.tool-call.sh / <name>.tool-result.sh
// (subagent <name>). Any other *.sh — including a <name> matching no
// subagent — produces a warning (`shell3 health` fails on it). A missing
// hooks/ dir means no hooks.
func discoverHooks(dir string, subagents []Subagent, hasNotifier bool, warn func(string)) (hookSet, error) {
	hs := hookSet{call: map[string]string{}, result: map[string]string{}}
	hooksDir := filepath.Join(dir, "hooks")
	entries, err := os.ReadDir(hooksDir)
	if err != nil {
		if os.IsNotExist(err) {
			return hs, nil
		}
		return hs, err
	}
	known := map[string]bool{}
	for _, sa := range subagents {
		known[sa.Name] = true
	}
	// The notifier persona is hookable like any subagent
	// (hooks/notifier.tool-call.sh) once notifier.md exists.
	if hasNotifier {
		known["notifier"] = true
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".sh") {
			continue
		}
		path := filepath.Join(hooksDir, name)
		switch name {
		case "tool-call.sh":
			hs.call[""] = path
			continue
		case "tool-result.sh":
			hs.result[""] = path
			continue
		}
		if agent, ok := strings.CutSuffix(name, ".tool-call.sh"); ok {
			if !known[agent] {
				warn(fmt.Sprintf("hook file %q names no subagent %q (agents/%s.md missing?)", path, agent, agent))
				continue
			}
			hs.call[agent] = path
			continue
		}
		if agent, ok := strings.CutSuffix(name, ".tool-result.sh"); ok {
			if !known[agent] {
				warn(fmt.Sprintf("hook file %q names no subagent %q (agents/%s.md missing?)", path, agent, agent))
				continue
			}
			hs.result[agent] = path
			continue
		}
		warn(fmt.Sprintf("hook file %q ignored: expected tool-call.sh, tool-result.sh, <subagent>.tool-call.sh, or <subagent>.tool-result.sh", path))
	}
	return hs, nil
}

// hookKey maps an agent name to its hookSet key: the main agent (or the
// zero-value session default) is "", any other name is a subagent's.
func (c *LoadedConfig) hookKey(agentName string) string {
	if agentName == "" || agentName == c.agent.Name {
		return ""
	}
	return agentName
}

// HasToolCall reports whether any tool-call hook exists (used to decide
// whether to install the gate closure at all).
func (c *LoadedConfig) HasToolCall() bool { return len(c.hooks.call) > 0 }

// HasToolResult reports whether any tool-result hook exists.
func (c *LoadedConfig) HasToolResult() bool { return len(c.hooks.result) > 0 }

// ToolCallHookFor returns the tool-call hook script path governing agentName
// ("" if none). Exposed for `shell3 health` to dry-run each hook.
func (c *LoadedConfig) ToolCallHookFor(agentName string) string {
	return c.hooks.call[c.hookKey(agentName)]
}

type ToolCallAction int

const (
	ActionRun ToolCallAction = iota
	ActionBlock
)

// ToolCallVerdict is the result of running an agent's tool-call hook.
type ToolCallVerdict struct {
	Action ToolCallAction
	Argv   []string // ActionRun: exec exactly this
	Reason string   // ActionBlock reason
	// Passthrough is true only on ActionRun when the hook expressed no
	// command/argv opinion — no hook for this agent, or an empty/{} verdict.
	// It lets the non-bash gate distinguish "hook didn't touch this" (allow)
	// from an actual command/argv verdict (which applies only to bash tools
	// and must fail closed).
	Passthrough bool
}

// hookVerdict is the JSON a tool-call hook prints to stdout. Precedence when
// several keys are set: block > argv > command (the safe outcome wins).
// Ask is parsed only to fail closed: the ask verdict was removed with the
// mail-model redesign (shell3 runs unattended; an ask is a denial with a
// delay), and a legacy hook still printing one must block loudly rather than
// silently degrade to an allow.
type hookVerdict struct {
	Block   bool     `json:"block"`
	Reason  string   `json:"reason"`
	Argv    []string `json:"argv"`
	Ask     string   `json:"ask"`
	Command *string  `json:"command"`
}

// runHook executes one hook script as `bash <path>` with payload on stdin and
// returns its stdout. cwd is the config dir, so a hook reads sibling files
// (.env, lib/) with relative paths. Any failure — start error, nonzero exit,
// timeout — returns an error (callers fail closed).
func runHook(ctx context.Context, cfgDir, path string, payload any) ([]byte, error) {
	in, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	hctx, cancel := context.WithTimeout(ctx, hookTimeout)
	defer cancel()
	cmd := exec.CommandContext(hctx, "bash", path)
	cmd.Dir = cfgDir
	// A killed hook may leave children holding the stdout pipe (e.g. a
	// backgrounded sleep); don't let Wait block on them past the kill.
	cmd.WaitDelay = time.Second
	cmd.Stdin = bytes.NewReader(in)
	stdout := &cappedBuffer{max: hookOutputCap}
	stderr := &cappedBuffer{max: hookOutputCap}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err := cmd.Run(); err != nil {
		if hctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("timed out after %s", hookTimeout)
		}
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return nil, fmt.Errorf("%w: %s", err, msg)
		}
		return nil, err
	}
	return stdout.Bytes(), nil
}

// toolCallPayload is the JSON a tool-call hook receives on stdin.
type toolCallPayload struct {
	Name     string  `json:"name"`
	Command  *string `json:"command"` // bash text for bash/bash_bg; null otherwise
	Args     string  `json:"args"`    // raw tool-args JSON
	Headless bool    `json:"headless"`
}

// RunToolCall runs the tool-call hook governing agentName for one tool
// invocation and returns the verdict. No hook for that agent → a passthrough
// run. FAILS CLOSED — a script error, malformed verdict JSON, or timeout
// blocks rather than runs. headless reports that no human is attached
// (subagents, cron); exposed to the script as .headless.
func (c *LoadedConfig) RunToolCall(ctx context.Context, agentName, name, command, argsJSON string, headless bool) ToolCallVerdict {
	passArgv := []string{"bash", "-c", command}
	path := c.hooks.call[c.hookKey(agentName)]
	if path == "" {
		return ToolCallVerdict{Action: ActionRun, Argv: passArgv, Passthrough: true}
	}
	payload := toolCallPayload{Name: name, Args: argsJSON, Headless: headless}
	if command != "" {
		payload.Command = &command
	}
	out, err := runHook(ctx, c.dir, path, payload)
	if err != nil {
		return ToolCallVerdict{Action: ActionBlock, Reason: "tool-call hook error: " + err.Error()}
	}
	trimmed := bytes.TrimSpace(out)
	if len(trimmed) == 0 {
		return ToolCallVerdict{Action: ActionRun, Argv: passArgv, Passthrough: true}
	}
	var v hookVerdict
	if err := json.Unmarshal(trimmed, &v); err != nil {
		return ToolCallVerdict{Action: ActionBlock, Reason: fmt.Sprintf("tool-call hook error: invalid verdict JSON: %v", err)}
	}
	switch {
	case v.Block:
		return ToolCallVerdict{Action: ActionBlock, Reason: v.Reason}
	case v.Argv != nil:
		// A present but malformed argv fails closed — never falls through to
		// run the original command unwrapped (the documented safety promise).
		if len(v.Argv) == 0 {
			return ToolCallVerdict{Action: ActionBlock, Reason: "tool-call hook error: argv is empty"}
		}
		if slices.Contains(v.Argv, "") {
			return ToolCallVerdict{Action: ActionBlock, Reason: "tool-call hook error: argv contains an empty element"}
		}
		return ToolCallVerdict{Action: ActionRun, Argv: v.Argv}
	case v.Ask != "":
		return ToolCallVerdict{Action: ActionBlock, Reason: "tool-call hook error: the ask verdict " +
			"no longer exists (hooks allow, block, or rewrite); update the hook to use block"}
	case v.Command != nil:
		return ToolCallVerdict{Action: ActionRun, Argv: []string{"bash", "-c", *v.Command}}
	}
	// {} — an explicit pass.
	return ToolCallVerdict{Action: ActionRun, Argv: passArgv, Passthrough: true}
}

// toolResultPayload is the JSON a tool-result hook receives on stdin.
type toolResultPayload struct {
	Name   string `json:"name"`
	Args   string `json:"args"`
	Output string `json:"output"`
}

// RunToolResult runs the tool-result hook governing agentName over one tool's
// output and returns the (possibly rewritten) output. No hook → output
// unchanged; {} or empty stdout → unchanged; {"output": ...} → rewritten.
// FAILS CLOSED — on any script failure the output is replaced by an error
// notice, never passed through unredacted.
func (c *LoadedConfig) RunToolResult(ctx context.Context, agentName, name, argsJSON, output string) string {
	path := c.hooks.result[c.hookKey(agentName)]
	if path == "" {
		return output
	}
	out, err := runHook(ctx, c.dir, path, toolResultPayload{Name: name, Args: argsJSON, Output: output})
	if err != nil {
		return "[tool-result hook failed: " + err.Error() + "]"
	}
	trimmed := bytes.TrimSpace(out)
	if len(trimmed) == 0 {
		return output
	}
	var v struct {
		Output *string `json:"output"`
	}
	if err := json.Unmarshal(trimmed, &v); err != nil {
		return "[tool-result hook failed: invalid verdict JSON: " + err.Error() + "]"
	}
	if v.Output == nil {
		return output
	}
	return *v.Output
}
