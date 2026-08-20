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
	call   map[string]hookRef
	result map[string]hookRef
}

// hookRef is where one agent's hook lives: a standalone script (the markdown
// config's hooks/*.sh) or a function declared in a kit (`gate:` / `note:`).
// Exactly one of the two forms is set.
type hookRef struct {
	path string // hooks/<...>.sh
	kit  string // kit file to source
	fn   string // function to call after sourcing
}

func (h hookRef) empty() bool { return h.path == "" && h.fn == "" }

// shellQuote single-quotes a path for safe interpolation into the source line.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// label names the hook for an error message: a path, or kit:function.
func (h hookRef) label() string {
	if h.path != "" {
		return h.path
	}
	return filepath.Base(h.kit) + ":" + h.fn
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
func discoverHooks(dir string, subagents []Subagent, warn func(string)) (hookSet, error) {
	hs := hookSet{call: map[string]hookRef{}, result: map[string]hookRef{}}
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
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".sh") {
			continue
		}
		path := filepath.Join(hooksDir, name)
		switch name {
		case "tool-call.sh":
			hs.call[""] = hookRef{path: path}
			continue
		case "tool-result.sh":
			hs.result[""] = hookRef{path: path}
			continue
		}
		if agent, ok := strings.CutSuffix(name, ".tool-call.sh"); ok {
			if !known[agent] {
				warn(fmt.Sprintf("hook file %q names no subagent %q (agents/%s.md missing?)", path, agent, agent))
				continue
			}
			hs.call[agent] = hookRef{path: path}
			continue
		}
		if agent, ok := strings.CutSuffix(name, ".tool-result.sh"); ok {
			if !known[agent] {
				warn(fmt.Sprintf("hook file %q names no subagent %q (agents/%s.md missing?)", path, agent, agent))
				continue
			}
			hs.result[agent] = hookRef{path: path}
			continue
		}
		warn(fmt.Sprintf("hook file %q ignored: expected tool-call.sh, tool-result.sh, <subagent>.tool-call.sh, or <subagent>.tool-result.sh", path))
	}
	return hs, nil
}

// SetKitHooks installs the gates and notes a kit declares, keyed by agent
// name. A kit config keeps its gate in the kit; the hooks/*.sh files remain
// the form a markdown config uses.
//
// Declaring both for one agent is an ERROR rather than a precedence rule.
// Silently picking a winner would hide a half-finished migration — exactly
// when someone believes a gate is running and it is not.
// mainName is the kit's first agent — the one this config keys as "" — since
// a kit's main agent need not share a name with the markdown config's.
func (c *LoadedConfig) SetKitHooks(kitPath, mainName string, gates, notes map[string]string) error {
	c.kitMainAgent = mainName
	for _, m := range []struct {
		kind  string
		from  map[string]string
		into  map[string]hookRef
		files string
	}{
		{"gate", gates, c.hooks.call, "tool-call.sh"},
		{"note", notes, c.hooks.result, "tool-result.sh"},
	} {
		for agent, fn := range m.from {
			key := c.hookKey(agent)
			if prev := m.into[key]; !prev.empty() {
				return fmt.Errorf("agent %q has both a kit %s (%s) and a hook file (%s) — delete one; a config with two cannot say which governs",
					agent, m.kind, fn, prev.label())
			}
			m.into[key] = hookRef{kit: kitPath, fn: fn}
		}
	}
	return nil
}

// hookKey maps an agent name to its hookSet key: the main agent (or the
// zero-value session default) is "", any other name is a subagent's.
func (c *LoadedConfig) hookKey(agentName string) string {
	// kitMainAgent is set when a kit installed its gates: a kit's main agent
	// need not share a name with the markdown config's, and both must key to
	// "" or the gate is stored under one name and looked up under another.
	if agentName == "" || agentName == c.agent.Name || (c.kitMainAgent != "" && agentName == c.kitMainAgent) {
		return ""
	}
	return agentName
}

// HasToolCall reports whether any tool-call hook exists (used to decide
// whether to install the gate closure at all).
func (c *LoadedConfig) HasToolCall() bool { return len(c.hooks.call) > 0 }

