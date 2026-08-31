//go:build unix

package main

import (
	"os"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/runs"
)

func TestAskResumeHint(t *testing.T) {
	got := askResumeHint()
	if !strings.Contains(got, "shell3 ask --resume") {
		t.Errorf("resume hint missing the resume invocation: %q", got)
	}
	if strings.Contains(got, "shell3 dev") {
		t.Errorf("resume hint still references the old command name: %q", got)
	}
}

func TestAskHeadless(t *testing.T) {
	cases := []struct {
		name                                       string
		interactive, scripted, stdinTTY, stderrTTY bool
		want                                       bool
	}{
		{"chat ui", true, false, true, true, false},
		{"chat ui with stderr redirected", true, false, true, false, false},
		{"message typed at a tty", false, false, true, true, false},
		{"scripted -p even with tty", false, true, true, true, true},
		{"no stdin tty (piped)", false, false, false, true, true},
		{"no stderr tty", false, false, true, false, true},
		{"scripted and no tty", false, true, false, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := askHeadless(c.interactive, c.scripted, c.stdinTTY, c.stderrTTY); got != c.want {
				t.Errorf("askHeadless(%v,%v,%v,%v) = %v, want %v",
					c.interactive, c.scripted, c.stdinTTY, c.stderrTTY, got, c.want)
			}
		})
	}
}

func TestAskSurfaceIsNotTelegrams(t *testing.T) {
	if strings.HasPrefix(askSurface, "telegram") {
		t.Fatalf("ask surface %q collides with the Telegram front-end's namespace", askSurface)
	}
	if askSurface == "" {
		t.Fatal("ask surface must not be empty — an empty key resolves nothing")
	}
}

func TestAskResumeFollowsItsOwnThread(t *testing.T) {
	st, err := runs.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	if got := askResumeID(st); got != "" {
		t.Fatalf("askResumeID with no marker = %q, want empty", got)
	}

	askID, err := st.NewSession(runs.Meta{Workdir: "/w", ConfigDir: "/c"})
	if err != nil {
		t.Fatal(err)
	}
	rememberAskSession(st, askID)

	tgID, err := st.NewSession(runs.Meta{Workdir: "/w", ConfigDir: "/c"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetCurrentSession("telegram:-100", tgID); err != nil {
		t.Fatal(err)
	}

	if got := askResumeID(st); got != askID {
		t.Fatalf("askResumeID = %q, want ask's own session %q (not the Telegram room's %q)", got, askID, tgID)
	}
}

func TestAskResumeSkipsSweptSession(t *testing.T) {
	st, err := runs.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	rememberAskSession(st, "sess-that-was-swept")
	if got := askResumeID(st); got != "" {
		t.Fatalf("askResumeID for a missing session = %q, want empty", got)
	}
}

func TestAskAgentDoesNotClaimTheMarker(t *testing.T) {
	src, err := os.ReadFile("ask.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	agentBranch := strings.Index(s, "return runAskAgent(")
	remember := strings.Index(s, "rememberAskSession(rt.Parts().Store()")
	if agentBranch < 0 || remember < 0 {
		t.Fatal("ask.go no longer has both the --agent branch and the marker write")
	}
	if remember < agentBranch {
		t.Error("rememberAskSession runs before the --agent early return, so an --agent run claims ask's resume marker")
	}
}
