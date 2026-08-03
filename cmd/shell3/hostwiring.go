//go:build unix

package main

import (
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/weatherjean/shell3/internal/cron"
	"github.com/weatherjean/shell3/internal/media"
	"github.com/weatherjean/shell3/internal/runs"
	"github.com/weatherjean/shell3/internal/shell3"
	"github.com/weatherjean/shell3/internal/telegram"
)

// openThreads runs the runs janitor for one front-end's thread index and opens
// it. Start-time only, never on `shell3 ask`: it sweeps runs/<id>/ dirs past
// runs_keep_days (0 = keep forever) and, in the same pass, drops thread-index
// entries pointing at sessions that no longer exist — whether just swept or
// already gone (deleted by hand, an older crash). Must run before
// NewThreadIndex opens that file for the live process.
func openThreads(rt *shell3.Runtime, threadsFile string) (*telegram.ThreadIndex, error) {
	runsRoot := rt.Parts().RunsRoot()
	threadsPath := filepath.Join(runsRoot, threadsFile)
	removedRuns, err := runs.Sweep(runsRoot,
		time.Duration(rt.Parts().RunsKeepDays())*24*time.Hour, time.Now())
	if err != nil {
		// Fail-open: Sweep skipped whatever it couldn't remove and kept going.
		// Leftover run dirs are cosmetic, never worth refusing to start over.
		fmt.Printf("warning: janitor: %v\n", err)
	}
	removedThreads, err := telegram.PruneThreadIndex(threadsPath,
		sessionExistsUnder(runsRoot, removedRuns))
	if err != nil {
		return nil, fmt.Errorf("runs janitor: prune thread index: %w", err)
	}
	if len(removedRuns) > 0 || removedThreads > 0 {
		fmt.Printf("janitor: removed %d runs, %d thread entries\n",
			len(removedRuns), removedThreads)
	}
	return telegram.NewThreadIndex(threadsPath)
}

// wireHost performs the transport-independent bot wiring shared by
// `shell3 telegram` and `shell3 serve`: media capabilities + the session
// decorator, the completion host, the hidden cron dispatch parent, job
// sources, the cron scheduler, and the /reload coordinator. Returns a cleanup
// that stops whichever scheduler is CURRENT at shutdown (reload swaps it).
func wireHost(b *telegram.Bot, rt *shell3.Runtime, workDir string) (cleanup func(), err error) {
	b.SetWorkDir(workDir) // resolves send_media_telegram relative paths
	b.SetVersion(version)
	b.SetRunsRoot(rt.Parts().RunsRoot())

	// Media (STT/describe/TTS/imagegen): built from the runtime's current
	// config and re-built by the reload closure below. The session decorator
	// registers image_generate on EVERY session and, for main chat sessions
	// (not the headless subagent children), the bot's host tools.
	// Runtime.Reload re-applies the decorator, so both survive a reload with
	// no separate resync.
	voiceModeStore, err := newVoiceModeStore()
	if err != nil {
		return nil, err
	}
	b.SetMedia(buildMediaCaps(rt), voiceModeStore)
	rt.SetSessionDecorator(func(s *shell3.Session) {
		_ = media.RegisterImageTool(s, buildMediaClients(rt))
		if !s.Headless() { // main chat sessions only, not subagent children
			b.DecorateChatSession(s)
		}
	})

	// Background completions (bash_bg, subagents, cron) route through the
	// notifier via the bot's CompletionHost: send verdicts post ⏰/🔔
	// messages, wake verdicts resume the owning thread or start a fresh
	// main-agent turn.
	rt.SetCompletionHost(b)

	// Cron dispatches subagents, which need SOME parent session. One hidden
	// session is the dispatch parent; it runs no turns of its own (the
	// notifier owns delivery). Adopted so it is never retired and its jobs
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

	// /reload + the reload tool: rebuild config, swap the cron scheduler,
	// re-wire media. The bot's host tools and image_generate need no
	// re-registration — Runtime.Reload re-applies the session decorator.
	b.SetReloader(func() (shell3.ReloadResult, error) {
		ns, res, err := reloadAndRearm(rt, b, cronSess, currentSched(),
			func() { b.SetMedia(buildMediaCaps(rt), voiceModeStore) })
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
