//go:build unix

package main

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/weatherjean/shell3/internal/cron"
	"github.com/weatherjean/shell3/internal/shell3"
	"github.com/weatherjean/shell3/internal/tunnel"
	"github.com/weatherjean/shell3/internal/web"
)

// Shared wiring for the hosted front-ends (telegram, web, dash). Each helper
// is small on purpose: the commands stay readable top-to-bottom while the
// invariants (config-dir anchoring, cron fail-fast, tunnel scraping, dashboard
// knob set) live in exactly one place.

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

// announcePublicURL delivers the host's public https URL to onURL: an
// explicit url wins; otherwise tunnelCmd is spawned detached ({addr} replaced
// by addr) and the first https URL it prints is delivered asynchronously.
// No-op when neither is configured; a tunnel that prints no URL warns and
// stays local.
func announcePublicURL(url, tunnelCmd, addr, home string, onURL func(string)) {
	switch {
	case url != "":
		onURL(url)
	case tunnelCmd != "" && addr != "":
		urls := tunnel.Start(tunnelCmd, addr, filepath.Join(home, "tunnel.log"))
		go func() {
			if u, ok := <-urls; ok {
				onURL(u)
			} else {
				fmt.Println("warning: tunnel printed no https URL; staying local (see tunnel.log)")
			}
		}()
	}
}

// buildDashboard assembles a dashboard server with the knob set every host
// shares: usage recording, the config-dir file explorer, the cron source, and
// the heartbeat status. hbArmed says whether this front-end actually ticks a
// declared heartbeat (only shell3 telegram does); the source closes over the
// runtime so a /reload's config swap shows up with no re-wiring. The caller
// adds host-specific extras (SetChat) and serves the handler.
func buildDashboard(rt *shell3.Runtime, sess *shell3.Session, auth web.AuthFunc, configDir string, sched *cron.Scheduler, hbArmed bool) (*web.Server, *web.UsageStore) {
	usage := web.NewUsageStore()
	srv := web.NewServer(rt, sess, auth)
	srv.SetUsage(usage)
	srv.SetConfigDir(configDir)
	if sched != nil {
		srv.SetCronSource(sched.Jobs)
	}
	srv.SetHeartbeatSource(func() *web.HeartbeatStatus {
		return web.HeartbeatFromConfig(rt.HeartbeatConfig(), hbArmed)
	})
	return srv, usage
}
