//go:build unix

package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/inbox"
	"github.com/weatherjean/shell3/internal/lispconfig"
	"github.com/weatherjean/shell3/internal/orchestrator"
	"github.com/weatherjean/shell3/internal/paths"
	"github.com/weatherjean/shell3/internal/runs"
	scheduler "github.com/weatherjean/shell3/internal/schedule"
	"github.com/weatherjean/shell3/internal/shell3"
	"github.com/weatherjean/shell3/internal/telegram"
	"github.com/weatherjean/shell3/internal/wrk"
)

// newTelegramCommand attaches Telegram as remote control for the same Lisp
// orchestrator used by chat. --console runs that exact bot contract locally.
func newTelegramCommand() *cobra.Command {
	var configPath, workDir string
	var console, here bool
	cmd := &cobra.Command{
		Use:   "telegram",
		Short: "Attach Telegram remote control to the orchestrator",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			restartRequested := false
			var feedback *hostFeedback
			// Registered before every resource defer, so a requested re-exec is
			// the final lifecycle step: replies, markers, schedules, sessions and
			// logs all close first. Exec keeps restart independent of launchd or
			// systemd while preserving the inherited credential environment.
			defer func() {
				if !restartRequested {
					return
				}
				executable, err := os.Executable()
				if err == nil {
					err = feedback.executing()
				}
				if err == nil {
					err = syscall.Exec(executable, os.Args, append(os.Environ(), restartReceiptEnv+"="+feedback.instance))
				}
				if err != nil {
					if feedback != nil {
						_ = feedback.record("restart", "", err)
					}
					fmt.Fprintf(cmd.ErrOrStderr(), "shell3: deferred restart failed: %v\n", err)
				}
			}()
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()

			resolved, err := resolveRuntimePaths(cmd, configPath, workDir, here)
			if err != nil {
				return err
			}
			configPath, workDir = resolved.config, resolved.workDir
			cfg, err := lispconfig.Load(configPath)
			if err != nil {
				return err
			}
			if cfg.Telegram == nil {
				return fmt.Errorf("%s: missing telegram form", configPath)
			}
			rt, err := orchestrator.OpenTelegram(ctx, configPath, workDir, cfg)
			if err != nil {
				return err
			}
			defer rt.Close()
			sessions := telegram.NewSessionIndex(func() *runs.Store { return rt.Store() }, "telegram")

			var apiClient *telegram.BotAPIClient
			var bot *telegram.Bot
			if console {
				bot = telegram.NewBot(telegram.NewConsoleClient(cmd.InOrStdin(), cmd.OutOrStdout(), telegram.ConsoleChatID), rt, telegram.ConsoleChatID, sessions)
			} else {
				token, err := lispconfig.ResolveSecret(cfg.Telegram.TokenEnv)
				if err != nil {
					return fmt.Errorf("telegram: %w", err)
				}
				apiClient, err = telegram.NewBotAPIClient(ctx, token, rt.Logger())
				if err != nil {
					return err
				}
				bot = telegram.NewBot(apiClient, rt, fmt.Sprint(cfg.Telegram.HomeChat), sessions)
			}
			bot.SetWorkDir(workDir)
			bot.SetLogger(rt.Logger())
			bot.SetConfigDir(filepath.Dir(configPath))
			bot.SetMaxConcurrentTurns(cfg.Telegram.MaxConcurrentTurns)
			bot.SetAnswerAllGroupMessages(cfg.Telegram.GroupMessages == "all")
			if !console {
				if err := bot.SetAllowFrom(cfg.Telegram.AllowFrom); err != nil {
					return err
				}
			}
			control := &telegramHostController{
				configPath: configPath, workDir: workDir, rt: rt, bot: bot,
				current: cfg, currentLoaded: time.Now().UTC(),
				restartEnabled: !console, applyAllowFrom: !console,
			}
			bot.SetHostControl(telegram.HostControl{
				Status: control.status, Validate: control.validate,
				Reload: control.reload, PrepareRestart: control.prepareRestart,
				Context: func() string { return feedback.context() },
				Record:  func(action, output string, err error) error { return feedback.record(action, output, err) },
			})
			bot.SetReload(control.reloadCommand)
			rt.SetSessionDecorator(func(sess *shell3.Session) {
				if !sess.Headless() {
					bot.DecorateOrchestratorSession(sess)
				}
			})
			mailbox := inbox.Store{Root: paths.NewLocal(workDir).Root}
			listener, err := inbox.StartListener(ctx, mailbox)
			if err != nil {
				return err
			}
			defer listener.Close()
			routerHints := make(chan string, 64)
			mainHints := make(chan struct{}, 1)
			go splitInboxHints(ctx, listener.Hints(), routerHints, mainHints)
			router, err := wrk.StartRouter(ctx, mailbox.Root, routerHints, rt.Logger())
			if err != nil {
				return err
			}
			defer router.Close()
			var scheduleManager *scheduler.Manager
			if !console {
				scheduleManager, err = scheduler.Start(ctx, configPath, workDir, cfg, rt.Store(), rt.Logger())
				if err != nil {
					return err
				}
				defer scheduleManager.Close()
			}

			_ = rt.RecoverBackgroundJobs()
			if !console {
				if err := apiClient.SetCommands(ctx, bot.BotCommands()); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not set Telegram commands: %v\n", err)
				}
				defer func() {
					shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer shutdownCancel()
					if err := bot.NotifyLifecycle(shutdownCtx, telegram.ShutdownNotice); err != nil {
						fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not send Telegram shutdown notice: %v\n", err)
					}
				}()
				fmt.Fprintf(cmd.OutOrStdout(), "shell3 telegram: remote control attached (home chat %d)\n", cfg.Telegram.HomeChat)
			}
			// Initialize receipts only after configuration, transports and owners
			// are ready. A startup failure must not claim restart completion.
			feedback, err = openHostFeedback(filepath.Join(mailbox.Root, "host-actions.json"), os.Getenv(restartReceiptEnv))
			_ = os.Unsetenv(restartReceiptEnv)
			if err != nil {
				rt.Logger().Warn("host action receipts unavailable; continuing with in-memory feedback", "error", err)
			}
			go notifyTelegramInbox(ctx, bot, mailbox, mainHints, !console, 30*time.Second, rt.Logger())
			botDone := make(chan struct{})
			go func() {
				bot.Run(ctx)
				close(botDone)
			}()
			select {
			case <-ctx.Done():
				return nil
			case <-botDone:
				return nil
			case <-bot.RestartReady():
				rt.Logger().Info("deferred host restart ready", "event", "host.restart_ready")
				restartRequested = true
				return nil
			}
		},
	}
	addRuntimeFlags(cmd, &configPath, &workDir, &here)
	cmd.Flags().BoolVar(&console, "console", false, "Run the Telegram bot contract over stdin/stdout without credentials or network")
	return cmd
}

