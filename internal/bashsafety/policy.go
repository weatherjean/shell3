// Package bashsafety is the pure decision core for shell3.bash_safety: given a
// command string and an allow/deny policy, it classifies the command as Run,
// Ask, or Deny. It interprets only the `*` wildcard and the shell operators that
// chain commands — it deliberately does NOT parse quoting or escapes (a cheap
// operator scan, not a shell grammar). Splitting on operators stops an
// allowlisted prefix from smuggling a non-allowlisted suffix
// (e.g. "git status && rm -rf ~").
package bashsafety

import (
	"regexp"
	"strings"
	"time"
)

// DefaultAskTimeout bounds how long an ask-verdict waits for a human before it
// falls back to deny. It applies when a policy enables bash_safety but does not
// set its own ask_timeout. Without a bound, an un-answered Telegram approval
// would block the turn goroutine forever.
const DefaultAskTimeout = 5 * time.Minute

// Verdict is the classification of a command under a Policy.
type Verdict int

const (
	// Run: every segment is allowlisted (or the policy is disabled).
	Run Verdict = iota
	// Ask: at least one segment is on neither list — a human must confirm.
	Ask
	// Deny: at least one segment is denylisted — hard block, never asked.
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

// Policy is the allow/deny configuration from shell3.bash_safety.
type Policy struct {
	Enabled bool
	Allow   []string
	Deny    []string
	// AskTimeout bounds how long an ask-verdict waits for a human before the gate
	// gives up and denies. Zero means "no bound" (wait forever); the config loader
	// substitutes DefaultAskTimeout when the user leaves ask_timeout unset.
	AskTimeout time.Duration
}

// splitRe matches the operators that separate or introduce a command: && || |
// ; newline, a lone & (background), the $( command-substitution open, a
// backtick, and the redirection operators >> >  <<< << < (which introduce a
// file/target that must not ride along inside an allowlisted segment). Longer
// operators are listed first so "&&"/"||"/">>"/"<<<"/"<<" win over the bare
// single-character forms in the trailing character class. This is a cheap
// heuristic scan, NOT a shell parser: it does not understand quotes/escapes,
// and it does not catch every command-introducing construct (e.g. process
// substitution <(...) ). deny is therefore best-effort; the allow list is the
// real safety boundary (see Decide's doc).
var splitRe = regexp.MustCompile(`\s*(?:&&|\|\||\$\(|>>|<<<|<<|[<>|;&\n` + "`" + `])\s*`)

// Split breaks a command into operator-separated segments, trimmed, with empties
// dropped. It does not understand quotes — a ';' inside a quoted string is split
// too. That errs toward more segments (more asks), which is fail-safe.
func Split(command string) []string {
	parts := splitRe.Split(command, -1)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		// Splitting on "$(" leaves the substitution's closing ')' glued to the
		// last inner segment (e.g. "$(rm -rf /)" → "rm -rf /)"). Strip trailing
		// unbalanced ')' so a deny/allow glob without a trailing '*' still matches
		// the bare command inside the substitution.
		for strings.HasSuffix(p, ")") && strings.Count(p, ")") > strings.Count(p, "(") {
			p = strings.TrimSpace(strings.TrimSuffix(p, ")"))
		}
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Decide classifies command under p. Reason is human-readable for Deny/Ask and
// empty for Run. Deny wins over Allow: any denied segment denies the whole
// command, regardless of the other segments.
func (p Policy) Decide(command string) (Verdict, string) {
	if !p.Enabled {
		return Run, ""
	}
	askSeg := ""
	for _, seg := range Split(command) {
		switch {
		case matchAny(p.Deny, seg):
			return Deny, "matches a bash_safety deny rule: " + seg
		case matchAny(p.Allow, seg):
			// allowlisted — fine
		default:
			if askSeg == "" {
				askSeg = seg
			}
		}
	}
	if askSeg != "" {
		return Ask, "not on the bash_safety allowlist: " + askSeg
	}
	return Run, ""
}

// matchAny reports whether s matches any glob pattern in pats.
func matchAny(pats []string, s string) bool {
	for _, pat := range pats {
		if matchGlob(pat, s) {
			return true
		}
	}
	return false
}

// matchGlob does a whole-string (anchored) match of s against pattern, where the
// only wildcard is `*` (→ ".*"). All other characters are matched literally.
func matchGlob(pattern, s string) bool {
	var b strings.Builder
	b.WriteString("^")
	for _, part := range strings.Split(pattern, "*") {
		b.WriteString(regexp.QuoteMeta(part))
		b.WriteString(".*")
	}
	// The loop appends a trailing ".*" after the last part; trim it so a pattern
	// without a trailing '*' stays anchored at the end.
	expr := strings.TrimSuffix(b.String(), ".*") + "$"
	re, err := regexp.Compile(expr)
	if err != nil {
		return false
	}
	return re.MatchString(s)
}
