//go:build unix

package main

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/config"
	"github.com/weatherjean/shell3/internal/cron"
	"github.com/weatherjean/shell3/internal/paths"
	"github.com/weatherjean/shell3/internal/shell3"
	"github.com/weatherjean/shell3/internal/telegram"
)

// newTelegramCommand builds `shell3 telegram` — the personal bot front-end.
// One chat, one agent, ONE long-lived conversation: every inbound message
// continues it (a Telegram reply is a context hint, not a session switch, and
// /new is the only reset), cron fires from a hidden dispatch parent, and
// background completions come back as mail.
//
// --console swaps the Telegram transport for stdin/stdout, driving the same
// bot loop with no credentials and no network.
func newTelegramCommand() *cobra.Command {
	var configDir string
	var console bool
	cmd := &cobra.Command{
		Use:   "telegram",
		Short: "Run the personal Telegram bot front-end",
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

			// Console mode needs no Telegram credentials — it drives the same
			// bot loop over stdin/stdout with a fixed dummy chat id.
			var chatID int64
			if console {
				chatID = telegram.ConsoleChatID
			} else if chatID, err = telegramHomeChat(tg); err != nil {
				return err
			}

			// The agent's shell runs in telegram.workdir; the config dir is the
			// fallback, so a hosted agent stays self-contained.
			workDir := tg.WorkDir
			if workDir == "" {
				workDir = resolved
			}

			// One-conversation model: the bot holds ONE long-lived session and the
			// thread index records which one it is, so a restart resumes it. The
			// index lives for the whole process (kept across /reload); openThreads
			// runs the runs janitor first.
			threads := openThreads(rt, "telegram")

			// Transport: the real Telegram bot API, or (--console) a stdin/stdout
			// dev transport driving the same bot loop headlessly. apiClient stays
			// nil in console mode and the Telegram-only side effects are skipped.
			var apiClient *telegram.BotAPIClient
			var b *telegram.Bot
			if console {
				b = telegram.NewBot(
					telegram.NewConsoleClient(os.Stdin, os.Stdout, chatID), rt, chatID, threads)
			} else {
				if apiClient, err = telegram.NewBotAPIClient(ctx, tg.Token, rt.Parts().Log()); err != nil {
					return err
				}
				b = telegram.NewBot(apiClient, rt, chatID, threads)
			}
			// Authorization is per sender, not per chat (see senderAllowlist):
			// a bad allow_from entry fails startup rather than silently
			// widening or narrowing who can drive an unrestricted shell.
			if err := b.SetAllowFrom(tg.AllowFrom); err != nil {
				return err
			}
			b.SetMaxConcurrentTurns(tg.MaxConcurrentTurns)
			// Transport-independent wiring (media, decorator, completion host,
			// cron parent + scheduler, /reload) — shared by the Bot API and
			// --console transports.
			// LIFO: the scheduler-stop cleanup runs before the earlier
			// `defer rt.Close()`.
			stopSched, err := wireHost(b, rt, workDir)
			if err != nil {
				return err
			}
			defer stopSched()

			if console {
				fmt.Println("shell3 telegram --console: reading events from stdin " +
					"(a line continues the conversation, \"@<id> text\" quotes a message, \"/…\" = command, EOF quits)")
			} else {
				// Register the "/" command hints (best-effort).
				if err := apiClient.SetCommands(ctx, b.BotCommands()); err != nil {
					fmt.Printf("warning: could not set commands: %v\n", err)
				}
				// Greet the chat (best-effort).
				banner := "๑ï shell3 online — your personal agent, at your pace\n\n" +
					"Here, just type — every message continues this chat's conversation, and a restart " +
					"picks it up where we left off. In a group, @mention me or reply to one of my " +
					"messages; each chat keeps its own conversation. /new starts a fresh one here, " +
					"/dash opens the dashboard, /stop halts the current turn (/superstop also kills " +
					"background jobs), /reload applies config changes."
				if _, err := apiClient.Send(ctx, chatID, banner); err != nil {
					fmt.Printf("warning: could not send the greeting: %v\n", err)
				}
				fmt.Printf("shell3 telegram: listening (home chat %d)\n  config: %s\n", chatID, resolved)
			}
			b.Run(ctx)
			return nil
		},
	}
	addConfigFlag(cmd, &configDir)
	cmd.Flags().BoolVar(&console, "console", false,
		"dev transport: run the bot loop over stdin/stdout instead of Telegram (headless event testing)")
	return cmd
}

