//go:build unix

package main

import (
	"fmt"
	"net"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/weatherjean/shell3/internal/shell3"
	"github.com/weatherjean/shell3/internal/web"
)

// newDashCommand builds `shell3 dash` — serve the Mini App dashboard locally
// with NO authentication, for development and troubleshooting. It builds a
// runtime from the same config the bot uses and reattaches to the latest
// session, so every dashboard endpoint (status, runs, jobs, cron, files,
// history) is browsable/curlable without Telegram initData. Bound to localhost
// only: with auth off it exposes history + files, so it must never face the
// network.
func newDashCommand() *cobra.Command {
	var configDir, addr string
	cmd := &cobra.Command{
		Use:   "dash",
		Short: "Serve the dashboard locally with NO auth (dev/troubleshooting)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			rt, resolved, err := openRuntime(ctx, configDir)
			if err != nil {
				return err
			}
			defer rt.Close()

			tg := rt.Telegram()
			// Reattach to the latest session so the dashboard shows the bot's real
			// history + runs (subagent child sessions included).
			sess, err := rt.Session(shell3.SessionOpts{Name: "dash", WorkDir: tg.WorkDir, ResumeLatest: true})
			if err != nil {
				return err
			}

			srv := web.NewServer(rt, sess, web.NoAuth())
			srv.SetConfigDir(filepath.Dir(resolved))
			srv.SetHeartbeatSource(func() *web.HeartbeatStatus {
				return web.HeartbeatFromConfig(rt.HeartbeatConfig(), false)
			})

			if addr == "" {
				addr = tg.Dashboard.Addr
			}
			if addr == "" {
				addr = "127.0.0.1:8765"
			}
			// dash serves with NO auth, so it must never face the network. The
			// bind address can come from the shared dashboard.addr (which the
			// tunneled Telegram dashboard may set to 0.0.0.0); refuse anything
			// that isn't loopback rather than silently exposing history + files.
			if !isLoopbackBind(addr) {
				return fmt.Errorf("shell3 dash is unauthenticated and localhost-only, "+
					"but %q is not a loopback address — pass --addr 127.0.0.1:PORT (the "+
					"authenticated `shell3 web`/`shell3 telegram` front-ends are the ones meant to face the network)", addr)
			}
			fmt.Printf("shell3 dash: serving the dashboard on http://%s  (NO AUTH — localhost only)\n", addr)
			fmt.Printf("  config: %s\n  API:    /api/{status,sessions,session,jobs,job,history,cron,files,file}\n", resolved)
			return startDashboard(ctx, addr, srv.Handler())
		},
	}
	addConfigFlag(cmd, &configDir)
	cmd.Flags().StringVar(&addr, "addr", "", "Listen address (default: the config's dashboard addr, else 127.0.0.1:8765)")
	return cmd
}

// isLoopbackBind reports whether addr (a host:port listen address) binds only
// the loopback interface. A bare-port (":8765"), a wildcard ("0.0.0.0"/"::"),
// or any routable IP binds beyond loopback and is rejected for the no-auth dash.
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
