//go:build unix

package main

import (
	"context"
	"fmt"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/weatherjean/shell3/internal/media"
	"github.com/weatherjean/shell3/internal/shell3"
	"github.com/weatherjean/shell3/internal/web"
)

// newWebCommand builds `shell3 web` — the standalone web front-end: the
// dashboard plus chat over plain HTTP, gated by a shared secret
// (shell3.web{ secret = ... }, sent as X-Auth-Token / ?key=). It is the
// Telegram-free fallback host: it resumes the latest stored session so a
// conversation started over Telegram continues in the browser. Run one
// front-end at a time — both `shell3 telegram` and `shell3 web` own the same
// runs store.
func newWebCommand() *cobra.Command {
	var configDir, addr string
	cmd := &cobra.Command{
		Use:   "web",
		Short: "Run the standalone web front-end (dashboard + chat, token auth)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			rt, resolved, err := openRuntime(ctx, configDir)
			if err != nil {
				return err
			}
			defer rt.Close()
			// resolved IS the config directory (~/.shell3): file-explorer
			// root and tunnel.log anchor here.
			home := resolved

			wc := rt.Web()
			if addr != "" {
				wc.Addr = addr
			}
			if wc.Addr == "" {
				return fmt.Errorf("no web config: add a web: block (addr, secret: env:SHELL3_WEB_SECRET) to shell3.yaml")
			}
			if wc.Secret == "" {
				return fmt.Errorf("shell3.web: secret is required (an empty secret must never mean \"no auth\") — set secret: env:SHELL3_WEB_SECRET in the web: block")
			}

			// Match the bot's session identity: the runs store keys the latest
			// session on (workdir, config), so borrowing the telegram workdir is
			// what makes "continue the bot's conversation in the browser" true.
			// With no telegram block it is "" and defaults to the runtime root.
			workDir := rt.Telegram().WorkDir

			// d is declared before the session so the Asker closure can capture
			// it; it is assigned just below, before any turn runs.
			var d *web.Driver
			sess, err := rt.Session(shell3.SessionOpts{
				Name: "web", WorkDir: workDir, ResumeLatest: true,
				Asker: func(ctx context.Context, command, reason string) bool {
					return d.Ask(ctx, command, reason)
				},
			})
			if err != nil {
				return err
			}
			d = web.NewDriver(ctx, rt, sess)

			// Register the image_generate host tool on every session — the main
			// one just created and each subagent child at spawn (a no-op when no
			// shell3.imagegen{} is declared, per media.RegisterImageTool);
			// Runtime.Reload re-applies it. Web has no TTS/STT surface of its own
			// (no send_media_telegram / voice replies), so unlike the bot there
			// is no SetMedia equivalent to wire.
			rt.SetSessionDecorator(func(s *shell3.Session) {
				_ = media.RegisterImageTool(s, buildMediaClients(rt))
			})

			// Scheduled jobs keep running while web is the active host; a
			// declared heartbeat does not (it is a Telegram-notification
			// feature), which deserves a line rather than silence.
			sched, err := armCron(sess, rt.Cron())
			if err != nil {
				return err
			}
			// Deferred as a closure over the mutable handle: /reload swaps sched
			// for a fresh scheduler (and can arm one when boot had none), and it
			// is the CURRENT one that must stop before `defer rt.Close()` runs.
			defer func() {
				if sched != nil {
					sched.Stop()
				}
			}()
			if rt.HeartbeatConfig() != nil {
				fmt.Println("note: shell3.heartbeat{} is inert under shell3 web (heartbeat check-ins run under shell3 telegram only)")
			}

			srv, usage := buildDashboard(rt, sess, web.TokenAuth(wc.Secret), home, sched, false)
			srv.SetChat(d)

			// /run + /reload: same coordinator as the bot (reloadAndRearm),
			// minus host tools — the web driver decorates none, so the rearm
			// adapter's redecorate is a no-op. The closure threads the mutable
			// scheduler handle across reloads; /reload holds the driver's turn
			// slot, so it never runs concurrently with itself.
			if sched != nil {
				d.SetJobRunner(sched.Run)
			}
			d.SetReloader(func() (shell3.ReloadResult, error) {
				// No media resync needed: Runtime.Reload re-applies the session
				// decorator, which re-registers image_generate with fresh clients.
				ns, res, err := reloadAndRearm(rt, webRearm{d}, srv, sess, sched, nil)
				sched = ns
				return res, err
			})
			// Wire the recorder BEFORE the wake loop starts: a resumed session
			// with a queued notice can run a turn immediately.
			d.SetUsageRecorder(usage.Set)
			go d.Run(ctx)

			// Optional public exposure — same mechanics as the Telegram
			// dashboard. URLs are printed ready-to-open, ?key= included: this
			// is the operator's own terminal, and the page swaps the key into
			// localStorage (and out of the address bar) on first load.
			announcePublicURL(wc.URL, wc.Tunnel, wc.Addr, home, func(u string) {
				fmt.Printf("web: public URL %s/?key=%s\n", u, wc.Secret)
			})

			fmt.Printf("shell3 web: chat + dashboard on http://%s/?key=%s (session #%s)\n", wc.Addr, wc.Secret, sess.ID())
			return startDashboard(ctx, wc.Addr, srv.Handler())
		},
	}
	addConfigFlag(cmd, &configDir)
	cmd.Flags().StringVar(&addr, "addr", "", "Listen address (overrides shell3.web.addr)")
	return cmd
}

// webRearm adapts the web driver to reloadAndRearm's rearmBot contract: the
// web host decorates no host tools, so redecoration is a no-op.
type webRearm struct{ d *web.Driver }

func (w webRearm) RedecorateSession()                      {}
func (w webRearm) SetJobRunner(fn func(name string) error) { w.d.SetJobRunner(fn) }
