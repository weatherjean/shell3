//go:build unix

package main

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/weatherjean/shell3/internal/cron"
	"github.com/weatherjean/shell3/internal/media"
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
	mdir, err := media.Dir()
	if err != nil {
		fmt.Fprintf(out, "warning: media janitor: %v\n", err)
		return
	}
	removedMedia, err := media.Sweep(mdir, time.Duration(mediaKeepDays)*24*time.Hour, time.Now())
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
	b.SetVersion(version)
	b.SetRunsRoot(rt.Parts().RunsRoot())

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
	// turn — quietly (mail_user is the way back to the chat).
	rt.SetCompletionHost(b)

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

	// /jobs, /job <id> and /cancel <id>: Session.Jobs reports the WHOLE job
	// runtime, not one session's share, so the pinned cron session is as good
	// a window as any. Cancel reuses jobManager's cascade (task_cancel
	// semantics).
	b.SetJobsSource(cronSess.Jobs, jobTranscriptOf(cronSess))
	b.SetJobControl(cronSess.KillJob)

	sched, err := armCron(cronSess, rt.Cron())
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
	// /cron's "last run" column reads the live scheduler's history.
	b.SetCronLastRuns(func() map[string]time.Time { return cronLastRuns(currentSched()) })

	// /reload + the reload tool: rebuild config, swap the cron scheduler.
	// The bot's host tools need no re-registration — Runtime.Reload
	// re-applies the session decorator.
	b.SetReloader(func() (shell3.ReloadResult, error) {
		ns, res, err := reloadAndRearm(rt, b, cronSess, currentSched())
		schedMu.Lock()
		sched = ns
		schedMu.Unlock()
		return res, err
	})

	return func() {
		if s := currentSched(); s != nil {
			s.Stop()
		}
	}, nil
}
