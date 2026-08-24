//go:build unix

package main

import (
	"context"
	"fmt"

	"github.com/weatherjean/shell3/internal/agentsetup"
	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/cron"
	"github.com/weatherjean/shell3/internal/kit"
	"github.com/weatherjean/shell3/internal/shell3"
)

// Shared wiring for the hosted front-ends (telegram, ask). Each helper is small
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

// completionPoster is the narrow slice of shell3.CompletionHost wireCronPost
// needs — just enough to keep it callable with a fake in tests, rather than
// demanding a full CompletionHost (WakeOwner, StartFreshTurn) a cron post
// never uses.
type completionPoster interface {
	// The error mirrors shell3.CompletionHost: non-nil = the post never
	// reached the user. Cron tool-job posts currently discard it (they have
	// no outbox row to keep) — see wireCronPost.
	PostCompletion(p shell3.CompletionPost) error
}

// armCron builds and starts a scheduler for the declared jobs (nil when there
// are none). Fail-fast: a malformed schedule is a startup error. The caller
// owns Stop. post is wired BEFORE Start(): a fire landing between Start()
// and a later SetPost call would find s.post nil and drop its result
// silently — see wireCronPost. store restores each job's run history
// (Runs/Failures/LastRun/...) so a restart doesn't erase it; log is where a
// failed SaveStatus is reported (never fatal to a run). Both are wired before
// Start() for the same reason post is: a tick landing before SetLogger would
// just log nowhere until then, which is cosmetic, but wiring everything
// before Start keeps one invariant instead of two.
func armCron(disp cron.Dispatcher, tools cron.ToolRunner, store cron.RunStore, log applog.Logger, host completionPoster, jobs []shell3.CronJob) (*cron.Scheduler, error) {
	if len(jobs) == 0 {
		return nil, nil
	}
	sched, err := cron.NewWithStore(disp, tools, store, jobs)
	if err != nil {
		return nil, err
	}
	sched.SetLogger(log)
	wireCronPost(sched, host)
	sched.Start()
	fmt.Printf("cron: %d job(s) scheduled\n", len(jobs))
	return sched, nil
}

// kitTools adapts the loaded kit to cron.ToolRunner. parts is resolved PER
// CALL, not captured once: /reload swaps *shell3.Runtime's Parts generation
// wholesale (see Runtime.Reload), and a snapshot taken at armCron time would
// keep dispatching against the pre-reload kit forever after. A cron tool job
// runs in the config dir by default — it belongs to the install, not to any
// agent's workdir — but honours workdir: like a prompt job does.
type kitTools struct {
	parts func() *agentsetup.Parts
}

func (k kitTools) RunTool(ctx context.Context, name, workDir string, args map[string]any) (string, error) {
	p := k.parts()
	t, ok := p.KitToolByName(name)
	if !ok {
		return "", fmt.Errorf("cron: no kit tool named %q", name)
	}
	if workDir == "" {
		workDir = p.ConfigDir()
	}
	return kit.Runner{Path: p.KitPath(), Dir: workDir}.Run(ctx, t, args)
}

// storeRunStore adapts the runs store to cron.RunStore, resolving Parts
// fresh per call — like kitTools, a /reload swaps the whole Parts
// generation wholesale, and a store handle captured once would keep
// reading/writing a database Runtime.Reload has since closed.
type storeRunStore struct {
	parts func() *agentsetup.Parts
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

// partsLogger adapts the app log to applog.Logger, resolving Parts fresh per
// call — like kitTools and storeRunStore, a /reload swaps the whole Parts
// generation (and with it, the underlying log file handle) wholesale, so a
// logger captured once could end up writing to a handle the old generation's
// teardown has since closed.
type partsLogger struct {
	parts func() *agentsetup.Parts
}

func (l partsLogger) Debug(msg string, fields ...any) { l.parts().Log().Debug(msg, fields...) }
func (l partsLogger) Info(msg string, fields ...any)  { l.parts().Log().Info(msg, fields...) }
func (l partsLogger) Warn(msg string, fields ...any)  { l.parts().Log().Warn(msg, fields...) }
func (l partsLogger) Error(msg string, err error, fields ...any) {
	l.parts().Log().Error(msg, err, fields...)
}

// wireCronPost installs sched's post callback, the surface a tool job uses to
// deliver its own result or failure (no model turn dispatches on its
// behalf). It rides the same CompletionHost.PostCompletion every other
// background completion uses, so a tool job's post obeys /quiet identically
// — a straight passthrough, since fireTool already builds the
// shell3.CompletionPost (CronJob + Text) that PostCompletion expects. A nil
// scheduler (no cron jobs) is a no-op. MUST be called before sched.Start()
// (see armCron / reloadAndRearm) — a fire landing before this runs finds
// post nil and drops its result silently.
func wireCronPost(sched *cron.Scheduler, host completionPoster) {
	if sched == nil {
		return
	}
	// The scheduler's callback is void: a tool job's post has no outbox row
	// behind it (nothing dispatched), so a failed send is logged upstream and
	// the next tick re-posts anyway — idempotent by design.
	sched.SetPost(func(p shell3.CompletionPost) { _ = host.PostCompletion(p) })
}
