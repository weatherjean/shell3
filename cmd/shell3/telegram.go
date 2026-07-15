//go:build unix

package main

import (
	"context"
	"fmt"
	"net/http"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/weatherjean/shell3/internal/cron"
	"github.com/weatherjean/shell3/internal/shell3"
	"github.com/weatherjean/shell3/internal/telegram"
	"github.com/weatherjean/shell3/internal/telegram/web"
	"github.com/weatherjean/shell3/internal/tunnel"
)

func newTelegramCommand() *cobra.Command {
	var configPath string
	cmd := &cobra.Command{
		Use:   "telegram",
		Short: "Run the personal Telegram bot front-end",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			resolved, err := resolveConfig(configPath)
			if err != nil {
				return err
			}
			// Anchor the bot to its own config directory, NOT the launch cwd: the
			// runtime root determines where runs/ + history live (runs.Open under
			// <workdir>/.shell3_project). Tying it to the config dir keeps the bot
			// self-contained.
			tgHome := filepath.Dir(resolved)
			rt, err := shell3.NewRuntime(ctx, shell3.RuntimeSpec{ConfigPath: resolved, WorkDir: tgHome})
			if err != nil {
				return err
			}
			defer rt.Close()

			tg := rt.Telegram()
			if tg.Token == "" || tg.ChatID == "" {
				return fmt.Errorf("no telegram config: add shell3.telegram{ token=..., chat_id=... } to shell3.lua")
			}
			chatID, err := strconv.ParseInt(tg.ChatID, 10, 64)
			if err != nil {
				return fmt.Errorf("telegram chat_id %q is not a number: %w", tg.ChatID, err)
			}

			// The Telegram bot runs THE configured agent (it spawns subagents but
			// does not switch agents). WorkDir roots its tools, defaulting to the
			// runtime root when unset.
			//
			// b is declared before the session so the on_tool_call Asker closure can
			// capture it; it is assigned just below, before any turn runs (and the
			// Asker only fires mid-turn).
			var b *telegram.Bot
			sess, err := rt.Session(shell3.SessionOpts{
				Name: "telegram", WorkDir: tg.WorkDir, ResumeLatest: true,
				Asker: func(ctx context.Context, command, reason string) bool {
					return b.Ask(ctx, command, reason)
				},
			})
			if err != nil {
				return err
			}

			// Scheduled jobs (shell3.telegram cron list): arm a scheduler on the main session.
			var sched *cron.Scheduler
			if jobs := rt.Cron(); len(jobs) > 0 {
				sched, err = cron.New(sess, jobs)
				if err != nil {
					return err // fail-fast on a bad schedule
				}
				sched.Start()
				defer sched.Stop() // LIFO: stops before the earlier `defer rt.Close()`
				fmt.Printf("cron: %d job(s) scheduled\n", len(jobs))
			}

			client, err := telegram.NewBotAPIClient(ctx, tg.Token)
			if err != nil {
				return err
			}
			b = telegram.NewBot(client, rt, sess, chatID)
			// Resolve send_media_telegram relative paths against the agent's workdir.
			workDir := tg.WorkDir
			if workDir == "" {
				workDir = tgHome
			}
			b.SetWorkDir(workDir)

			// Wire /run <job> to the scheduler's manual fire (no-op if no cron).
			if sched != nil {
				b.SetJobRunner(sched.Run)
			}

			// Register the "/" command hints (best-effort).
			if err := client.SetCommands(ctx, telegram.BotCommands()); err != nil {
				fmt.Printf("warning: could not set commands: %v\n", err)
			}

			srv := serveDashboard(ctx, rt, b, sess, sched, tg, chatID, filepath.Dir(resolved))

			// /reload + reload tool: rebuild config, re-decorate the session, swap
			// the cron scheduler. Coordination lives in reloadAndRearm (testable);
			// the closure threads the mutable scheduler handle across reloads.
			b.SetReloader(func() (shell3.ReloadResult, error) {
				var dash cronDashboard
				if srv != nil { // avoid a non-nil interface wrapping a nil *web.Server
					dash = srv
				}
				ns, res, err := reloadAndRearm(rt, b, dash, sess, sched)
				sched = ns
				return res, err
			})

			wireMenuButton(ctx, client, tg.Dashboard, tgHome)

			// Install a persistent reply-keyboard bar above the input: one-tap
			// slash-command buttons that auto-send their command. Best-effort.
			{
				banner := fmt.Sprintf("shell3 online — session #%s", sess.ID())
				rows := [][]telegram.ReplyKey{{{Text: "/stop"}, {Text: "/reload"}, {Text: "/clear"}}}
				if err := client.ShowReplyKeyboard(ctx, chatID, banner, rows); err != nil {
					fmt.Printf("warning: could not set reply keyboard: %v\n", err)
				}
			}

			fmt.Printf("shell3 telegram: listening for chat %d\n", chatID)
			b.Run(ctx)
			return nil
		},
	}
	addConfigFlag(cmd, &configPath)
	return cmd
}

