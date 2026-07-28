//go:build unix

package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/weatherjean/shell3/internal/cron"
	"github.com/weatherjean/shell3/internal/media"
	"github.com/weatherjean/shell3/internal/shell3"
	"github.com/weatherjean/shell3/internal/tunnel"
)

// Shared wiring for the hosted front-ends (serve, ask). Each helper is small
// on purpose: the commands stay readable top-to-bottom while the invariants
// (config-dir anchoring, cron fail-fast, tunnel scraping) live in exactly one
// place.

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
//
// A tunnel-printed URL is probed until it actually serves before it is
// announced: a quick tunnel's fresh hostname can take a while to route at the
// edge (Cloudflare answers 530 until then), and announcing early hands the
// user a URL that opens as a blank error page. An explicit url is assumed
// stable and announced immediately.
func announcePublicURL(url, tunnelCmd, addr, home string, onURL func(url string, serving bool)) {
	switch {
	case url != "":
		onURL(url, true) // a fixed URL is the operator's promise; don't second-guess it
	case tunnelCmd != "" && addr != "":
		urls := tunnel.Start(tunnelCmd, addr, filepath.Join(home, "tunnel.log"))
		go func() {
			u, ok := <-urls
			if !ok {
				fmt.Println("warning: tunnel printed no https URL; staying local (see tunnel.log)")
				return
			}
			onURL(u, waitURLServes(u, 3*time.Minute))
		}()
	}
}

// waitURLServes polls u until it answers with any non-edge-error HTTP status
// (< 500) or the deadline passes; either way the caller proceeds — this only
// delays the announcement, it never blocks it forever. Status codes are enough:
// an auth challenge (401/404) still proves the tunnel routes to us. Returns
// whether the URL was actually observed serving, so announcements can be
// honest on timeout (the host's own DNS cache can lag the rest of the world).
func waitURLServes(u string, deadline time.Duration) bool {
	// Resolve through a public resolver: freshly minted tunnel hostnames stay
	// negative-cached in the OS resolver long after the rest of the world sees
	// them, which turned this poll into a guaranteed timeout on macOS.
	dialer := &net.Dialer{Resolver: &net.Resolver{PreferGo: true,
		Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "udp", "1.1.1.1:53")
		}}}
	client := &http.Client{Timeout: 5 * time.Second,
		Transport: &http.Transport{DialContext: dialer.DialContext}}
	end := time.Now().Add(deadline)
	for {
		resp, err := client.Get(u)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode < 500 {
				return true
			}
		}
		if time.Now().After(end) {
			fmt.Printf("warning: %s still not serving after %s; announcing anyway (see tunnel.log)\n", u, deadline)
			return false
		}
		time.Sleep(3 * time.Second)
	}
}

// sessionExistsUnder builds the sessionExists predicate webui.PruneThreadIndex
// needs: a session id is gone if runs.Sweep just removed it, or if its
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
