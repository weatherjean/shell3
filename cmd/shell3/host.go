//go:build unix

package main

import (
	"context"
	"fmt"

	"github.com/weatherjean/shell3/internal/cron"
	"github.com/weatherjean/shell3/internal/shell3"
)

// Shared wiring for the hosted front-ends (telegram, serve, ask). Each helper is small
// on purpose: the commands stay readable top-to-bottom while the invariants
// (config-dir anchoring, cron fail-fast) live in exactly one place.

// openRuntime resolves the --config value and builds a Runtime anchored to
// the config directory — the runtime root determines where runs/ + history
// live (runs.Open under <workdir>/.shell3_project), so tying it to the config
// dir keeps a hosted agent self-contained. Returns the resolved config dir,
// the host's home.
func openRuntime(ctx context.Context, configDir string) (*shell3.Runtime, string, error) {
	resolved, err := resolveConfig(configDir)
	if err != nil {
		return nil, "", err
	}
	rt, err := shell3.NewRuntime(ctx, shell3.RuntimeSpec{ConfigDir: resolved, WorkDir: resolved})
	if err != nil {
		return nil, "", err
	}
	return rt, resolved, nil
}

// armCron builds and starts a scheduler for the declared jobs (nil when there
// are none). Fail-fast: a malformed schedule is a startup error. The caller
// owns Stop.
func armCron(disp cron.Dispatcher, jobs []shell3.CronJob) (*cron.Scheduler, error) {
	if len(jobs) == 0 {
		return nil, nil
	}
	sched, err := cron.New(disp, jobs)
	if err != nil {
		return nil, err
	}
	sched.Start()
	fmt.Printf("cron: %d job(s) scheduled\n", len(jobs))
	return sched, nil
}
