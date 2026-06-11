package shell3_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/pkg/shell3"
)

func writeCfg(t *testing.T, dir, body string) string {
	t.Helper()
	p := filepath.Join(dir, "shell3.lua")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const baseCfg = `
shell3.model("main", { base_url="https://api.x/v1", api_key="k", model="m-1", context_window=1000 })
local explorer = shell3.subagent({ name="explorer", model="main", description="d", prompt="p", tools={} })
shell3.agent({ name="code", model="main", prompt="hi", tools={ subagents={explorer} } })
`

func TestReload_AddAgentTakesEffect(t *testing.T) {
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
	writeCfg(t, dir, baseCfg+`
shell3.agent({ name="research", model="main", prompt="research", tools={} })
shell3.cron({ jobs = { { name="n", schedule="@daily", agent="explorer", prompt="go" } } })
`)
	res, err := rt.Reload()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if err := sess.SwitchAgent("research"); err != nil {
		t.Fatalf("new agent not live after reload: %v", err)
	}
	if jobs := rt.Cron(); len(jobs) != 1 || jobs[0].Name != "n" {
		t.Fatalf("cron not reloaded: %+v", jobs)
	}
	if res.Agents < 2 || res.Jobs != 1 {
		t.Fatalf("bad reload result: %+v", res)
	}
}

func TestReload_InvalidKeepsOldConfig(t *testing.T) {
	dir := t.TempDir()
	path := writeCfg(t, dir, baseCfg)
	rt, err := shell3.NewRuntime(shell3.RuntimeSpec{ConfigPath: path, WorkDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	sess, _ := rt.Session(shell3.SessionOpts{Name: "telegram", Agent: "code"})
	writeCfg(t, dir, baseCfg+`
shell3.cron({ jobs = { { schedule="@daily", agent="ghost", prompt="x" } } })
`)
	if _, err := rt.Reload(); err == nil {
		t.Fatal("expected reload to reject the invalid config")
	}
	if err := sess.SwitchAgent("code"); err != nil {
		t.Fatalf("old config broken after failed reload: %v", err)
	}
	if jobs := rt.Cron(); len(jobs) != 0 {
		t.Fatalf("failed reload must not arm jobs: %+v", jobs)
	}
}

func TestReload_RestoresAgentAndParams(t *testing.T) {
	dir := t.TempDir()
	path := writeCfg(t, dir, baseCfg+`
shell3.agent({ name="research", model="main", prompt="research", tools={} })
`)
	rt, err := shell3.NewRuntime(shell3.RuntimeSpec{ConfigPath: path, WorkDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	sess, _ := rt.Session(shell3.SessionOpts{Name: "telegram", Agent: "code"})
	if err := sess.SwitchAgent("research"); err != nil {
		t.Fatal(err)
	}
	writeCfg(t, dir, baseCfg+`
shell3.agent({ name="research", model="main", prompt="research v2", tools={} })
`)
	if _, err := rt.Reload(); err != nil {
		t.Fatal(err)
	}
	if got := sess.ActiveAgent(); got != "research" {
		t.Fatalf("active agent not preserved across reload: got %q", got)
	}
}

func TestReload_DeletedActiveAgentFallsBack(t *testing.T) {
	dir := t.TempDir()
	path := writeCfg(t, dir, baseCfg+`
shell3.agent({ name="research", model="main", prompt="research", tools={} })
`)
	rt, err := shell3.NewRuntime(shell3.RuntimeSpec{ConfigPath: path, WorkDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	sess, _ := rt.Session(shell3.SessionOpts{Name: "telegram", Agent: "code"})
	if err := sess.SwitchAgent("research"); err != nil {
		t.Fatal(err)
	}
	writeCfg(t, dir, baseCfg)
	res, err := rt.Reload()
	if err != nil {
		t.Fatalf("reload should not error on deleted active agent: %v", err)
	}
	if got := sess.ActiveAgent(); got == "research" {
		t.Fatal("deleted agent should not remain active")
	}
	if !strings.Contains(strings.Join(res.Notes, " "), "research") {
		t.Fatalf("expected a note about the dropped agent, got %+v", res.Notes)
	}
}
