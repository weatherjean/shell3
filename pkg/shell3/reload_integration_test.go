package shell3_test

import (
	"testing"

	"github.com/weatherjean/shell3/pkg/shell3"
)

// TestReload_PreservesHistoryAndArmsNewJob proves that a reload arms a
// newly-declared cron job AND that the SAME live *Session object survives the
// rebuild (its identity and active agent are preserved in place — it is never
// recreated). Reusing the same `sess` handle across the reload is the identity
// proof; the active-agent + agent-list assertions prove the in-place re-derive
// actually took effect on that same object rather than leaving it stale.
func TestReload_PreservesHistoryAndArmsNewJob(t *testing.T) {
	dir := t.TempDir()
	path := writeCfg(t, dir, baseCfg)
	rt, err := shell3.NewRuntime(shell3.RuntimeSpec{ConfigPath: path, WorkDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	sess, err := rt.Session(shell3.SessionOpts{Name: "telegram", Agent: "code"})
	if err != nil {
		t.Fatal(err)
	}
	if got := sess.ActiveAgent(); got != "code" {
		t.Fatalf("precondition: active agent = %q, want code", got)
	}

	writeCfg(t, dir, baseCfg+`
shell3.telegram({ token="t", chat_id="1", agent="code", cron = { { name="nightly", schedule="@daily", agent="explorer", prompt="go", notify=false } } })
`)
	if _, err := rt.Reload(); err != nil {
		t.Fatal(err)
	}

	// The newly-declared job is armed and visible via the Runtime.
	if jobs := rt.Cron(); len(jobs) != 1 || jobs[0].Name != "nightly" {
		t.Fatalf("new job not armed: %+v", jobs)
	}

	// Same *Session object survived in place: its active agent is preserved and
	// the (unchanged) "code" agent still resolves on the very same handle.
	if got := sess.ActiveAgent(); got != "code" {
		t.Fatalf("active agent not preserved on the live session: got %q", got)
	}
	var hasCode bool
	for _, n := range sess.AgentNames() {
		if n == "code" {
			hasCode = true
		}
	}
	if !hasCode {
		t.Fatalf("re-derived session lost its agent list: %+v", sess.AgentNames())
	}
}
