//go:build unix

package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/weatherjean/shell3/internal/cron"
	"github.com/weatherjean/shell3/internal/heartbeat"
	"github.com/weatherjean/shell3/internal/media"
	"github.com/weatherjean/shell3/internal/paths"
	"github.com/weatherjean/shell3/internal/shell3"
	"github.com/weatherjean/shell3/internal/telegram"
	"github.com/weatherjean/shell3/internal/web"
)

func newTelegramCommand() *cobra.Command {
	var configDir string
	cmd := &cobra.Command{
		Use:   "telegram",
		Short: "Run the personal Telegram bot front-end",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			rt, resolved, err := openRuntime(ctx, configDir)
			if err != nil {
				return err
			}
			defer rt.Close()
			// resolved IS the config directory (~/.shell3): workdir default,
			// file-explorer root, and tunnel.log all anchor here.
			tgHome := resolved

			tg := rt.Telegram()
			if tg.Token == "" || tg.ChatID == "" {
				return fmt.Errorf("no telegram config: add a telegram: block (token, chat_id) to shell3.yaml")
			}
			chatID, err := strconv.ParseInt(tg.ChatID, 10, 64)
			if err != nil {
				return fmt.Errorf("telegram chat_id %q is not a number: %w", tg.ChatID, err)
			}

			// The Telegram bot runs THE configured agent (it spawns subagents but
			// does not switch agents). WorkDir roots its tools, defaulting to the
			// runtime root when unset.
			//
			// b is declared before the session so the tool-call hook Asker closure can
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

			// Scheduled jobs (top-level shell3.cron list): arm a scheduler on the
			// main session.
			sched, err := armCron(sess, rt.Cron())
			if err != nil {
				return err
			}
			// Deferred as a closure over the mutable handle: /reload swaps sched
			// for a fresh scheduler, and it is the CURRENT one that must stop at
			// shutdown. LIFO: stops before the earlier `defer rt.Close()`.
			defer func() {
				if sched != nil {
					sched.Stop()
				}
			}()

			client, err := telegram.NewBotAPIClient(ctx, tg.Token, rt.Parts().Log())
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

			// Media (STT/describe/TTS/imagegen): built from the runtime's current
			// config and wired at boot; the reload closure below rebuilds it from
			// the freshly reloaded config and re-wires it. The session decorator
			// registers image_generate on EVERY session — the main one (already
			// live: SetSessionDecorator applies to existing sessions) and each
			// subagent child at spawn — and Runtime.Reload re-applies it, so no
			// separate image-tool resync is needed.
			voiceModeStore, err := newVoiceModeStore()
			if err != nil {
				return err
			}
			b.SetMedia(buildMediaClients(rt), voiceModeStore)
			rt.SetSessionDecorator(func(s *shell3.Session) {
				_ = media.RegisterImageTool(s, buildMediaClients(rt))
			})

			// Wire /run <job> to the scheduler's manual fire (no-op if no cron).
			if sched != nil {
				b.SetJobRunner(sched.Run)
			}

			// Heartbeat (shell3.heartbeat{}): periodic check-in turns on the main
			// session. Each idle in-window tick Interjects the checklist prompt;
			// the idle-wake runs the turn and the bot suppresses HEARTBEAT_OK
			// replies, so the chat only hears about real alerts.
			hbInject := func(p string) { sess.Interject(p) }
			hbTick := rearmHeartbeat(nil, rt.HeartbeatConfig(), hbInject, b.Busy)
			if hbTick != nil {
				fmt.Printf("heartbeat: every %s\n", rt.HeartbeatConfig().Every)
			}
			defer func() {
				if hbTick != nil {
					hbTick.Stop()
				}
			}()

			// Register the "/" command hints (best-effort).
			if err := client.SetCommands(ctx, telegram.BotCommands()); err != nil {
				fmt.Printf("warning: could not set commands: %v\n", err)
			}

			srv := serveDashboard(ctx, rt, b, sess, sched, tg, chatID, tgHome)

			// /reload + reload tool: rebuild config, re-decorate the session, swap
			// the cron scheduler. Coordination lives in reloadAndRearm (testable);
			// the closure threads the mutable scheduler handle across reloads.
			b.SetReloader(func() (shell3.ReloadResult, error) {
				var dash cronDashboard
				if srv != nil { // avoid a non-nil interface wrapping a nil *web.Server
					dash = srv
				}
				resyncMedia := func() { b.SetMedia(buildMediaClients(rt), voiceModeStore) }
				ns, res, err := reloadAndRearm(rt, b, dash, sess, sched, resyncMedia)
				sched = ns
				if err == nil {
					hbTick = rearmHeartbeat(hbTick, rt.HeartbeatConfig(), hbInject, b.Busy)
				}
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
	addConfigFlag(cmd, &configDir)
	return cmd
}

// buildMediaClients resolves rt's current shell3.stt/tts/describe/imagegen
// blocks into a fresh media.Clients, spawning each capability's run_proxy (at
// most once, on first use) via the runtime's shared proxy Spawner. Called at
// boot and again on every reload (the config may have changed which media
// blocks are declared, or their models).
func buildMediaClients(rt *shell3.Runtime) *media.Clients {
	p := rt.Parts()
	return media.New(p.MediaConfig(), p.EnsureProxy)
}

// newVoiceModeStore opens the per-chat inbound-voice-reply mode file at
// ~/.shell3/voice_mode.json (the global root, independent of which config or
// workdir this process was started with).
func newVoiceModeStore() (*media.ModeStore, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home directory: %w", err)
	}
	return &media.ModeStore{Path: filepath.Join(paths.NewGlobal(home).Root, "voice_mode.json")}, nil
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
// resyncMedia, when non-nil, is called after a successful Reload: it rebuilds
// the host's media.Clients from the freshly reloaded config and re-wires them
// (SetMedia for the Telegram bot's STT/TTS/describe; the web driver has no
// media wiring of its own and passes nil). The image_generate host tool needs
// no resync here — Runtime.Reload re-applies the session decorator that
// registers it.
func reloadAndRearm(r configReloader, b rearmBot, dash cronDashboard, disp cron.Dispatcher, old *cron.Scheduler, resyncMedia func()) (*cron.Scheduler, shell3.ReloadResult, error) {
	res, err := r.Reload()
	if err != nil {
		return old, res, err
	}
	if resyncMedia != nil {
		resyncMedia()
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

// rearmHeartbeat stops the old ticker (nil-safe) and arms a fresh one from
// cfg; a nil cfg disarms and returns nil. Shared by startup (old = nil) and
// the reload path, so a reload picks up changed heartbeat settings and a
// removed block stops the ticking.
func rearmHeartbeat(old *heartbeat.Ticker, cfg *shell3.Heartbeat, inject func(string), busy func() bool) *heartbeat.Ticker {
	if old != nil {
		old.Stop()
	}
	if cfg == nil {
		return nil
	}
	t := heartbeat.NewTicker(*cfg, inject, busy)
	t.Start()
	return t
}

// serveDashboard builds and serves the Mini App dashboard when it is enabled
// in config, wiring the usage recorder, config-dir file explorer, and cron
// source. Returns nil when the dashboard is disabled. Serving is best-effort:
// listen failures are printed, not fatal.
func serveDashboard(ctx context.Context, rt *shell3.Runtime, b *telegram.Bot, sess *shell3.Session, sched *cron.Scheduler, tg shell3.TelegramConfig, chatID int64, configDir string) *web.Server {
	if !tg.Dashboard.Enabled || tg.Dashboard.Addr == "" {
		return nil
	}
	srv, usage := buildDashboard(rt, sess, web.TelegramAuth(tg.Token, chatID), configDir, sched, true)
	b.SetUsageRecorder(usage.Set)
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
	announcePublicURL(dash.URL, dash.Tunnel, dash.Addr, tgHome, func(url string) {
		if err := client.SetMenuButton(ctx, "dash", url); err != nil {
			fmt.Printf("warning: could not set menu button: %v\n", err)
			return
		}
		fmt.Printf("dashboard menu button → %s\n", url)
	})
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
