package config

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/weatherjean/shell3/internal/kit"
)

// hookKind discriminates the kit hook kinds. Each is a separate table, so an
// agent governed by one kind is unaffected by the others.
type hookKind int

const (
	// hookToolCall is `gate:` — runs before every tool call, can refuse.
	hookToolCall hookKind = iota
	// hookToolResult is `note:` — rewrites a tool's output.
	hookToolResult
	// hookCommand is `command:` — a host command answered by a shell
	// function, with no model turn.
	hookCommand
	// hookEvent is `event:` — a subscriber on the session event stream.
	hookEvent
)

func (k hookKind) String() string {
	switch k {
	case hookToolCall:
		return "tool-call"
	case hookToolResult:
		return "tool-result"
	case hookCommand:
		return "command"
	default:
		return "event"
	}
}

// hookSet maps each kind to its table of hooks.
//
// For the per-agent kinds (gate/note/event) the inner key is the governed
// agent: "" is the kit's main agent, any other key is an employee, and an
// absent key means that agent runs without that hook. There is no fallback or
// chaining between keys — each agent is governed by exactly one function per
// kind, or none.
//
// For hookCommand the inner key is the COMMAND NAME instead, because a host
// command belongs to the install rather than to an agent. Same shape, same
// runner; only the meaning of the key differs.
type hookSet map[hookKind]map[string]hookRef

// set records one hook, allocating the kind's table on first use.
func (h hookSet) set(kind hookKind, key string, ref hookRef) {
	if h[kind] == nil {
		h[kind] = map[string]hookRef{}
	}
	h[kind][key] = ref
}

// get returns the hook for one kind and key; the zero hookRef (empty()) when
// there is none.
func (h hookSet) get(kind hookKind, key string) hookRef { return h[kind][key] }

// hookRef is where one agent's hook lives: a function declared in the kit
// (`gate:` / `note:`), reached by sourcing the kit and calling it.
type hookRef struct {
	kit string // kit file to source
	fn  string // function to call after sourcing
}

func (h hookRef) empty() bool { return h.fn == "" }