// HasToolResult reports whether any tool-result hook exists.
func (c *LoadedConfig) HasToolResult() bool { return len(c.hooks.result) > 0 }

// ToolCallHookFor names the tool-call hook governing agentName ("" if none):
// a script path, or kit:function for a kit-declared gate. Exposed for
// `shell3 health` to report and dry-run each hook.
func (c *LoadedConfig) ToolCallHookFor(agentName string) string {
	ref := c.hooks.call[c.hookKey(agentName)]
	if ref.empty() {
		return ""
	}
	return ref.label()
}

type ToolCallAction int

const (
	ActionRun ToolCallAction = iota
	ActionBlock
	// ActionReview is a soft deny: the hook is unsure and defers to the LLM
	// reviewer (chat layer resolves it to run or block; no reviewer wired =
	// block). Reason carries the hook's flag description into the review.
	ActionReview
)

// ToolCallVerdict is the result of running an agent's tool-call hook.
type ToolCallVerdict struct {
	Action ToolCallAction
	Argv   []string // ActionRun: exec exactly this
	Reason string   // ActionBlock / ActionReview reason
	// Passthrough is true only on ActionRun when the hook expressed no
	// command/argv opinion — no hook for this agent, or an empty/{} verdict.
	// It lets the non-bash gate distinguish "hook didn't touch this" (allow)
	// from an actual command/argv verdict (which applies only to bash tools
	// and must fail closed).
	Passthrough bool
}

// hookVerdict is the JSON a tool-call hook prints to stdout. Precedence when
// several keys are set: block > review > argv > command (the safe outcome
// wins — an unsure hook that also printed a rewrite gets the review, and one
// that also blocked gets the block).
// Ask is parsed only to fail closed: the ask verdict was removed with the
// mail-model redesign (shell3 runs unattended; an ask is a denial with a
// delay), and a legacy hook still printing one must block loudly rather than
// silently degrade to an allow.
type hookVerdict struct {
	Block   bool     `json:"block"`
	Review  bool     `json:"review"`
	Reason  string   `json:"reason"`
	Argv    []string `json:"argv"`
	Ask     string   `json:"ask"`
	Command *string  `json:"command"`
}

// runHook executes one hook script as `bash <path>` with payload on stdin and
// returns its stdout. cwd is the config dir, so a hook reads sibling files
// (.env, lib/) with relative paths. Any failure — start error, nonzero exit,
// timeout — returns an error (callers fail closed).
func runHook(ctx context.Context, cfgDir string, ref hookRef, payload any) ([]byte, error) {
	in, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	hctx, cancel := context.WithTimeout(ctx, hookTimeout)
	defer cancel()
	var cmd *exec.Cmd
	if ref.path != "" {
		cmd = exec.CommandContext(hctx, "bash", ref.path)
	} else {
		// A kit-declared gate: source the kit, then call the one function.
		// Sourcing is safe precisely because the kit parser rejects top-level
		// statements — a kit defines functions and does nothing else, so this
		// costs a parse and runs no side effects.
		script := fmt.Sprintf("set -uo pipefail; source %s; %s", shellQuote(ref.kit), ref.fn)
		cmd = exec.CommandContext(hctx, "bash", "-c", script)
	}
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
	ref := c.hooks.call[c.hookKey(agentName)]
	if ref.empty() {
		return ToolCallVerdict{Action: ActionRun, Argv: passArgv, Passthrough: true}
	}
	payload := toolCallPayload{Name: name, Args: argsJSON, Headless: headless}
	if command != "" {
		payload.Command = &command
	}
	out, err := runHook(ctx, c.dir, ref, payload)
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
	case v.Review:
		return ToolCallVerdict{Action: ActionReview, Reason: v.Reason}
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
	ref := c.hooks.result[c.hookKey(agentName)]
	if ref.empty() {
		return output
	}
	out, err := runHook(ctx, c.dir, ref, toolResultPayload{Name: name, Args: argsJSON, Output: output})
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