// telegramHomeChat validates the `telegram:` block the way the front-end needs
// it and returns the HOME chat: where cron results and ownerless completions
// land. It is not an access rule — rooms are authorized by who speaks in them
// (allow_from), never by being listed here.
//
// Every failure names the field at fault: the block is almost always PRESENT
// (shell3 boot always writes one, blank fields included) and the user's actual
// problem is one empty field — telling them to add a block they are looking at
// is the wrong diagnosis. Shared with `shell3 health`, so the check and the
// message are the same in both places.
func telegramHomeChat(tg config.TelegramConfig) (int64, error) {
	switch {
	case !tg.Present:
		return 0, fmt.Errorf("no telegram: block in the kit's shell3: wiring — add one (token, chat_id), or run `shell3 boot` to write it")
	case tg.Token == "":
		return 0, fmt.Errorf("telegram.token is empty — put your @BotFather token in the .env beside shell3.sh")
	}
	if tg.ChatID == "" {
		// No home chat named: fall back to the first allowlisted person's DM.
		// A bot cannot open a DM the user has never written to, so this only
		// works once they have — `shell3 health` says so.
		if len(tg.AllowFrom) == 0 {
			return 0, fmt.Errorf("telegram.chat_id is empty and telegram.allow_from names nobody — " +
				"set chat_id to the chat cron results should land in (@userinfobot prints yours), " +
				"or list at least one allow_from user id")
		}
		id, err := parseChatID(tg.AllowFrom[0])
		if err != nil {
			return 0, fmt.Errorf("telegram.allow_from[0] %q is not a number", tg.AllowFrom[0])
		}
		return id, nil
	}
	id, err := parseChatID(tg.ChatID)
	if err != nil {
		return 0, fmt.Errorf("telegram.chat_id %q is not a number", tg.ChatID)
	}
	// A GROUP chat id is negative and can never equal a user id, so the
	// allowlist's "the chat owner" fallback resolves to nobody: the bot would
	// start, look healthy, and ignore every human being. Refuse instead of
	// letting that be discovered by silence.
	if id < 0 && len(tg.AllowFrom) == 0 {
		return 0, fmt.Errorf("telegram.chat_id %d is a group and telegram.allow_from is empty — "+
			"nobody would be allowed to drive the agent; list the user ids that may (@userinfobot prints yours)", id)
	}
	return id, nil
}

// parseChatID is the one definition of what a chat id IS — a base-10 int64,
// exactly what the Bot API takes. Both validators (boot's form/flag check and
// telegramChatID above) call it, wrapping the error in their own wording.
func parseChatID(s string) (int64, error) {
	return strconv.ParseInt(strings.TrimSpace(s), 10, 64)
}

// newQuietStore opens the /quiet toggle's file at ~/.shell3/quiet_mode.json.
func newQuietStore() (*telegram.QuietStore, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home directory: %w", err)
	}
	return &telegram.QuietStore{Path: filepath.Join(paths.NewGlobal(home).Root, "quiet_mode.json")}, nil
}

// configReloader and rearmBot are the narrow slices of *shell3.Runtime and
// *telegram.Bot that reloadAndRearm needs, keeping the reload-coordination
// logic unit-testable with fakes.
type configReloader interface {
	Reload() (shell3.ReloadResult, error)
	Cron() []shell3.CronJob
}

type rearmBot interface {
	SetJobRunner(func(name string) error)
	// PostCompletion, alongside SetJobRunner, lets reloadAndRearm wire the
	// new scheduler's tool-job post callback itself — see wireCronPost.
	PostCompletion(p shell3.CompletionPost) error
}

// reloadAndRearm performs a /reload: rebuild config, then stop the old cron
// scheduler and arm a fresh one from the reloaded jobs (rewiring the bot's
// /run handler). Returns the new scheduler (nil when no jobs), the reload
// result, and any error. On failure the old scheduler is left running and
// returned unchanged, so a bad config never tears down a working schedule.
//
// The bot's host tools (send_media_telegram, reload, status) need no
// explicit re-registration: they are installed via the session decorator,
// which Runtime.Reload re-applies to every live session. store and log carry
// through to the fresh scheduler exactly like the initial armCron call (see
// internal/cron/store.go, cmd/shell3/host.go's partsLogger) — a reload's
// scheduler restores run history from the same seam a fresh start does.
func reloadAndRearm(r configReloader, b rearmBot, disp cron.Dispatcher, tools cron.ToolRunner, store cron.RunStore, log applog.Logger, old *cron.Scheduler) (*cron.Scheduler, shell3.ReloadResult, error) {
	res, err := r.Reload()
	if err != nil {
		return old, res, err
	}
	jobs := r.Cron()
	if len(jobs) == 0 {
		if old != nil {
			old.Stop()
		}
		b.SetJobRunner(nil)
		return nil, res, nil
	}
	// Build (and thereby parse) the new scheduler BEFORE stopping the old one:
	// a malformed schedule surfaces only here, and must not tear down a working
	// schedule.
	ns, err := cron.NewWithStore(disp, tools, store, jobs)
	if err != nil {
		return old, res, err
	}
	ns.SetLogger(log)
	// Wire post BEFORE Start(): a scheduled tick or /run landing in the gap
	// between Start() and a later SetPost would find post nil and drop its
	// tool job's result silently — AGENTS.md promises a completion is never
	// lost.
	wireCronPost(ns, b)
	if old != nil {
		old.Stop()
	}
	ns.Start()
	b.SetJobRunner(ns.Run)
	return ns, res, nil
}