func validateTelegramReload(configPath string, current, fresh *lispconfig.Config) error {
	if fresh.Telegram == nil {
		return fmt.Errorf("%s: missing telegram form", configPath)
	}
	if reasons := telegramRestartReasons(current, fresh); len(reasons) > 0 {
		return &restartRequiredError{reasons: reasons}
	}
	return nil
}

func splitInboxHints(ctx context.Context, hints <-chan string, router chan<- string, main chan<- struct{}) {
	defer close(router)
	for {
		select {
		case <-ctx.Done():
			return
		case target, ok := <-hints:
			if !ok {
				return
			}
			if target == "main" {
				select {
				case main <- struct{}{}:
				default:
				}
				continue
			}
			select {
			case router <- target:
			default:
			}
		}
	}
}

func notifyTelegramInbox(ctx context.Context, bot *telegram.Bot, store inbox.Store, hints <-chan struct{}, announceStartup bool, reconcileEvery time.Duration, log applog.Logger) {
	if announceStartup {
		for {
			if err := bot.NotifyLifecycle(ctx, telegram.StartupNotice); err == nil {
				break
			} else {
				log.Warn("telegram startup notification failed", "error", err)
			}
			retry := time.NewTimer(30 * time.Second)
			select {
			case <-ctx.Done():
				if !retry.Stop() {
					<-retry.C
				}
				return
			case <-retry.C:
			}
		}
	}
	notify := func() bool {
		if err := bot.WakeInbox(ctx, store); err != nil {
			log.Warn("inbox dispatch failed", "error", err)
			return false
		}
		return true
	}
	retry := time.NewTimer(time.Hour)
	if !retry.Stop() {
		<-retry.C
	}
	defer retry.Stop()
	reconcile := time.NewTicker(reconcileEvery)
	defer reconcile.Stop()
	wake := make(chan struct{}, 1)
	wake <- struct{}{}
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-hints:
			if !ok {
				hints = nil
				continue
			}
			select {
			case wake <- struct{}{}:
			default:
			}
		case <-retry.C:
			select {
			case wake <- struct{}{}:
			default:
			}
		case <-reconcile.C:
			select {
			case wake <- struct{}{}:
			default:
			}
		case <-wake:
			if !notify() {
				retry.Reset(30 * time.Second)
			}
		}
	}
}
