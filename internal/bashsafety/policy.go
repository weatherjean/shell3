// Package bashsafety is the pure decision core for shell3.bash_safety: given a
// command string and two regex lists, it classifies the command as Run, Ask, or
// Deny. A hard_deny match hard-blocks (never prompted); a deny match prompts the
// human for approval (allow/deny); a command matching neither runs.
//
// Patterns are full-command regexes (regexp.MatchString, unanchored), so a match
// is found anywhere in the command — chaining or substitution can't hide a denied
// command behind a benign prefix (e.g. "echo hi; rm -rf /" still matches
// `rm\s+-rf`). They are compiled with DOTALL (see CompileRule) so "." also spans
// newlines: a command split across lines can't slip a flagged fragment past a
// ".*" pattern. There is no allowlist and no command splitting: bash is unsafe by
// default, and these lists are the regex guardrails.
package bashsafety

import (
	"regexp"
	"strings"
	"time"
)

// dotAllPrefix forces "." to match newlines too. A bash command can contain
// literal newlines, so without this a ".*"-style pattern silently fails to span
// a multi-line command. Broadening a denylist match is always fail-safe (more
// prompts/blocks, never fewer), so DOTALL is the default for every rule.
const dotAllPrefix = "(?s)"

// CompileRule compiles one deny/hard_deny pattern with DOTALL semantics. The
// config loader calls this (a bad pattern is a load error); tests use it too, so
// production and test matching share identical semantics.
func CompileRule(pattern string) (*regexp.Regexp, error) {
	return regexp.Compile(dotAllPrefix + pattern)
}

// rulePattern renders a compiled rule's source for a human-readable reason,
// hiding the internal DOTALL prefix so the user sees the pattern they wrote.
func rulePattern(re *regexp.Regexp) string {
	return strings.TrimPrefix(re.String(), dotAllPrefix)
}

// DefaultAskTimeout bounds how long an ask-verdict waits for a human before it
// falls back to deny. It applies when a policy enables bash_safety but does not
// set its own ask_timeout. Without a bound, an un-answered approval would block
// the turn goroutine forever.
const DefaultAskTimeout = 5 * time.Minute

// Verdict is the classification of a command under a Policy.
type Verdict int

const (
	// Run: matches no rule (or the policy is disabled).
	Run Verdict = iota
	// Ask: matches a deny rule — a human must confirm before it runs.
	Ask
	// Deny: matches a hard_deny rule — hard block, never prompted.
	Deny
)

func (v Verdict) String() string {
	switch v {
	case Run:
		return "run"
	case Ask:
		return "ask"
	case Deny:
		return "deny"
	default:
		return "unknown"
	}
}

// Policy is the compiled configuration from shell3.bash_safety. Deny and HardDeny
// hold regexes compiled by the config loader (a bad pattern is a load error).
type Policy struct {
	Enabled bool
	// Deny patterns prompt the human (allow/deny) on a match — blocked unless
	// approved. A headless run (no asker) denies the match.
	Deny []*regexp.Regexp
	// HardDeny patterns hard-block on a match: never prompted, never run. Checked
	// before Deny, so hard_deny wins.
	HardDeny []*regexp.Regexp
	// AskTimeout bounds how long an ask-verdict waits for a human before the gate
	// gives up and denies. Zero means "no bound" (wait forever); the config loader
	// substitutes DefaultAskTimeout when the user leaves ask_timeout unset.
	AskTimeout time.Duration
}

// Decide classifies command under p. Reason is human-readable for Deny/Ask and
// empty for Run. HardDeny is checked first (it wins over Deny).
func (p Policy) Decide(command string) (Verdict, string) {
	if !p.Enabled {
		return Run, ""
	}
	for _, re := range p.HardDeny {
		if re.MatchString(command) {
			return Deny, "matches a bash_safety hard_deny rule: " + rulePattern(re)
		}
	}
	for _, re := range p.Deny {
		if re.MatchString(command) {
			return Ask, "matches a bash_safety deny rule: " + rulePattern(re)
		}
	}
	return Run, ""
}
