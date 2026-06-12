//go:build unix

package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/weatherjean/shell3/internal/agentsetup"
	"github.com/weatherjean/shell3/internal/cron"
	"github.com/weatherjean/shell3/internal/telegram"
	"github.com/weatherjean/shell3/internal/telegram/web"
	"github.com/weatherjean/shell3/pkg/shell3"
)

func newTelegramCommand() *cobra.Command {
	var configPath string
	cmd := &cobra.Command{
		Use:   "telegram",
		Short: "Run the personal Telegram bot front-end",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("get working directory: %w", err)
			}
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			resolved, err := agentsetup.ResolveTelegramConfigPath(configPath, cwd, home)
			if err != nil {
				return err
			}
			rt, err := shell3.NewRuntime(shell3.RuntimeSpec{ConfigPath: resolved, WorkDir: cwd})
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

			// The Telegram bot runs one fixed agent (it spawns subagents but does
			// not switch agents). Agent picks it; "" → first declared. WorkDir
			// roots its tools, defaulting to the runtime root when unset.
			sess, err := rt.Session(shell3.SessionOpts{Name: "telegram", Agent: tg.Agent, WorkDir: tg.WorkDir})
			if err != nil {
				return err
			}

			// Scheduled jobs (shell3.cron{}): arm a scheduler on the main session.
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
			b := telegram.NewBot(client, rt, sess, chatID)
			// Resolve send_media_telegram relative paths against the agent's workdir.
			workDir := tg.WorkDir
			if workDir == "" {
				workDir = cwd
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

			var srv *web.Server
			if tg.Dashboard.Enabled && tg.Dashboard.Addr != "" {
				usage := web.NewUsageStore()
				b.SetUsageRecorder(usage.Set)
				srv = web.NewServer(rt, sess, tg.Token, chatID)
				srv.SetUsage(usage)
				if sched != nil {
					srv.SetCronSource(cronSource(sched))
				}
				go func() {
					_ = startDashboard(ctx, tg.Dashboard.Addr, srv.Handler())
				}()
				fmt.Printf("dashboard on %s (expose via: tailscale serve https / proxy %s)\n", tg.Dashboard.Addr, tg.Dashboard.Addr)
			}

			// /reload + reload tool: rebuild config in place, re-decorate the
			// session, and swap the cron scheduler. Runs only when the session
			// is idle (commands handled between turns; the reload tool defers to
			// end-of-turn — see registerReloadTool). The coordination lives in
			// reloadAndRearm (testable); the closure just threads the mutable
			// scheduler handle across reloads.
			b.SetReloader(func() (shell3.ReloadResult, error) {
				var dash cronDashboard
				if srv != nil { // avoid a non-nil interface wrapping a nil *web.Server
					dash = srv
				}
				ns, res, err := reloadAndRearm(rt, b, dash, sess, sched)
				sched = ns
				return res, err
			})

			// If the dashboard has a public URL, set the bot's in-chat menu
			// button to open it as a Mini App (the bottom-left "Open App"
			// button). Best-effort: a failure here must not stop the bot.
			// The dashboard's authenticated launcher is the menu button (bottom-
			// left): a Mini App opened from the menu button receives signed
			// initData, so it passes the dashboard's auth. (A reply-keyboard
			// web_app button cannot — it gets no initData — so the bar carries
			// only command buttons; see below.)
			if tg.Dashboard.Enabled && tg.Dashboard.URL != "" {
				if err := client.SetMenuButton(ctx, "dash", tg.Dashboard.URL); err != nil {
					fmt.Printf("warning: could not set menu button: %v\n", err)
				}
			}

			// Install a persistent reply-keyboard bar above the input: one-tap
			// slash-command buttons that auto-send their command. Best-effort.
			{
				rows := [][]telegram.ReplyKey{{{Text: "/stop"}, {Text: "/reload"}, {Text: "/clear"}}}
				if err := client.ShowReplyKeyboard(ctx, chatID, "shell3 online — quick actions ready.", rows); err != nil {
					fmt.Printf("warning: could not set reply keyboard: %v\n", err)
				}
			}

			fmt.Printf("shell3 telegram: listening for chat %d\n", chatID)
			b.Run(ctx)
			return nil
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "", "path to shell3.lua")
	return cmd
}

// configReloader, rearmBot, and cronDashboard are the narrow slices of
// *shell3.Runtime, *telegram.Bot, and *web.Server that reloadAndRearm needs.
// Defining them here keeps the reload-coordination logic unit-testable with
// fakes instead of a live runtime, bot, and HTTP server.
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
// source). It returns the new scheduler (nil when the reloaded config has no
// jobs), the reload result, and any error. dash may be nil when no dashboard
// runs. On reload failure the old scheduler is left running and returned
// unchanged, so a bad config never tears down a working schedule.
func reloadAndRearm(r configReloader, b rearmBot, dash cronDashboard, disp cron.Dispatcher, old *cron.Scheduler) (*cron.Scheduler, shell3.ReloadResult, error) {
	res, err := r.Reload()
	if err != nil {
		return old, res, err
	}
	b.RedecorateSession() // re-apply host tools dropped by reload
	if old != nil {
		old.Stop()
	}
	jobs := r.Cron()
	if len(jobs) == 0 {
		b.SetJobRunner(nil)
		if dash != nil {
			dash.SetCronSource(nil)
		}
		return nil, res, nil
	}
	ns, err := cron.New(disp, jobs)
	if err != nil {
		return nil, res, err
	}
	ns.Start()
	b.SetJobRunner(ns.Run)
	if dash != nil {
		dash.SetCronSource(cronSource(ns))
	}
	return ns, res, nil
}

// cronSource adapts a scheduler to the dashboard's cron DTO provider.
func cronSource(sched *cron.Scheduler) func() []web.CronJob {
	return func() []web.CronJob {
		var out []web.CronJob
		for _, j := range sched.Jobs() {
			out = append(out, web.CronJob{
				Name: j.Name, Schedule: j.Schedule, Agent: j.Agent,
				Notify: j.Notify, LastRun: j.LastRun, LastSubID: j.LastSubID,
			})
		}
		return out
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
