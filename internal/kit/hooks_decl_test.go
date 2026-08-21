package kit_test

import (
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/kit"
)

// A command: block binds the function under it and carries a description.
// Commands are install-wide, not scoped to an agent, so one declared before
// any agent block is legal.
func TestParseCommand(t *testing.T) {
	src := `#---
# command: standup
# description: Yesterday's commits
#---
cmd_standup() {
  echo hi
}

#---
# agent: main
#---
main_prompt() {
  cat <<'SHELL3_EOF'
you are main
SHELL3_EOF
}
`
	k, err := kit.Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	c, ok := k.Commands["standup"]
	if !ok {
		t.Fatalf("Commands = %+v, want a standup entry", k.Commands)
	}
	if c.Func != "cmd_standup" {
		t.Errorf("Func = %q, want cmd_standup", c.Func)
	}
	if c.Desc != "Yesterday's commits" {
		t.Errorf("Desc = %q", c.Desc)
	}
}

// A command needs a description: it is registered into the front-end's command
// menu, where an undescribed verb is unusable.
func TestParseCommandWithoutDescriptionFails(t *testing.T) {
	src := `#---
# command: standup
#---
cmd_standup() { echo hi; }

#---
# agent: main
#---
main_prompt() {
  cat <<'SHELL3_EOF'
you are main
SHELL3_EOF
}
`
	_, err := kit.Parse([]byte(src))
	if err == nil || !strings.Contains(err.Error(), "description") {
		t.Fatalf("err = %v, want a missing-description error", err)
	}
}

// Two commands of the same name is a typo, not a redefinition.
func TestParseDuplicateCommandFails(t *testing.T) {
	src := `#---
# command: standup
# description: one
#---
cmd_a() { echo a; }

#---
# command: standup
# description: two
#---
cmd_b() { echo b; }

#---
# agent: main
#---
main_prompt() {
  cat <<'SHELL3_EOF'
you are main
SHELL3_EOF
}
`
	_, err := kit.Parse([]byte(src))
	if err == nil || !strings.Contains(err.Error(), "already declared") {
		t.Fatalf("err = %v, want a duplicate-command error", err)
	}
}

// An event: block is NAMED like gate:/note: — one function observes several
// agents — and carries a mandatory kind filter.
func TestParseEvent(t *testing.T) {
	src := `#---
# agent: main
#---
main_prompt() {
  cat <<'SHELL3_EOF'
you are main
SHELL3_EOF
}

#---
# event: [main]
# on: [turn_done, tool_result]
#---
ev_log() { cat >> /tmp/x; }
`
	k, err := kit.Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	e, ok := k.Events["main"]
	if !ok {
		t.Fatalf("Events = %+v, want a main entry", k.Events)
	}
	if e.Func != "ev_log" {
		t.Errorf("Func = %q, want ev_log", e.Func)
	}
	if strings.Join(e.On, ",") != "turn_done,tool_result" {
		t.Errorf("On = %v", e.On)
	}
}

// on: is mandatory. assistant_token fires per streamed token, so an
// unfiltered subscriber would fork a shell thousands of times per turn.
func TestParseEventWithoutOnFails(t *testing.T) {
	src := `#---
# agent: main
#---
main_prompt() {
  cat <<'SHELL3_EOF'
you are main
SHELL3_EOF
}

#---
# event: [main]
#---
ev_log() { cat; }
`
	_, err := kit.Parse([]byte(src))
	if err == nil || !strings.Contains(err.Error(), "on:") {
		t.Fatalf("err = %v, want a missing-on: error", err)
	}
}

// An unknown event kind is a typo that would otherwise present as a
// subscriber that silently never fires.
func TestParseEventUnknownKindFails(t *testing.T) {
	src := `#---
# agent: main
#---
main_prompt() {
  cat <<'SHELL3_EOF'
you are main
SHELL3_EOF
}

#---
# event: [main]
# on: [turn_dome]
#---
ev_log() { cat; }
`
	_, err := kit.Parse([]byte(src))
	if err == nil || !strings.Contains(err.Error(), "turn_dome") {
		t.Fatalf("err = %v, want an unknown-kind error naming turn_dome", err)
	}
}

// event: names agents, so a name the kit does not declare is a load error —
// the same rule gate:/note: already follow.
func TestParseEventUnknownAgentFails(t *testing.T) {
	src := `#---
# agent: main
#---
main_prompt() {
  cat <<'SHELL3_EOF'
you are main
SHELL3_EOF
}

#---
# event: [nope]
# on: [turn_done]
#---
ev_log() { cat; }
`
	_, err := kit.Parse([]byte(src))
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("err = %v, want an unknown-agent error", err)
	}
}

// A command named after a built-in is a load error, not a silent shadow: the
// built-in always wins at dispatch, so the declaration would never fire and
// the author would have no way to see why.
func TestParseCommandShadowingBuiltinFails(t *testing.T) {
	src := `#---
# command: stop
# description: shadow attempt
#---
cmd_stop() { echo hi; }

#---
# agent: main
#---
main_prompt() {
  cat <<'SHELL3_EOF'
you are main
SHELL3_EOF
}
`
	_, err := kit.Parse([]byte(src))
	if err == nil || !strings.Contains(err.Error(), "built-in") {
		t.Fatalf("err = %v, want a built-in-collision error", err)
	}
}
