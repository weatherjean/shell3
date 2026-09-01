package kit

import (
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/notify"
)

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

func TestParseCron_WorkdirAndSchedule(t *testing.T) {
	k, err := Parse(cronKit(`#---
# cron: tick
# schedule: "*/5 * * * *"
# agent: main
# workdir: ~/work
#---
p() { cat <<'EOF'
tick
EOF
}
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
		"no agent": {`#---
# cron: x
# schedule: "@daily"
#---
`, "needs an agent:"},
		"agent and tool together": {`#---
# cron: x
# schedule: "@daily"
# agent: main
# tool: sync
#---
p() { cat <<'EOF'
body
EOF
}
`, "cron jobs run agent turns only"},
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
		"duplicate name": {`#---
# cron: x
# schedule: "@daily"
# agent: main
#---
a() { cat <<'EOF'
body
EOF
}
#---
# cron: x
# schedule: "@hourly"
# agent: main
#---
b() { cat <<'EOF'
body
EOF
}
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

func TestParseCron_DoesNotDisturbScope(t *testing.T) {
	k, err := Parse(cronKit(`#---
# cron: mid
# schedule: "@daily"
# agent: main
#---
mid_cron() { cat <<'EOF'
mid
EOF
}

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

func TestParseCron_SecondKindIsAnError(t *testing.T) {
	_, err := Parse(cronKit(`#---
# cron: x
# schedule: "@daily"
# agent: main
# command: greet
# description: hi
#---
greet_impl() { echo hi; }
`))
	if err == nil || !strings.Contains(err.Error(), "more than one") {
		t.Fatalf("want the multi-kind error, got %v", err)
	}
}

func TestParseCron_ToolJobIsRefused(t *testing.T) {
	_, err := Parse(cronKit(`#---
# cron: sync-job
# schedule: "@every 30m"
# tool: sync
#---
`))
	if err == nil {
		t.Fatal("Parse accepted a cron tool: job; want a load error")
	}
	if !strings.Contains(err.Error(), "cron jobs run agent turns only") {
		t.Fatalf("Parse error = %v; want it to name the replacement", err)
	}
}