// label names the hook for an error message: kit:function.
func (h hookRef) label() string {
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

// KitHooks is what a parsed kit contributes to this config: the per-agent
// tables keyed by agent name, plus the install-wide command table keyed by
// command name. Every field is optional.
type KitHooks struct {
	Gates    map[string]string
	Notes    map[string]string
	Events   map[string]EventSub
	Commands map[string]string
}

// EventSub is one agent's event subscriber: the function to run and the event
// kinds it receives. On is mandatory at parse time — see kit.EventNames.
type EventSub struct {
	Func string
	On   []string
}

// Empty reports whether the kit declared no hooks at all, so callers can skip
// SetKitHooks entirely.
func (k KitHooks) Empty() bool {
	return len(k.Gates) == 0 && len(k.Notes) == 0 && len(k.Events) == 0 && len(k.Commands) == 0
}

// SetKitHooks installs the hooks a kit declares. mainName is the kit's first
// agent — the one the per-agent tables key as "". Command names are NOT
// normalised through hookKey: they are command names, not agent names.
func (c *LoadedConfig) SetKitHooks(kitPath, mainName string, h KitHooks) {
	c.kitMainAgent = mainName
	if c.hooks == nil {
		c.hooks = hookSet{}
	}
	for _, m := range []struct {
		kind hookKind
		from map[string]string
	}{
		{hookToolCall, h.Gates},
		{hookToolResult, h.Notes},
	} {
		for agent, fn := range m.from {
			c.hooks.set(m.kind, c.hookKey(agent), hookRef{kit: kitPath, fn: fn})
		}
	}
	for agent, sub := range h.Events {
		key := c.hookKey(agent)
		c.hooks.set(hookEvent, key, hookRef{kit: kitPath, fn: sub.Func})
		if c.eventOn == nil {
			c.eventOn = map[string][]string{}
		}
		c.eventOn[key] = sub.On
	}
	for name, fn := range h.Commands {
		c.hooks.set(hookCommand, name, hookRef{kit: kitPath, fn: fn})
	}
}

// hookKey maps an agent name to its hookSet key: the kit's main agent (and
// the zero-value session default) is "", any employee is its own name. Both
// spellings of the main agent must key to "" or a gate is stored under one
// name and looked up under another.
func (c *LoadedConfig) hookKey(agentName string) string {
	if agentName == "" || (c.kitMainAgent != "" && agentName == c.kitMainAgent) {
		return ""
	}
	return agentName
}

// HasToolCall reports whether any tool-call hook exists (used to decide
// whether to install the gate closure at all).
func (c *LoadedConfig) HasToolCall() bool { return len(c.hooks[hookToolCall]) > 0 }

// HasToolResult reports whether any tool-result hook exists.
func (c *LoadedConfig) HasToolResult() bool { return len(c.hooks[hookToolResult]) > 0 }

// HasEvent reports whether any event subscriber exists.
func (c *LoadedConfig) HasEvent() bool { return len(c.hooks[hookEvent]) > 0 }

// ToolCallHookFor names the tool-call hook governing agentName ("" if none):
// a script path, or kit:function for a kit-declared gate. Exposed for
// `shell3 health` to report and dry-run each hook.
func (c *LoadedConfig) ToolCallHookFor(agentName string) string {
	ref := c.hooks.get(hookToolCall, c.hookKey(agentName))
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
func runHook(ctx context.Context, cfgDir string, ref hookRef, payload any, env ...string) ([]byte, error) {
	in, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	hctx, cancel := context.WithTimeout(ctx, hookTimeout)
	defer cancel()
	// Source the kit, then call the one declared function. Sourcing is safe
	// precisely because the kit parser rejects top-level statements — a kit
	// defines functions and does nothing else, so this costs a parse and runs
	// no side effects.
	script := fmt.Sprintf("set -uo pipefail; source %s; %s", kit.ShellQuote(ref.kit), ref.fn)
	cmd := exec.CommandContext(hctx, "bash", "-c", script)
	cmd.Dir = cfgDir
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
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
	ref := c.hooks.get(hookToolCall, c.hookKey(agentName))
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
	ref := c.hooks.get(hookToolResult, c.hookKey(agentName))
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

// ErrNoSuchCommand reports that this config declares no command by that name,
// so the caller's own unknown-command handling applies. Distinct from a
// command that ran and failed.
var ErrNoSuchCommand = errors.New("no such kit command")

// HasCommand reports whether the kit declares a command by this name. A
// front-end asks before routing a verb none of its built-ins claim.
func (c *LoadedConfig) HasCommand(name string) bool {
	return !c.hooks.get(hookCommand, name).empty()
}

// CommandNames lists the declared command names in sorted order, for a
// front-end registering them in its command menu.
func (c *LoadedConfig) CommandNames() []string {
	return slices.Sorted(maps.Keys(c.hooks[hookCommand]))
}

// RunCommand runs a kit-declared host command and returns its stdout, trimmed.
// Everything the user typed after the verb reaches the function as $ARG.
//
// Unlike gate:/note: there is no fail-closed question here: a command grants
// nothing and blocks nothing, so a failure is simply reported to whoever asked
// for it. A nonzero exit returns the error with the script's stderr attached.
func (c *LoadedConfig) RunCommand(ctx context.Context, name, arg string) (string, error) {
	ref := c.hooks.get(hookCommand, name)
	if ref.empty() {
		return "", ErrNoSuchCommand
	}
	payload := struct {
		Command string `json:"command"`
		Arg     string `json:"arg"`
	}{Command: name, Arg: arg}
	out, err := runHook(ctx, c.dir, ref, payload, "ARG="+arg)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// SubscribesTo reports whether agentName's event subscriber receives this
// kind. Callers ask BEFORE rendering an event to JSON, so an unsubscribed
// kind costs nothing beyond this lookup — which is what makes a per-token
// event stream affordable to hang a shell hook off.
func (c *LoadedConfig) SubscribesTo(agentName, kind string) bool {
	key := c.hookKey(agentName)
	if c.hooks.get(hookEvent, key).empty() {
		return false
	}
	return slices.Contains(c.eventOn[key], kind)
}

// RunEvent delivers one event to agentName's subscriber. payload is the
// already-rendered event JSON, handed to the function on stdin.
//
// An observer cannot refuse, rewrite, or delay anything, so unlike gate:/note:
// there is nothing here to fail closed on: its stdout is ignored and a failure
// is returned for the caller to log. Kinds the subscriber did not name are
// dropped without running it.
func (c *LoadedConfig) RunEvent(ctx context.Context, agentName, kind string, payload []byte) error {
	if !c.SubscribesTo(agentName, kind) {
		return nil
	}
	ref := c.hooks.get(hookEvent, c.hookKey(agentName))
	_, err := runHook(ctx, c.dir, ref, json.RawMessage(payload))
	return err
}

// VerifyHooks checks that every ACTION hook the kit declares — commands and
// event subscribers — is a function the shell can actually find, and returns
// one problem string per failure.
//
// It deliberately does NOT run them. A gate or a note is a decision function
// whose entire contract is to return a verdict, so dry-running one with a probe
// payload is free. A command or an event subscriber is an ACTION: running one
// to check it would post the message, push the commit, or send the mail every
// time someone typed `shell3 health`. Checking that the function is defined
// catches the failure that actually happens (a syntax error in the kit, a
// renamed function) without doing the work.
func (c *LoadedConfig) VerifyHooks(ctx context.Context) []string {
	type check struct{ label, fn, kit string }
	var checks []check
	for _, name := range slices.Sorted(maps.Keys(c.hooks[hookCommand])) {
		ref := c.hooks[hookCommand][name]
		checks = append(checks, check{"command /" + name, ref.fn, ref.kit})
	}
	for _, agent := range slices.Sorted(maps.Keys(c.hooks[hookEvent])) {
		ref := c.hooks[hookEvent][agent]
		label := "event subscriber for agent " + agent
		if agent == "" {
			label = "event subscriber for the main agent"
		}
		checks = append(checks, check{label, ref.fn, ref.kit})
	}

	var problems []string
	for _, ck := range checks {
		hctx, cancel := context.WithTimeout(ctx, hookTimeout)
		// `declare -F` prints the name and exits nonzero when the function is
		// not defined. Sourcing is safe for the same reason runHook's is: a
		// kit is definitions-only, so this costs a parse and runs nothing.
		script := fmt.Sprintf("set -uo pipefail; source %s; declare -F %s >/dev/null",
			kit.ShellQuote(ck.kit), kit.ShellQuote(ck.fn))
		cmd := exec.CommandContext(hctx, "bash", "-c", script)
		cmd.Dir = c.dir
		cmd.WaitDelay = time.Second
		stderr := &cappedBuffer{max: hookOutputCap}
		cmd.Stderr = stderr
		err := cmd.Run()
		cancel()
		if err != nil {
			msg := strings.TrimSpace(stderr.String())
			if msg == "" {
				msg = "function " + ck.fn + " is not defined after sourcing the kit"
			}
			problems = append(problems, ck.label+": "+msg)
		}
	}
	return problems
}