// configReloader, rearmBot, and cronDashboard are the narrow slices of
// *shell3.Runtime, *telegram.Bot, and *web.Server that reloadAndRearm needs,
// keeping the reload-coordination logic unit-testable with fakes.
type configReloader interface {
	Reload() (shell3.ReloadResult, error)
	Cron() []shell3.CronJob
}

type rearmBot interface {
	RedecorateSession()
	SetJobRunner(func(name string) error)
}

type cronDashboard interface {
	SetCronSource(func() []web.CronJob)
}

// reloadAndRearm performs a /reload: rebuild config, re-decorate the session's
// host tools, then stop the old cron scheduler and arm a fresh one from the
// reloaded jobs (rewiring the bot's /run handler and the dashboard's cron
// source). Returns the new scheduler (nil when no jobs), the reload result, and
// any error; dash may be nil when no dashboard runs. On failure the old
// scheduler is left running and returned unchanged, so a bad config never tears
// down a working schedule.
func reloadAndRearm(r configReloader, b rearmBot, dash cronDashboard, disp cron.Dispatcher, old *cron.Scheduler) (*cron.Scheduler, shell3.ReloadResult, error) {
	res, err := r.Reload()
	if err != nil {
		return old, res, err
	}
	b.RedecorateSession() // re-apply host tools dropped by reload
	jobs := r.Cron()
	if len(jobs) == 0 {
		if old != nil {
			old.Stop()
		}
		b.SetJobRunner(nil)
		if dash != nil {
			dash.SetCronSource(nil)
		}
		return nil, res, nil
	}
	// Build (and thereby parse) the new scheduler BEFORE stopping the old one:
	// validateCron doesn't parse cron expressions, so a malformed schedule
	// surfaces only here — and must not tear down a working schedule.
	ns, err := cron.New(disp, jobs)
	if err != nil {
		return old, res, err
	}
	if old != nil {
		old.Stop()
	}
	ns.Start()
	b.SetJobRunner(ns.Run)
	if dash != nil {
		dash.SetCronSource(ns.Jobs)
	}
	return ns, res, nil
}

// serveDashboard builds and serves the Mini App dashboard when it is enabled
// in config, wiring the usage recorder, config-dir file explorer, and cron
// source. Returns nil when the dashboard is disabled. Serving is best-effort:
// listen failures are printed, not fatal.
func serveDashboard(ctx context.Context, rt *shell3.Runtime, b *telegram.Bot, sess *shell3.Session, sched *cron.Scheduler, tg shell3.TelegramConfig, chatID int64, configDir string) *web.Server {
	if !tg.Dashboard.Enabled || tg.Dashboard.Addr == "" {
		return nil
	}
	usage := web.NewUsageStore()
	b.SetUsageRecorder(usage.Set)
	srv := web.NewServer(rt, sess, tg.Token, chatID)
	srv.SetUsage(usage)
	srv.SetConfigDir(configDir) // read-only file explorer rooted at the config folder
	if sched != nil {
		srv.SetCronSource(sched.Jobs)
	}
	go func() {
		// Surface listen failures (port already bound, bad addr): silence here
		// would leave the menu button and tunnel pointing at a dead port with
		// no diagnostic.
		if err := startDashboard(ctx, tg.Dashboard.Addr, srv.Handler()); err != nil {
			fmt.Printf("warning: dashboard failed: %v\n", err)
		}
	}()
	fmt.Printf("dashboard on %s (expose via: tailscale serve https / proxy %s)\n", tg.Dashboard.Addr, tg.Dashboard.Addr)
	return srv
}

// wireMenuButton points the Telegram menu button at the dashboard Mini App
// (best-effort). The menu button is the authenticated launcher: a Mini App
// opened from it receives signed initData and passes auth. A reply-keyboard
// web_app button gets no initData, so the bar carries only command buttons.
// An explicit dashboard.url wins; otherwise a configured dashboard.tunnel is
// spawned and its printed https URL used.
func wireMenuButton(ctx context.Context, client *telegram.BotAPIClient, dash shell3.DashboardConfig, tgHome string) {
	if !dash.Enabled {
		return
	}
	setMenu := func(url string) {
		if err := client.SetMenuButton(ctx, "dash", url); err != nil {
			fmt.Printf("warning: could not set menu button: %v\n", err)
			return
		}
		fmt.Printf("dashboard menu button → %s\n", url)
	}
	switch {
	case dash.URL != "":
		setMenu(dash.URL)
	case dash.Tunnel != "" && dash.Addr != "":
		urls := tunnel.Start(dash.Tunnel, dash.Addr, filepath.Join(tgHome, "tunnel.log"))
		go func() {
			if url, ok := <-urls; ok {
				setMenu(url)
			} else {
				fmt.Println("warning: tunnel printed no https URL; dashboard stays local (see tunnel.log)")
			}
		}()
	}
}

// startDashboard runs an HTTP server on addr with the given handler, and
// gracefully shuts it down when ctx is cancelled.
func startDashboard(ctx context.Context, addr string, h http.Handler) error {
	srv := &http.Server{Addr: addr, Handler: h}
	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		_ = srv.Shutdown(context.Background())
		return <-errCh
	}
}
