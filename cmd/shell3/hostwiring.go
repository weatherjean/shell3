//go:build unix

package main

import (
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/weatherjean/shell3/internal/config"
	"github.com/weatherjean/shell3/internal/cron"
	"github.com/weatherjean/shell3/internal/mediadir"
	"github.com/weatherjean/shell3/internal/render"
	"github.com/weatherjean/shell3/internal/runs"
	"github.com/weatherjean/shell3/internal/shell3"
	"github.com/weatherjean/shell3/internal/telegram"
)

// runStartupJanitors runs the two start-time-only sweeps for rt. Split from
// runJanitors so the sweeps themselves are testable without a live Runtime —
// they DELETE user data, so they get their own coverage.
func runStartupJanitors(rt *shell3.Runtime) {
	p := rt.Parts()
	runJanitors(p.RunsRoot(), p.RunsKeepDays(), p.MediaKeepDays(), os.Stdout)
}

// runJanitors sweeps the runs store and the media dir, once per process,
// never on `shell3 ask`. Both fail open: a janitor fault is cosmetic, never
// worth refusing to start over. Both keep-days values are "0 = keep forever".
func runJanitors(runsRoot string, runsKeepDays, mediaKeepDays int, out io.Writer) {
	// Sessions past runs_keep_days, and, in the same pass, thread entries
	// pointing at sessions that no longer exist.
	removedRuns, removedThreads, err := runs.Sweep(runsRoot,
		time.Duration(runsKeepDays)*24*time.Hour, time.Now())
	if err != nil {
		fmt.Fprintf(out, "warning: janitor: %v\n", err)
	}
	if len(removedRuns) > 0 || removedThreads > 0 {
		fmt.Fprintf(out, "janitor: removed %d runs, %d thread entries\n",
			len(removedRuns), removedThreads)
	}
	// The media dir, gated by media_keep_days (default 0 = keep forever, so
	// this is opt-in). Attachments are user data; deletion only happens if
	// the operator asked for it.
	if mediaKeepDays <= 0 {
		return
	}
	mdir, err := mediadir.Dir()
	if err != nil {
		fmt.Fprintf(out, "warning: media janitor: %v\n", err)
		return
	}
	removedMedia, err := mediadir.Sweep(mdir, time.Duration(mediaKeepDays)*24*time.Hour, time.Now())
	if err != nil {
		fmt.Fprintf(out, "warning: media janitor: %v\n", err)
	}
	if removedMedia > 0 {
		fmt.Fprintf(out, "janitor: removed %d media files\n", removedMedia)
	}
}

// openThreads runs the startup janitors and returns one front-end's thread
// index over the (possibly just-swept) runs store.
func openThreads(rt *shell3.Runtime, surface string) *telegram.ThreadIndex {
	runStartupJanitors(rt)
	// The store is resolved per call: /reload swaps Parts generations and the
	// parked old generation closes its database handle when it drains.
	return telegram.NewThreadIndex(func() *runs.Store {
		if p := rt.Parts(); p != nil {
			return p.Store()
		}
		return nil
	}, surface)
}

