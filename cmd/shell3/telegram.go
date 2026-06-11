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

			cwd, _ := os.Getwd()
			home, _ := os.UserHomeDir()
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
			b := telegram.NewBot(client, rt, sess, chatID, tg.Dashboard.URL)
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
			// end-of-turn — see registerReloadTool).
			b.SetReloader(func() (shell3.ReloadResult, error) {
				res, err := rt.Reload()
				if err != nil {
					return res, err
				}
				b.RedecorateSession() // re-apply approver + host tools dropped by reload
				if sched != nil {
					sched.Stop()
				}
				sched = nil
				if jobs := rt.Cron(); len(jobs) > 0 {
					ns, nerr := cron.New(sess, jobs)
					if nerr != nil {
						return res, nerr
					}
					ns.Start()
					sched = ns
					b.SetJobRunner(sched.Run)
					if srv != nil {
						srv.SetCronSource(cronSource(sched))
					}
				} else {
					b.SetJobRunner(nil)
					if srv != nil {
						srv.SetCronSource(nil)
					}
				}
				return res, nil
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
