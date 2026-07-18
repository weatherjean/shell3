package shell3_test

import (
	"context"
	"testing"

	"github.com/weatherjean/shell3/internal/shell3"
)

func TestRuntime_CronConfig(t *testing.T) {
	dir := t.TempDir()
	writeBaseTree(t, dir, map[string]string{
		"shell3.yaml":        baseYAML + "telegram:\n  token: t\n  chat_id: \"1\"\n",
		"agents/explorer.md": "---\ndescription: d\n---\np\n",
		"cron/n.md":          "---\nschedule: \"@daily\"\nagent: explorer\n---\ngo\n",
	})
	rt, err := shell3.NewRuntime(context.Background(), shell3.RuntimeSpec{ConfigDir: dir, WorkDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	jobs := rt.Cron()
	if len(jobs) != 1 || jobs[0].Name != "n" || jobs[0].Agent != "explorer" || jobs[0].Notify {
		t.Fatalf("bad cron config: %+v", jobs)
	}
}
