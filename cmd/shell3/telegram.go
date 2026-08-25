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

// newTelegramCommand builds `shell3 telegram`, the bot front-end: one
// long-lived conversation per chat that every message continues (a reply is a
// context hint, /new the only reset), cron firing from a hidden dispatch
// parent, and completions coming back as mail.
//
// --console swaps the transport for stdin/stdout, driving the same bot loop
// with no credentials and no network.
func newTelegramCommand() *cobra.Command {
	var configDir string
	var console bool
	var convoLog bool
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

			// Console mode needs no credentials: the same loop over
			// stdin/stdout with a fixed dummy chat id.
			var chatID int64
			if console {
				chatID = telegram.ConsoleChatID
			} else if chatID, err = telegramHomeChat(tg); err != nil {
				return err
			}

			// The shell runs in telegram.workdir, falling back to the config
			// dir so a hosted agent stays self-contained.
			workDir := tg.WorkDir
			if workDir == "" {
				workDir = resolved
			}

			// The thread index records which session each room is holding, so a
			// restart resumes them. It lives for the whole process, across
			// /reload; openThreads runs the janitors first.
			threads := openThreads(rt, "telegram")

			// The real Bot API, or --console's stdin/stdout transport driving
			// the same loop. apiClient stays nil in console mode, and the
			// Telegram-only side effects are skipped.
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
			// Authorization is per sender, not per chat, so a bad allow_from
			// entry fails startup rather than silently widening or narrowing
			// who can drive an unrestricted shell.
			if err := b.SetAllowFrom(tg.AllowFrom); err != nil {
				return err
			}
			b.SetMaxConcurrentTurns(tg.MaxConcurrentTurns)
			// The wire record, opt-in. Wrap the transport BEFORE Run so the
			// first update is already covered.
			if convoLog {
				f, err := openConvoLog(resolved)
				if err != nil {
					return err
				}
				defer f.Close()
				b.SetConvoLog(f)
				fmt.Printf("conversation log: %s\n", f.Name())
			}
			// Transport-independent wiring, shared by both transports. LIFO:
			// the scheduler-stop cleanup runs before the earlier rt.Close().
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
	cmd.Flags().BoolVar(&convoLog, "convo-log", false,
		"record every message in and out to <config>/"+convoLogName+" as JSONL (off by default: it logs every room the bot can see)")
	return cmd
}

// convoLogName is the wire record's filename inside the config dir.
const convoLogName = "convo.jsonl"

// openConvoLog opens the conversation log, rotating it the way the app log is
// rotated — the same two knobs, because a record kept for debugging must not
// be the thing that fills the disk.
func openConvoLog(configDir string) (*os.File, error) {
	f, err := applog.OpenFile(filepath.Join(configDir, convoLogName),
		applog.DefaultMaxBytes, applog.DefaultMaxArchives)
	if err != nil {
		return nil, fmt.Errorf("conversation log: %w", err)
	}
	return f, nil
}

// telegramHomeChat validates the telegram: block and returns the HOME chat,
// where cron results and ownerless completions land. Not an access rule —
// rooms are authorized by who speaks in them.
//
// Every failure names the field at fault: the block is almost always present,
// since boot writes one with blank fields, and telling someone to add a block
// they are looking at is the wrong diagnosis. Shared with `shell3 health`.
func telegramHomeChat(tg config.TelegramConfig) (int64, error) {
	switch {
	case !tg.Present:
		return 0, fmt.Errorf("no telegram: block in the kit's shell3: wiring — add one (token, chat_id), or run `shell3 boot` to write it")
	case tg.Token == "":
		return 0, fmt.Errorf("telegram.token is empty — put your @BotFather token in the .env beside shell3.sh")
	}
	if tg.ChatID == "" {
		// Fall back to the first allowlisted person's DM. A bot cannot open a
		// DM the user has never written to, which `shell3 health` says.
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
	// allowlist's owner fallback resolves to nobody and the bot would start,
	// look healthy, and ignore every human. Refuse rather than be discovered
	// by silence.
	if id < 0 && len(tg.AllowFrom) == 0 {
		return 0, fmt.Errorf("telegram.chat_id %d is a group and telegram.allow_from is empty — "+
			"nobody would be allowed to drive the agent; list the user ids that may (@userinfobot prints yours)", id)
	}
	return id, nil
}

// parseChatID is the one definition of a chat id: a base-10 int64, what the
// Bot API takes. Validators wrap its error in their own wording.
func parseChatID(s string) (int64, error) {
	return strconv.ParseInt(strings.TrimSpace(s), 10, 64)
}

func newQuietStore() (*telegram.QuietStore, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home directory: %w", err)
	}
	return &telegram.QuietStore{Path: filepath.Join(paths.NewGlobal(home).Root, "quiet_mode.json")}, nil
}

// configReloader and rearmBot are the slices of Runtime and Bot that
// reloadAndRearm needs, so the coordination logic is testable with fakes.
type configReloader interface {
	Reload() (shell3.ReloadResult, error)
	Cron() []shell3.CronJob
}

type rearmBot interface {
	SetJobRunner(func(name string) error)
	// With SetJobRunner, this lets reloadAndRearm wire the new scheduler's
	// tool-job post callback itself.
	PostCompletion(p shell3.CompletionPost) error
}

// reloadAndRearm rebuilds config, then stops the old cron scheduler and arms a
// fresh one from the reloaded jobs, rewiring /run. On failure the old
// scheduler is returned still running, so a bad config never tears down a
// working schedule.
//
// The host tools need no re-registration: they install through the session
// decorator, which Runtime.Reload re-applies to every live
// session.
func reloadAndRearm(r configReloader, b rearmBot, disp cron.Dispatcher, store cron.RunStore, log applog.Logger, old *cron.Scheduler) (*cron.Scheduler, shell3.ReloadResult, error) {
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
	ns, err := cron.NewWithStore(disp, store, jobs)
	if err != nil {
		return old, res, err
	}
	ns.SetLogger(log)
	if old != nil {
		old.Stop()
	}
	ns.Start()
	b.SetJobRunner(ns.Run)
	return ns, res, nil
}
