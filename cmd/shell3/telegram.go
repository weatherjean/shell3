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
	"time"

	"github.com/spf13/cobra"

	"github.com/weatherjean/shell3/internal/config"
	"github.com/weatherjean/shell3/internal/cron"
	"github.com/weatherjean/shell3/internal/paths"
	"github.com/weatherjean/shell3/internal/shell3"
	"github.com/weatherjean/shell3/internal/telegram"
)

// newTelegramCommand builds `shell3 telegram` — the personal bot front-end.
// One chat, one agent: every inbound message runs its own session (a fresh
// one, or the thread's session when the message is a Telegram reply), cron
// fires from a hidden dispatch parent, and background completions come back
// through the notifier.
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
			} else if chatID, err = telegramChatID(tg); err != nil {
				return err
			}

			// The agent's shell runs in telegram.workdir; the config dir is the
			// fallback, so a hosted agent stays self-contained.
			workDir := tg.WorkDir
			if workDir == "" {
				workDir = resolved
			}

			// Fresh-turn model: the bot holds NO long-lived session. Each inbound
			// message runs in its own session (fresh, or the thread's session on a
			// Telegram reply); the thread index maps message ids → session ids and
			// lives for the whole process (kept across /reload). openThreads runs
			// the runs janitor first.
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
			// Transport-independent wiring (media, decorator, completion host,
			// cron parent + scheduler, /reload) — shared with `shell3 serve`.
			// LIFO: the scheduler-stop cleanup runs before the earlier
			// `defer rt.Close()`.
			stopSched, err := wireHost(b, rt, workDir)
			if err != nil {
				return err
			}
			defer stopSched()

			if console {
				fmt.Println("shell3 telegram --console: reading events from stdin " +
					"(plain line = fresh message, \"@<id> text\" = reply, \"/…\" = command, EOF quits)")
			} else {
				// Register the "/" command hints (best-effort).
				if err := apiClient.SetCommands(ctx, telegram.BotCommands()); err != nil {
					fmt.Printf("warning: could not set commands: %v\n", err)
				}
				// The menu button persists on Telegram's side; one set by an older
				// build would keep opening a dead web_app URL.
				if err := apiClient.ClearMenuButton(ctx); err != nil {
					fmt.Printf("warning: could not clear menu button: %v\n", err)
				}
				// Greet the chat; the ReplyKeyboardRemove markup also tears down any
				// persistent reply-keyboard bar left by an older build (commands
				// live in the "/" menu instead). Best-effort.
				banner := "๑ï shell3 online — your personal agent, at your pace\n\n" +
					"Every message starts a fresh thread; reply to any of my messages to continue its thread. " +
					"/status shows what's wired, /jobs what's running, /stop halts the current turn, " +
					"/reload applies config changes."
				if err := apiClient.SendRemovingKeyboard(ctx, chatID, banner); err != nil {
					fmt.Printf("warning: could not send the greeting: %v\n", err)
				}
				fmt.Printf("shell3 telegram: listening for chat %d\n  config: %s\n", chatID, resolved)
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

// telegramChatID validates the `telegram:` block the way the front-end needs it
// and returns the parsed chat id. Every failure names the field at fault: the
// block is almost always PRESENT (shell3 boot always writes one, blank fields
// included) and the user's actual problem is one empty field — telling them to
// add a block they are looking at is the wrong diagnosis. Shared with
// `shell3 health`, so the check and the message are the same in both places.
func telegramChatID(tg config.TelegramConfig) (int64, error) {
	switch {
	case !tg.Present:
		return 0, fmt.Errorf("no telegram: block in shell3.yaml — add one (token, chat_id), or run `shell3 boot` to write it")
	case tg.Token == "":
		return 0, fmt.Errorf("telegram.token is empty — put your @BotFather token in the .env beside shell3.yaml")
	case tg.ChatID == "":
		return 0, fmt.Errorf("telegram.chat_id is empty — set it in shell3.yaml to the one chat the bot answers (@userinfobot prints yours)")
	}
	id, err := parseChatID(tg.ChatID)
	if err != nil {
		return 0, fmt.Errorf("telegram.chat_id %q is not a number", tg.ChatID)
	}
	return id, nil
}

// parseChatID is the one definition of what a chat id IS — a base-10 int64,
// exactly what the Bot API takes. Both validators (boot's form/flag check and
// telegramChatID above) call it, wrapping the error in their own wording.
func parseChatID(s string) (int64, error) {
	return strconv.ParseInt(strings.TrimSpace(s), 10, 64)
}

// jobTranscriptOf builds the per-job transcript lookup /job <id> renders: a
// subagent job's transcript is its child session's, a bash_bg job's is its
// captured output. Unknown ids render with no Output section.
func jobTranscriptOf(sess *shell3.Session) func(id string) string {
	return func(id string) string {
		for _, j := range sess.Jobs() {
			if j.ID != id {
				continue
			}
			if j.Kind == shell3.JobSubagent {
				return sess.JobTranscript(id)
			}
			return sess.JobOutput(id)
		}
		return ""
	}
}

// cronLastRuns projects the scheduler's per-job status onto the name → last-run
// map /cron renders. A nil scheduler (no jobs) or an unparsable/never-run entry
// simply contributes nothing, which renders as "never".
func cronLastRuns(sched *cron.Scheduler) map[string]time.Time {
	if sched == nil {
		return nil
	}
	out := map[string]time.Time{}
	for _, j := range sched.Jobs() {
		if j.LastRun == "" {
			continue
		}
		t, err := time.Parse(time.RFC3339, j.LastRun)
		if err != nil {
			continue
		}
		out[j.Name] = t
	}
	return out
}

// buildMediaCaps bundles the runtime's media clients with the two shell3.yaml
// defaults the bot reads directly (media.stt.echo, media.tts.mode). Rebuilt on
// every reload, since the config may have changed either.
func buildMediaCaps(rt *shell3.Runtime) *telegram.MediaCaps {
	caps := &telegram.MediaCaps{Clients: *buildMediaClients(rt)}
	cfg := rt.Parts().MediaConfig()
	if stt := cfg.STT(); stt != nil {
		caps.STTEcho = stt.Echo
	}
	if tts := cfg.TTS(); tts != nil {
		caps.TTSMode = tts.Mode
	}
	return caps
}

// newVoiceModeStore opens the per-chat inbound-voice-reply mode file at
// ~/.shell3/voice_mode.json (the global root, independent of which config or
// workdir this process was started with).
func newVoiceModeStore() (*telegram.ModeStore, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home directory: %w", err)
	}
	return &telegram.ModeStore{Path: filepath.Join(paths.NewGlobal(home).Root, "voice_mode.json")}, nil
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
}

// reloadAndRearm performs a /reload: rebuild config, then stop the old cron
// scheduler and arm a fresh one from the reloaded jobs (rewiring the bot's
// /run handler). Returns the new scheduler (nil when no jobs), the reload
// result, and any error. On failure the old scheduler is left running and
// returned unchanged, so a bad config never tears down a working schedule.
//
// The bot's host tools (send_media_telegram, reload, status) and image_generate
// need no explicit re-registration: they are installed via the session
// decorator, which Runtime.Reload re-applies to every live session.
// resyncMedia, when non-nil, is called after a successful Reload to rebuild the
// bot's media capabilities from the freshly reloaded config.
func reloadAndRearm(r configReloader, b rearmBot, disp cron.Dispatcher, old *cron.Scheduler, resyncMedia func()) (*cron.Scheduler, shell3.ReloadResult, error) {
	res, err := r.Reload()
	if err != nil {
		return old, res, err
	}
	if resyncMedia != nil {
		resyncMedia()
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
	ns, err := cron.New(disp, jobs)
	if err != nil {
		return old, res, err
	}
	if old != nil {
		old.Stop()
	}
	ns.Start()
	b.SetJobRunner(ns.Run)
	return ns, res, nil
}
