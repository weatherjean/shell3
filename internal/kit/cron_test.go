package kit

import (
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/notify"
)

// cronKit wraps declaration text in a minimal kit that declares the agents
// and tools a cron block may name, so the end-of-parse resolution checks have
// something real to resolve against.
func cronKit(decls string) []byte {
	return []byte(`#---
# agent: main
# description: the main agent
#---
main_prompt() { cat <<'EOF'
main
EOF
}

#---
# tool: sync
# description: sync things
#---
sync_impl() { echo ok; }

#---
# tool: needs_arg
# description: wants an argument
# params:
#   what: {type: string, required: true}
#---
needs_arg_impl() { echo "$what"; }

` + decls)
}

func TestParseCron_AgentJob(t *testing.T) {
	k, err := Parse(cronKit(`#---
# cron: daily
# schedule: "@daily"
# agent: main
# report: raw
#---
cron_daily() { cat <<'EOF'
Summarize the day.
EOF
}
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(k.Crons) != 1 {
		t.Fatalf("crons = %d, want 1", len(k.Crons))
	}
	j := k.Crons[0]
	if j.Name != "daily" || j.Schedule != "@daily" || j.Agent != "main" || j.Report != notify.ReportRaw {
		t.Fatalf("job = %+v", j)
	}
	if j.Prompt != "Summarize the day." {
		t.Fatalf("prompt = %q", j.Prompt)
	}
	if j.Func != "cron_daily" {
		t.Fatalf("func = %q", j.Func)
	}
}

// A tool job binds NO function: it has no prompt, so the next function in the
// file is somebody else's implementation and must not be swallowed.
func TestParseCron_ToolJobBindsNoFunction(t *testing.T) {
	k, err := Parse(cronKit(`#---
# cron: sync-notion
# schedule: "@every 30m"
# tool: sync
#---
`))
	if err != nil {
		t.Fatal(err)
	}
	j := k.Crons[0]
	if j.Tool != "sync" || j.Agent != "" || j.Func != "" || j.Prompt != "" {
		t.Fatalf("job = %+v", j)
	}
}

func TestParseCron_WorkdirAndSchedule(t *testing.T) {
	k, err := Parse(cronKit(`#---
# cron: tick
# schedule: "*/5 * * * *"
# tool: sync
# workdir: ~/work
#---
`))
	if err != nil {
		t.Fatal(err)
	}
	if k.Crons[0].WorkDir != "~/work" {
		t.Fatalf("workdir = %q", k.Crons[0].WorkDir)
	}
}

func TestParseCronErrors(t *testing.T) {
	for name, tc := range map[string]struct{ src, want string }{
		"no schedule": {`#---
# cron: x
# agent: main
#---
p() { cat <<'EOF'
body
EOF
}
`, "needs a schedule"},
		"bad schedule": {`#---
# cron: x
# schedule: every 5 min
# agent: main
#---
p() { cat <<'EOF'
body
EOF
}
`, "invalid schedule"},
		"neither agent nor tool": {`#---
# cron: x
# schedule: "@daily"
#---
`, "exactly one of agent: or tool:"},
		"both agent and tool": {`#---
# cron: x
# schedule: "@daily"
# agent: main
# tool: sync
#---
p() { cat <<'EOF'
body
EOF
}
`, "exactly one of agent: or tool:"},
		"tool and report": {`#---
# cron: x
# schedule: "@daily"
# tool: sync
# report: raw
#---
`, "report only applies to an agent: job"},
		// direct: was the pre-report spelling. It must fail with a message
		// naming the replacement, not with yaml's "field not found".
		"the removed direct: key": {`#---
# cron: x
# schedule: "@daily"
# agent: main
# direct: true
#---
p() { cat <<'EOF'
body
EOF
}
`, "direct: was replaced by report:"},
		"an unknown report mode": {`#---
# cron: x
# schedule: "@daily"
# agent: main
# report: alwyas
#---
p() { cat <<'EOF'
body
EOF
}
`, "unknown report mode"},
		"agent job with no function": {`#---
# cron: x
# schedule: "@daily"
# agent: main
#---
`, "no prompt function"},
		"agent job with no heredoc": {`#---
# cron: x
# schedule: "@daily"
# agent: main
#---
p() { echo hi; }
`, "no heredoc"},
		"unknown agent": {`#---
# cron: x
# schedule: "@daily"
# agent: ghost
#---
p() { cat <<'EOF'
body
EOF
}
`, "does not declare"},
		"unknown tool": {`#---
# cron: x
# schedule: "@daily"
# tool: ghost
#---
`, "does not declare"},
		"tool needs an argument": {`#---
# cron: x
# schedule: "@daily"
# tool: needs_arg
#---
`, "passes no arguments"},
		"duplicate name": {`#---
# cron: x
# schedule: "@daily"
# tool: sync
#---
#---
# cron: x
# schedule: "@hourly"
# tool: sync
#---
`, "already declared"},
	} {
		_, err := Parse(cronKit(tc.src))
		if err == nil {
			t.Errorf("%s: expected an error", name)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error %q does not contain %q", name, err, tc.want)
		}
	}
}

// A cron: block names itself the way tool:/command: do, so it must not open,
// close, or otherwise disturb the scope a tool: block files into — a cron
// declared mid-agent would otherwise orphan every tool below it.
func TestParseCron_DoesNotDisturbScope(t *testing.T) {
	k, err := Parse(cronKit(`#---
# cron: mid
# schedule: "@daily"
# tool: sync
#---

#---
# tool: after
# description: declared after a cron block
#---
after_impl() { echo ok; }
`))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tl := range k.Agents[0].Tools {
		if tl.Name == "after" {
			found = true
		}
	}
	if !found {
		t.Fatalf("tool declared after a cron block lost its scope: %+v", k.Agents[0].Tools)
	}
}

// A cron block that also names another declaration kind is ambiguous: agent:
// and tool: are cron PAYLOAD, but skill:/command:/gate: are not.
func TestParseCron_SecondKindIsAnError(t *testing.T) {
	_, err := Parse(cronKit(`#---
# cron: x
# schedule: "@daily"
# tool: sync
# command: greet
# description: hi
#---
greet_impl() { echo hi; }
`))
	if err == nil || !strings.Contains(err.Error(), "more than one") {
		t.Fatalf("want the multi-kind error, got %v", err)
	}
}

// A cron tool job resolves against the WHOLE kit, so a name two scopes each
// declare is ambiguous and must fail at load rather than run whichever
// function happened to parse first.
func TestParseCron_AmbiguousToolScope(t *testing.T) {
	src := []byte(`#---
# agent: main
# description: main
#---
main_prompt() { cat <<'EOF'
main
EOF
}

#---
# tool: dup
# description: one
#---
dup_a() { echo a; }

#---
# agent: other
# description: other
#---
other_prompt() { cat <<'EOF'
other
EOF
}

#---
# tool: dup
# description: two
#---
dup_b() { echo b; }

#---
# cron: x
# schedule: "@daily"
# tool: dup
#---
`)
	_, err := Parse(src)
	if err == nil || !strings.Contains(err.Error(), "more than one scope") {
		t.Fatalf("want the ambiguous-scope error, got %v", err)
	}
}
