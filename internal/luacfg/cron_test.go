package luacfg

import (
	"strings"
	"testing"
)

func TestLoadCron(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", `
shell3.model("main", { base_url="https://api.x/v1", api_key="k", model="m-1", context_window=1000 })
local explorer = shell3.subagent({ name="explorer", model="main", description="d", prompt="p", tools={} })
shell3.agent({ name="code", model="main", prompt="hi", tools={ subagents={explorer} } })
shell3.telegram({ token="t", chat_id="1", agent="code", cron = {
  { name="nightly", schedule="0 9 * * *", agent="explorer", prompt="summarize", notify=true },
  { schedule="@hourly", agent="explorer", prompt="check", workdir="/tmp" },
}})
`)
	c, err := Load(dir + "/shell3.lua")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	jobs := c.Cron()
	if len(jobs) != 2 {
		t.Fatalf("want 2 jobs, got %d", len(jobs))
	}
	if jobs[0].Name != "nightly" || jobs[0].Schedule != "0 9 * * *" || jobs[0].Agent != "explorer" || !jobs[0].Notify {
		t.Fatalf("bad job 0: %+v", jobs[0])
	}
	// notify defaults to true when omitted; name defaults to job-<n>.
	if !jobs[1].Notify || jobs[1].Name != "job-2" || jobs[1].WorkDir != "/tmp" {
		t.Fatalf("bad job 1 defaults: %+v", jobs[1])
	}
}

func TestLoadCronUnknownAgent(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", `
shell3.model("main", { base_url="https://api.x/v1", api_key="k", model="m-1", context_window=1000 })
shell3.agent({ name="code", model="main", prompt="hi", tools={} })
shell3.telegram({ token="t", chat_id="1", agent="code", cron = { { schedule="@daily", agent="ghost", prompt="x" } } })
`)
	_, err := Load(dir + "/shell3.lua")
	if err == nil || !strings.Contains(err.Error(), `unknown subagent "ghost"`) {
		t.Fatalf("want unknown-subagent error, got %v", err)
	}
}

func TestLoadCronUnknownKey(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", `
shell3.model("main", { base_url="https://api.x/v1", api_key="k", model="m-1", context_window=1000 })
shell3.agent({ name="code", model="main", prompt="hi", tools={} })
shell3.telegram({ token="t", chat_id="1", agent="code", cron = { { schedule="@daily", agent="code", prompt="x", nope=true } } })
`)
	_, err := Load(dir + "/shell3.lua")
	if err == nil || !strings.Contains(err.Error(), `unknown key "nope"`) {
		t.Fatalf("want unknown-key error, got %v", err)
	}
}

// TestTopLevelCronRemoved pins the clean break: the old top-level shell3.cron
// global no longer exists, so calling it raises a nil-value error.
func TestTopLevelCronRemoved(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", `
shell3.model("main", { base_url="https://api.x/v1", api_key="k", model="m-1", context_window=1000 })
shell3.agent({ name="code", model="main", prompt="hi", tools={} })
shell3.cron({ jobs = { { schedule="@daily", agent="code", prompt="x" } } })
`)
	_, err := Load(dir + "/shell3.lua")
	if err == nil || !strings.Contains(err.Error(), "attempt to call a non-function object") {
		t.Fatalf("want call-error for removed shell3.cron, got %v", err)
	}
}