// wireHost performs the transport-independent bot wiring shared by
// `shell3 telegram` and `shell3 serve`: the session decorator, the
// completion host, the hidden cron dispatch parent, job sources, the cron
// scheduler, and the /reload coordinator. Returns a cleanup that stops
// whichever scheduler is CURRENT at shutdown (reload swaps it).
func wireHost(b *telegram.Bot, rt *shell3.Runtime, workDir string) (cleanup func(), err error) {
	b.SetWorkDir(workDir) // resolves send_media_telegram relative paths
	b.SetConfigDir(rt.Parts().ConfigDir())
	b.SetLogger(rt.Parts().Log()) // host-side faults (e.g. a lost current-session marker write) land in the app log

	// The session decorator registers, for main chat sessions (not the
	// headless subagent children), the bot's host tools. Runtime.Reload
	// re-applies the decorator, so it survives a reload with no separate
	// resync.
	// The /quiet toggle: persisted on its own store, read per send.
	quietStore, err := newQuietStore()
	if err != nil {
		return nil, err
	}
	b.SetQuiet(quietStore)
	rt.SetSessionDecorator(func(s *shell3.Session) {
		if !s.Headless() { // main chat sessions only, not subagent children
			b.DecorateChatSession(s)
		}
	})

	// Background completions (bash_bg, subagents, cron) route as mail via
	// the bot's CompletionHost: floor/direct posts land as ⏰/🔔 messages,
	// default mail resumes the owning thread — or starts a fresh main-agent
	// turn — whose reply posts as an ✉️ update.
	rt.SetCompletionHost(b)

	// Kit-declared commands (`command:`) join the built-in menu. Answered by
	// a shell function with no model turn, so they cost nothing to run.
	// Re-installed on reload, since a kit may add or drop one.
	installKitCommands(b, rt)
	installRoomConfig(b, rt)

	// Redeliver what the previous process left undelivered (completions that
	// raced a shutdown, jobs killed in flight) now that the host can carry
	// them. Start-time-only, like the janitors — ask never runs this.
	if n := rt.RecoverCompletions(); n > 0 {
		rt.Parts().Log().Info("recovered undelivered completions from the previous run", "count", n)
	}
	// And keep redelivering while THIS process runs: a completion post that
	// failed to send (Telegram outage) keeps its outbox row, and this ticker
	// retries it until the transport is back — the ⚠️/⏰/🔔 floor survives an
	// outage without waiting for a restart. No-op when nothing failed.
	redeliverDone := make(chan struct{})
	go func() {
		t := time.NewTicker(redeliverEvery)
		defer t.Stop()
		for {
			select {
			case <-redeliverDone:
				return
			case <-t.C:
				if n := rt.RedeliverUndelivered(); n > 0 {
					rt.Parts().Log().Info("redelivered completion posts after a send failure", "count", n)
				}
			}
		}
	}()

	// Cron dispatches subagents, which need SOME parent session. One hidden
	// session is the dispatch parent; it runs no turns of its own (results
	// route as completion mail). Adopted so it is never retired and its jobs
	// keep resolving.
	cronSess, err := rt.Session(shell3.SessionOpts{
		Name: "cron", WorkDir: workDir, Headless: true,
	})
	if err != nil {
		return nil, err
	}
	b.AdoptSession(cronSess)

	// tools adapts the loaded kit to cron.ToolRunner, resolving Parts fresh on
	// every call so a /reload's new kit takes effect without re-wiring.
	tools := kitTools{parts: rt.Parts}
	// store persists run history across restarts (internal/cron/store.go);
	// like tools, it resolves Parts fresh per call so a /reload's swapped
	// database is what gets read/written.
	store := storeRunStore{parts: rt.Parts}
	log := partsLogger{parts: rt.Parts}
	sched, err := armCron(cronSess, tools, store, log, b, rt.Cron())
	if err != nil {
		return nil, err
	}
	// A closure over the mutable handle: /reload swaps sched for a fresh
	// scheduler, and it is the CURRENT one that must stop at shutdown.
	var schedMu sync.Mutex
	currentSched := func() *cron.Scheduler {
		schedMu.Lock()
		defer schedMu.Unlock()
		return sched
	}
	if sched != nil {
		b.SetJobRunner(sched.Run) // /run <job>
	}
	// The dash's Cron section reads the live scheduler's history (JobStatus
	// carries the run counts, not just the last-run time).
	cronStatusFn := func() []cron.JobStatus {
		if s := currentSched(); s != nil {
			return s.Jobs()
		}
		return nil
	}
	// The dash's Cron section also shows each job's rolling spend. A rollup
	// failure (closed store mid-reload, corrupt db) must not break the page —
	// log and omit costs entirely rather than fail the whole view over a
	// section that was already best-effort (render.cronCostSuffix treats a
	// missing entry as "unknown", never "zero").
	cronCostFn := func() map[string]runs.JobCost {
		store := rt.Parts().Store()
		if store == nil {
			return nil
		}
		rows, err := store.CronRollup(time.Now().Add(-render.CronRollupWindow))
		if err != nil {
			rt.Parts().Log().Warn("cron cost rollup failed", "error", err)
			return nil
		}
		out := make(map[string]runs.JobCost, len(rows))
		for _, c := range rows {
			out[c.CronJob] = c
		}
		return out
	}

	// The web dash: an always-on 127.0.0.1 listener over the same live state
	// the bot used to render inline (dash_port: 0 disables; a bind failure
	// warns and the bot runs dashless).
	closeDash := wireDash(b, rt, cronSess, cronStatusFn, cronCostFn)

	// /reload + the reload tool: rebuild config, swap the cron scheduler.
	// The bot's host tools need no re-registration — Runtime.Reload
	// re-applies the session decorator.
	b.SetReloader(func() (shell3.ReloadResult, error) {
		// reloadAndRearm wires the fresh scheduler's post callback itself,
		// before starting it — see wireCronPost's ordering note.
		ns, res, err := reloadAndRearm(rt, b, cronSess, tools, store, log, currentSched())
		// A reload may add, rename, or drop a command; refresh the menu so
		// the "/" list matches the kit that is actually loaded.
		installKitCommands(b, rt)
		installRoomConfig(b, rt)
		schedMu.Lock()
		sched = ns
		schedMu.Unlock()
		return res, err
	})

	return func() {
		close(redeliverDone)
		closeDash()
		if s := currentSched(); s != nil {
			s.Stop()
		}
	}, nil
}

