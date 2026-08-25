//go:build unix

package main

import (
	"context"
	"fmt"

	"github.com/weatherjean/shell3/internal/agentsetup"
	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/cron"
	"github.com/weatherjean/shell3/internal/shell3"
)

// Shared wiring for the hosted front-ends (telegram, ask). Each helper is small
// on purpose: the commands stay readable top-to-bottom while the invariants
// (config-dir anchoring, cron fail-fast) live in exactly one place.

// openRuntime resolves the --config value and builds a Runtime anchored to
// the config directory — the runtime root determines where runs/ + history
// live (runs.Open under <workdir>/.shell3_project), so tying it to the config
// dir keeps a hosted agent self-contained.
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
// owns Stop. store restores each job's run history
// (Runs/Failures/LastRun/...) so a restart doesn't erase it; log is where a
// failed SaveStatus is reported (never fatal to a run). Both are wired before
// Start(): a tick landing before SetLogger would just log nowhere until then,
// which is cosmetic, but wiring everything before Start keeps one invariant
// instead of two.
func armCron(disp cron.Dispatcher, store cron.RunStore, log applog.Logger, jobs []shell3.CronJob) (*cron.Scheduler, error) {
	if len(jobs) == 0 {
		return nil, nil
	}
	sched, err := cron.NewWithStore(disp, store, jobs)
	if err != nil {
		return nil, err
	}
	sched.SetLogger(log)
	sched.Start()
	fmt.Printf("cron: %d job(s) scheduled\n", len(jobs))
	return sched, nil
}

// partsRef resolves the current Parts generation PER CALL, never capturing a
// snapshot: /reload swaps *shell3.Runtime's Parts wholesale (see
// Runtime.Reload), so anything held from before it — the kit, the store
// handle, the log file — belongs to a generation whose teardown has since run.
// The cron adapters below all embed it.
type partsRef struct {
	parts func() *agentsetup.Parts
}

// storeRunStore adapts the runs store to cron.RunStore.
type storeRunStore struct {
	partsRef
}

func (s storeRunStore) LoadStatus() (map[string]cron.JobStatus, error) {
	st := s.parts().Store()
	if st == nil {
		return nil, nil
	}
	return cron.StoreRunStore{Store: st}.LoadStatus()
}

func (s storeRunStore) SaveStatus(status cron.JobStatus) error {
	st := s.parts().Store()
	if st == nil {
		return nil
	}
	return cron.StoreRunStore{Store: st}.SaveStatus(status)
}

// partsLogger adapts the app log to applog.Logger.
type partsLogger struct {
	partsRef
}

func (l partsLogger) Debug(msg string, fields ...any) { l.parts().Log().Debug(msg, fields...) }
func (l partsLogger) Info(msg string, fields ...any)  { l.parts().Log().Info(msg, fields...) }
func (l partsLogger) Warn(msg string, fields ...any)  { l.parts().Log().Warn(msg, fields...) }
func (l partsLogger) Error(msg string, err error, fields ...any) {
	l.parts().Log().Error(msg, err, fields...)
}
