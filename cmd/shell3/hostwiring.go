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

// runJanitors sweeps the runs store and media dir once per process, never on
// `shell3 ask`. Both fail open — a janitor fault is cosmetic — and both
// keep-days values treat 0 as keep forever.
func runJanitors(runsRoot string, runsKeepDays, mediaKeepDays int, out io.Writer) {
	// Sessions past runs_keep_days, plus thread entries naming dead sessions.
	removedRuns, removedThreads, err := runs.Sweep(runsRoot,
		time.Duration(runsKeepDays)*24*time.Hour, time.Now())
	if err != nil {
		fmt.Fprintf(out, "warning: janitor: %v\n", err)
	}
	if len(removedRuns) > 0 || removedThreads > 0 {
		fmt.Fprintf(out, "janitor: removed %d runs, %d thread entries\n",
			len(removedRuns), removedThreads)
	}
	// Opt-in by default: attachments are user data, deleted only on request.
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

// openThreads runs the janitors and returns one front-end's thread index over
// the just-swept store.
func openThreads(rt *shell3.Runtime, surface string) *telegram.ThreadIndex {
	p := rt.Parts()
	runJanitors(p.RunsRoot(), p.RunsKeepDays(), p.MediaKeepDays(), os.Stdout)
	// Resolved per call: /reload swaps generations, and the parked one closes
	// its database handle when it drains.
	return telegram.NewThreadIndex(func() *runs.Store {
		if p := rt.Parts(); p != nil {
			return p.Store()
		}
		return nil
	}, surface)
}

// wireHost is the transport-independent bot wiring both `shell3 telegram`
// transports share: session decorator, completion host, hidden cron parent,
// job sources, scheduler, /reload coordinator. Its cleanup stops whichever
// scheduler is CURRENT at shutdown, since a reload swaps it.
func wireHost(b *telegram.Bot, rt *shell3.Runtime, workDir string) (cleanup func(), err error) {
	b.SetWorkDir(workDir) // resolves send_media_telegram relative paths
	b.SetConfigDir(rt.Parts().ConfigDir())
	b.SetRunsRoot(rt.Parts().RunsRoot())
	b.SetLogger(rt.Parts().Log()) // host-side faults (e.g. a lost current-session marker write) land in the app log

	// The decorator registers the bot's host tools on main chat sessions, not
	// headless children, and Runtime.Reload re-applies it, so it survives a
	// reload with no resync. /quiet is persisted separately, read per send.
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

	// Completions route as mail through the bot's CompletionHost: floor and
	// direct posts land as ⏰/🔔, default mail resumes the owning thread — or
	// starts a fresh turn — whose reply posts as ✉️.
	rt.SetCompletionHost(b)

	// Kit commands join the built-in menu, answered by a shell function with
	// no model turn. Re-installed on reload, since a kit may add or drop one.
	installKitCommands(b, rt)
	installRoomConfig(b, rt)

	// Keep redelivering while THIS process runs: a post that failed to
	// send keeps its outbox row, and this ticker retries until the transport
	// is back, so the floor survives an outage without a restart.
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

	// Cron's subagents need SOME parent session. One hidden session is it,
	// running no turns of its own, adopted so it is never retired and its
	// jobs keep resolving.
	cronSess, err := rt.Session(shell3.SessionOpts{
		Name: "cron", WorkDir: workDir, Headless: true,
	})
	if err != nil {
		return nil, err
	}
	b.AdoptSession(cronSess)

	// Both resolve Parts per call, so a /reload's new kit, database and log
	// take effect without re-wiring.
	store := storeRunStore{partsRef{rt.Parts}}
	log := partsLogger{partsRef{rt.Parts}}
	sched, err := armCron(cronSess, store, log, rt.Cron())
	if err != nil {
		return nil, err
	}
	// A closure over the mutable handle: /reload swaps sched, and the CURRENT
	// one is what must stop at shutdown.
	var schedMu sync.Mutex
	currentSched := func() *cron.Scheduler {
		schedMu.Lock()
		defer schedMu.Unlock()
		return sched
	}
	if sched != nil {
		b.SetJobRunner(sched.Run) // /run <job>
	}
	// The scheduler only ever learns that a dispatch was ACCEPTED. The
	// completion router is where a cron run's real result is known, so point
	// it back here — through currentSched, so a /reload's new scheduler is the
	// one that records.
	rt.SetCronOutcomeHook(func(o shell3.CronOutcome) {
		if s := currentSched(); s != nil {
			s.RecordOutcome(o)
		}
	})

	// Redeliver what the previous process left behind, now that a host can
	// carry it. Start-time-only, like the janitors. ORDER IS LOAD-BEARING:
	// after the cron outcome hook, because a dead-PID marker recovered here
	// is a cron run that was lost mid-flight — an outcome its job's history
	// must count, and reportCronOutcome silently drops with no hook wired.
	if n := rt.RecoverCompletions(); n > 0 {
		rt.Parts().Log().Info("recovered undelivered completions from the previous run", "count", n)
	}
	// The status document reads the live scheduler's history.
	cronStatusFn := func() []cron.JobStatus {
		if s := currentSched(); s != nil {
			return s.Jobs()
		}
		return nil
	}
	// It also shows each job's rolling spend. A rollup failure must not break
	// the page: log and omit the costs, which render.cronCost already treats
	// as "unknown" rather than "zero".
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

	// /status is a point-in-time HTML document sent through Telegram. It reads
	// the same live state on every invocation and spends no model turn.
	b.SetStatusDocument(func(sess *shell3.Session) (string, []byte, error) {
		if sess == nil {
			sess = b.LiveSession()
		}
		now := time.Now()
		page := render.StatusPageHTML(sess, rt, version, cronSess.Jobs(),
			cronStatusFn(), cronCostFn(), statusRooms(b), b.Inbox(), now)
		return "shell3-status-" + now.UTC().Format("20060102-150405") + ".html", []byte(page), nil
	})

	// /reload and the reload tool rebuild config and swap the scheduler. The
	// host tools need no re-registration: Runtime.Reload re-applies the
	// decorator.
	b.SetReloader(func() (shell3.ReloadResult, error) {
		ns, res, err := reloadAndRearm(rt, b, cronSess, store, log, currentSched())
		// A reload may add, rename or drop a command.
		installKitCommands(b, rt)
		installRoomConfig(b, rt)
		schedMu.Lock()
		sched = ns
		schedMu.Unlock()
		return res, err
	})

	return func() {
		close(redeliverDone)
		if s := currentSched(); s != nil {
			s.Stop()
		}
	}, nil
}

func statusRooms(b *telegram.Bot) []render.RoomInfo {
	rooms := b.Rooms()
	out := make([]render.RoomInfo, 0, len(rooms))
	for _, r := range rooms {
		out = append(out, render.RoomInfo{
			ChatID: r.ChatID, Title: r.Title, Busy: r.Busy,
			Jobs: r.Jobs, Queued: r.Queued, SessionID: r.SessionID,
		})
	}
	return out
}

// redeliverEvery: long enough that a hard outage costs a handful of retries
// per hour, short enough that the ⚠️ lands minutes after the network returns.
const redeliverEvery = 5 * time.Minute

// installKitCommands points the bot at the loaded kit's commands. Both the
// menu list and the runner come from the live Parts, so a reload changes them
// together.
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
	// Resolved per call, not captured: a runner holding the old LoadedConfig
	// would keep sourcing the previous kit after a reload.
	b.SetKitCommands(cmds, func(ctx context.Context, name, arg string) (string, error) {
		return rt.Parts().LoadedConfig().RunCommand(ctx, name, arg)
	})
}

// installRoomConfig hands the bot the per-room configuration and the reader
// its briefs use. Called at startup and after every /reload, resolving the
// telegram: block per call so new rooms take effect without a restart.
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
