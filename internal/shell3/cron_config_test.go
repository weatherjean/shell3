package shell3_test

import (
	"context"
	"testing"

	"github.com/weatherjean/shell3/internal/shell3"
)

func TestRuntime_CronConfig(t *testing.T) {
	dir := t.TempDir()
	writeBaseTree(t, dir, map[string]string{
		"shell3.sh": baseWiring + kitAgentDecl("main", "hi") +
			kitAgentDecl("explorer", "p", "description: d") + `
#---
# cron: n
# schedule: "@daily"
# agent: explorer
#---
cron_n() { cat <<'EOF2'
go
EOF2
}
`,
	})
	rt, err := shell3.NewRuntime(context.Background(), shell3.RuntimeSpec{ConfigDir: dir, WorkDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	jobs := rt.Cron()
	if len(jobs) != 1 || jobs[0].Name != "n" || jobs[0].Agent != "explorer" {
		t.Fatalf("bad cron config: %+v", jobs)
	}
	jobs[0].Name = "mutated"
	if got := rt.Cron()[0].Name; got != "n" {
		t.Fatalf("Cron returned mutable runtime state: %q", got)
	}
}
