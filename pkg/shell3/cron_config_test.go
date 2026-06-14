package shell3_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/weatherjean/shell3/pkg/shell3"
)

func TestRuntime_CronConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := `
shell3.model("main", { base_url="https://api.x/v1", api_key="k", model="m-1", context_window=1000 })
local explorer = shell3.subagent({ name="explorer", model="main", description="d", prompt="p", tools={} })
shell3.agent({ name="code", model="main", prompt="hi", tools={ subagents={explorer} } })
shell3.telegram({ token="t", chat_id="1", agent="code", cron = { { name="n", schedule="@daily", agent="explorer", prompt="go", notify=false } } })
`
	path := filepath.Join(dir, "shell3.lua")
	if err := os.WriteFile(path, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	rt, err := shell3.NewRuntime(shell3.RuntimeSpec{ConfigPath: path, WorkDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	jobs := rt.Cron()
	if len(jobs) != 1 || jobs[0].Name != "n" || jobs[0].Agent != "explorer" || jobs[0].Notify {
		t.Fatalf("bad cron config: %+v", jobs)
	}
}
