//go:build unix

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/weatherjean/shell3/internal/cron"
	"github.com/weatherjean/shell3/internal/media"
	"github.com/weatherjean/shell3/internal/shell3"
)

// Shared wiring for the hosted front-ends (telegram, ask). Each helper is
// small on purpose: the commands stay readable top-to-bottom while the
// invariants (config-dir anchoring, cron fail-fast) live in exactly one place.

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

// sessionExistsUnder builds the sessionExists predicate the front-end's thread
// index pruning needs: a session id is gone if runs.Sweep just removed it, or if its
// runs/<id>/ dir isn't there for any other reason (deleted by hand, an older
// crash) — either way its thread entry is stale and gets dropped.
func sessionExistsUnder(runsRoot string, justRemoved []string) func(id string) bool {
	removed := make(map[string]bool, len(justRemoved))
	for _, id := range justRemoved {
		removed[id] = true
	}
	return func(id string) bool {
		if removed[id] {
			return false
		}
		_, err := os.Stat(filepath.Join(runsRoot, "runs", id))
		return err == nil
	}
}

// buildMediaClients resolves the four media capabilities (STT/TTS/describe/
// imagegen) from the runtime's current config, starting each model's run_proxy
// (at most once, on first use) via the runtime's shared proxy Spawner. Called
// at boot and again on every reload, since the config may have changed which
// media blocks are declared or which models they name.
func buildMediaClients(rt *shell3.Runtime) *media.Clients {
	p := rt.Parts()
	return media.New(p.MediaConfig(), p.EnsureProxy)
}
