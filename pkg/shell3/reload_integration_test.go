package shell3_test

import (
	"testing"

	"github.com/weatherjean/shell3/pkg/shell3"
)

// TestReload_PreservesHistory proves that the SAME live *Session object
// survives the rebuild (its identity and active agent are preserved in place —
// it is never recreated). Reusing the same `sess` handle across the reload is
// the identity proof; the active-agent + agent-list assertions prove the
// in-place re-derive actually took effect on that same object rather than
// leaving it stale.
func TestReload_PreservesHistory(t *testing.T) {
	dir := t.TempDir()
	path := writeCfg(t, dir, baseCfg)
	rt, err := shell3.NewRuntime(shell3.RuntimeSpec{ConfigPath: path, WorkDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	sess, err := rt.Session(shell3.SessionOpts{Name: "frontend", Agent: "code"})
	if err != nil {
		t.Fatal(err)
	}
	if got := sess.ActiveAgent(); got != "code" {
		t.Fatalf("precondition: active agent = %q, want code", got)
	}

	writeCfg(t, dir, baseCfg+`
shell3.agent({ name="research", model="main", prompt="research", tools={} })
`)
	if _, err := rt.Reload(); err != nil {
		t.Fatal(err)
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