// redeliverEvery is how often the host retries completion posts whose send
// failed. Long enough that a hard outage costs a handful of retry sends per
// hour, short enough that the ⚠️ lands minutes after the network returns.
const redeliverEvery = 5 * time.Minute

// installKitCommands points the bot at whatever commands the currently loaded
// kit declares. Both the list (for the "/" menu) and the runner come from the
// live Parts, so a reload that changes the kit changes both together.
func installKitCommands(b *telegram.Bot, rt *shell3.Runtime) {
	p := rt.Parts()
	k := p.Kit()
	if k == nil || len(k.Commands) == 0 {
		b.SetKitCommands(nil, nil)
		return
	}
	cmds := make([]telegram.KitCommand, 0, len(k.Commands))
	for _, name := range slices.Sorted(maps.Keys(k.Commands)) {
		cmds = append(cmds, telegram.KitCommand{Name: name, Desc: k.Commands[name].Desc})
	}
	// The config is resolved per call, not captured: a reload swaps Parts,
	// and a runner holding the old LoadedConfig would keep sourcing the
	// previous kit.
	b.SetKitCommands(cmds, func(ctx context.Context, name, arg string) (string, error) {
		return rt.Parts().LoadedConfig().RunCommand(ctx, name, arg)
	})
}

// installRoomConfig hands the bot the operator's per-room configuration and
// the reader its room briefs use for declared context files. Called at
// startup AND after every /reload — the telegram: block is resolved per call
// so a reload's new rooms take effect without a restart.
func installRoomConfig(b *telegram.Bot, rt *shell3.Runtime) {
	tg := rt.Telegram()
	settings := make([]telegram.ChatSetting, 0, len(tg.Chats))
	for _, ch := range tg.Chats {
		id, err := parseChatID(ch.ID)
		if err != nil {
			continue // rejected at load; a survivor here can only be ignored
		}
		settings = append(settings, telegram.ChatSetting{
			ChatID: id, UseDescription: ch.UseDescription, Context: ch.Context,
		})
	}
	b.SetChatSettings(settings)

	// Room context files go through the SAME reader the agent's own
	// `context:` uses — the 64 KB cap and middle elision are what keep a
	// runaway brief from crowding out the conversation it informs.
	configDir := rt.Parts().ConfigDir()
	b.SetContextReader(func(paths []string) string {
		files, err := config.ResolveContextFiles(configDir, paths)
		if err != nil {
			return ""
		}
		var sb strings.Builder
		for _, f := range files {
			fmt.Fprintf(&sb, "### %s\n\n%s\n\n", f.Path, f.Body)
		}
		return sb.String()
	})
}
