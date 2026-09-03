//go:build unix

package main

import (
	"context"
	"errors"
	"fmt"
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
			rt, err := orchestrator.OpenTelegram(ctx, configPath, workDir)
			if err != nil {
				return err
			}
			defer rt.Close()
			threads := telegram.NewThreadIndex(func() *runs.Store { return rt.Store() }, "telegram")

			var apiClient *telegram.BotAPIClient
			var bot *telegram.Bot
			if console {
				bot = telegram.NewBot(telegram.NewConsoleClient(cmd.InOrStdin(), cmd.OutOrStdout(), telegram.ConsoleChatID), rt, telegram.ConsoleChatID, threads)
			} else {
				token, err := lispconfig.ResolveSecret(configPath, cfg.Telegram.TokenEnv)
				if err != nil {
					return fmt.Errorf("telegram: %w", err)
				}
				apiClient, err = telegram.NewBotAPIClient(ctx, token, rt.Logger())
				if err != nil {
					return err
				}
				bot = telegram.NewBot(apiClient, rt, cfg.Telegram.HomeChat, threads)
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
			bot.SetReload(func() error {
				fresh, err := lispconfig.Load(configPath)
				if err != nil {
					return err
				}
				if err := validateTelegramReload(configPath, cfg, fresh); err != nil {
					return err
				}
				if _, err := orchestrator.Reload(rt, configPath, workDir, true); err != nil {
					return err
				}
				bot.SetMaxConcurrentTurns(fresh.Telegram.MaxConcurrentTurns)
				bot.SetAnswerAllGroupMessages(fresh.Telegram.GroupMessages == "all")
				if !console {
					if err := bot.SetAllowFrom(fresh.Telegram.AllowFrom); err != nil {
						return err
					}
				}
				cfg = fresh
				return nil
			})
			rt.SetSessionDecorator(func(sess *shell3.Session) {
				if !sess.Headless() {
					bot.DecorateOrchestratorSession(sess)
				}
			})
			rt.SetCompletionHost(bot)

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

			go notifyTelegramInbox(ctx, bot, mailbox, mainHints, 30*time.Second, rt.Logger())
			_ = rt.RecoverCompletions()
			go redeliverTelegramCompletions(ctx, rt)
			if !console {
				if err := apiClient.SetCommands(ctx, bot.BotCommands()); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not set Telegram commands: %v\n", err)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "shell3 telegram: remote control attached (home chat %d)\n", cfg.Telegram.HomeChat)
			}
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
	if fresh.Telegram.TokenEnv != current.Telegram.TokenEnv || fresh.Telegram.HomeChat != current.Telegram.HomeChat {
		return fmt.Errorf("telegram token-env or home-chat changed; restart the adapter")
	}
	if !scheduler.SameDeclarations(fresh.Schedules, current.Schedules) {
		return fmt.Errorf("schedule declarations changed; restart the persistent adapter")
	}
	return nil
}

func redeliverTelegramCompletions(ctx context.Context, rt *shell3.Runtime) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			rt.RedeliverUndelivered()
		}
	}
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

func notifyTelegramInbox(ctx context.Context, bot *telegram.Bot, store inbox.Store, hints <-chan struct{}, reconcileEvery time.Duration, log applog.Logger) {
	last := ""
	notify := func() bool {
		_, count, err := store.List("main", inbox.StatusPending, 0, 1)
		if err != nil {
			log.Warn("inbox status failed", "error", err)
			return false
		}
		if count == 0 {
			last = ""
			return true
		}
		latest, _, err := store.List("main", inbox.StatusPending, count-1, 1)
		if err != nil || len(latest) != 1 {
			if err == nil {
				err = errors.New("pending inbox changed while counting")
			}
			log.Warn("inbox status failed", "error", err)
			return false
		}
		signature := fmt.Sprintf("%d/%s", count, latest[0].Message.ID)
		if signature == last {
			return true
		}
		if err := bot.NotifyInbox(ctx, count); err != nil {
			log.Warn("telegram inbox notification failed", "pending", count, "error", err)
			return false
		}
		last = signature
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
