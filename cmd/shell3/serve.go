//go:build unix

package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/weatherjean/shell3/internal/runs"
	"github.com/weatherjean/shell3/internal/shell3"
	"github.com/weatherjean/shell3/internal/webui"
)

// Names of the two .env keys the interface authenticates with. Referenced from
// shell3.yaml as `web.password: env:SHELL3_WEB_PASSWORD`, so the secrets stay
// in .env like every other one.
const (
	envWebPassword = "SHELL3_WEB_PASSWORD"
	envWebTOTP     = "SHELL3_WEB_TOTP_SECRET"
)

// minPasswordLength is what boot enforces and serve warns below. Sixteen rather
// than the conventional eight because a login here is a shell, so the password
// has to survive a guessing budget from anyone who finds the URL.
const minPasswordLength = 16

// requireWebPassword refuses to serve an unauthenticated interface. This is a
// hard error, not a warning: reaching the port is arbitrary command execution
// on this machine, so there is no configuration where starting without a
// password is the helpful thing to do.
//
// The config LOAD deliberately still succeeds without one — `shell3 ask` serves
// nothing and must stay usable — which is why the check lives here.
func requireWebPassword(web shell3.WebConfig) error {
	if web.Password != "" {
		return nil
	}
	// One line: the error renderer collapses newlines into a paragraph, so the
	// step-by-step belongs in printWebPasswordHelp, not in here.
	return fmt.Errorf("web.password is not set, and the interface hands a shell to whoever reaches it"+
		" (set web.password: env:%s, and %s in .env)", envWebPassword, envWebPassword)
}

// printWebPasswordHelp spells out the fix, in a shape the error renderer cannot
// reflow into a single paragraph.
func printWebPasswordHelp(out io.Writer) {
	fmt.Fprintf(out, "\nThe web interface needs a password before it can be served.\n\n"+
		"  In shell3.yaml, under web:\n    password: env:%s\n\n"+
		"  In %s beside it:\n    %s=<at least %d characters>\n\n"+
		"Reaching this interface means reaching a shell — the agent runs bash on this\n"+
		"machine — so there is no unauthenticated mode to fall back to.\n\n",
		envWebPassword, ".env", envWebPassword, minPasswordLength)
}

// weakPasswordWarning flags a password too short for what it guards. Not a
// refusal: an operator whose config already works should not be locked out of
// their own machine by an upgrade.
func weakPasswordWarning(web shell3.WebConfig) string {
	if web.Password == "" || len([]rune(web.Password)) >= minPasswordLength {
		return ""
	}
	return fmt.Sprintf("  warning: the web password is shorter than %d characters, and it is the only "+
		"thing between whoever finds this URL and a shell", minPasswordLength)
}

// cleartextWarning flags a network-facing bind with no TLS, where the password
// and the session cookie travel in clear. Exposure is the operator's, so the
// warning points at the deployment docs rather than prescribing one answer.
func cleartextWarning(addr string) string {
	if isLoopbackBind(addr) {
		return ""
	}
	return "  warning: this address faces the network over plain http — the password and " +
		"session cookie cross it in clear. Use https exposure or a TLS-terminating " +
		"proxy (docs/deploying.md)"
}

// newServeCommand builds `shell3 serve` — the web interface, and the only
// hosted front-end. It runs the agent, serves the app, and schedules cron.
//
// Bound to loopback by default. The interface authenticates (a password, plus
// TOTP when configured), but a login is a shell, so exposing it still argues
// for a proxy that authenticates in its own right.
func newServeCommand() *cobra.Command {
	var configDir, addr string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the agent and serve the web interface",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			rt, resolved, err := openRuntime(ctx, configDir)
			if err != nil {
				return err
			}
			defer rt.Close()

			// Before anything else: an interface with no password is a shell
			// on an open port.
			if err := requireWebPassword(rt.Web()); err != nil {
				printWebPasswordHelp(cmd.OutOrStdout())
				return err
			}

			workDir := rt.Web().WorkDir
			if workDir == "" {
				workDir = resolved
			}

			// Runs janitor: start-time only. Deletes sessions whose last
			// activity is past runs_keep_days (0 = keep forever) — rows, FTS
			// entries, job-log dirs — and drops thread-index rows (every
			// surface, "web" included) pointing at sessions that no longer
			// exist. Runs on its own connection: openRuntime has already
			// opened the runtime's store above, so this is a second connection
			// to the same database, not a handoff. Before the server listens,
			// so a browser never sees rows the sweep is about to remove.
			runsRoot := rt.Parts().RunsRoot()
			removedRuns, threadsDropped, err := runs.Sweep(runsRoot,
				time.Duration(rt.Parts().RunsKeepDays())*24*time.Hour, time.Now())
			if err != nil {
				// Fail-open: Sweep skipped whatever it couldn't remove and kept
				// going. Leftover run dirs are cosmetic, never worth refusing to
				// start over.
				fmt.Printf("warning: janitor: %v\n", err)
			}
			if len(removedRuns) > 0 || threadsDropped > 0 {
				fmt.Printf("janitor: removed %d runs, %d thread entries\n",
					len(removedRuns), threadsDropped)
			}

			srv, err := webui.New(webui.Options{
				Runtime:   rt,
				WorkDir:   workDir,
				ConfigDir: resolved,
				Version:   version,
			})
			if err != nil {
				return err
			}
			defer srv.Close()

			// Cron dispatches subagents, which need SOME parent session. One
			// hidden session is the dispatch parent; it runs no turns of its own
			// (the notifier owns delivery).
			cronSess, err := rt.Session(shell3.SessionOpts{
				Name: "cron", WorkDir: workDir, Headless: true,
			})
			if err != nil {
				return err
			}
			sched, err := armCron(cronSess, rt.Cron())
			if err != nil {
				return err
			}
			if sched != nil {
				defer sched.Stop()
				srv.SetCronSource(sched)
			}

			// The worker runs out-of-turn work: cron results and job completions
			// the notifier chose to wake on.
			go srv.Start(ctx)

			if addr == "" {
				addr = rt.Web().Addr
			}
			if addr == "" {
				addr = "127.0.0.1:8765"
			}
			fmt.Printf("shell3: http://%s\n  config: %s\n", addr, resolved)
			if warning := cleartextWarning(addr); warning != "" {
				fmt.Println(warning)
			}
			if warning := weakPasswordWarning(rt.Web()); warning != "" {
				fmt.Println(warning)
			}
			if rt.Web().TOTPSecret == "" {
				fmt.Printf("  note: password only. Set %s in .env for a second factor\n", envWebTOTP)
			}

			// A fixed web.url announces the interface's public address. A
			// public URL puts a shell on the internet behind one password, so
			// say what that means at the moment it becomes reachable.
			web := rt.Web()
			announcePublicURL(web.URL, func(url string, serving bool) {
				fmt.Printf("public URL: %s\n", url)
			})

			return startServer(ctx, addr, srv.Handler())
		},
	}
	addConfigFlag(cmd, &configDir)
	cmd.Flags().StringVar(&addr, "addr", "",
		"Listen address (default: web.addr, else 127.0.0.1:8765)")
	return cmd
}

// startServer serves h until ctx ends, then shuts down gracefully. Streaming
// endpoints hold connections open, so the drain window is short and the
// deferred cancel releases it either way.
func startServer(ctx context.Context, addr string, h http.Handler) error {
	srv := &http.Server{Addr: addr, Handler: h}
	errs := make(chan error, 1)
	go func() {
		err := srv.ListenAndServe()
		if err == http.ErrServerClosed {
			err = nil
		}
		errs <- err
	}()

	select {
	case err := <-errs:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}
