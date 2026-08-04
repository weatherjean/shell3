//go:build unix

package main

import (
	"context"
	"fmt"
	"net"

	"github.com/weatherjean/shell3/internal/cron"
	"github.com/weatherjean/shell3/internal/media"
	"github.com/weatherjean/shell3/internal/shell3"
)

// Shared wiring for the hosted front-ends (serve, ask). Each helper is small
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

// announcePublicURL delivers the host's public https URL to onURL when one
// is configured. No-op otherwise — exposure beyond the fixed url is the
// operator's own concern (reverse proxy, tailscale, SSH port forwarding, ...).
func announcePublicURL(url string, onURL func(url string)) {
	if url != "" {
		onURL(url) // a fixed URL is the operator's promise; don't second-guess it
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

// isLoopbackBind reports whether addr (a host:port listen address) binds only
// the loopback interface. A bare port (":8765") or a wildcard ("0.0.0.0"/"::")
// faces the network. The interface authenticates, but plain http carries the
// password and session cookie in clear, so callers warn.
func isLoopbackBind(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "" {
		return false // ":port" listens on all interfaces
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
