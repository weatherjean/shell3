package shell3

import (
	"context"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/runs"
)

// SetStoreForTest wires a runs store into a test runtime, so tests exercise
// the same persistence path the production runtime gets from its Parts.
func (rt *Runtime) SetStoreForTest(st *runs.Store) {
	rt.store = st
}

// RuntimeForTest builds a Runtime around a caller-supplied per-session config
// builder, for test harnesses in other packages (see internal/shell3/shell3test). It
// exists so those tests can inject a fake model without the production shell3
// package importing `testing` or fakellm; it is NOT part of the stable public
// API. The returned Runtime owns no shared parts (cleanup is a no-op) and must be
// Closed by the caller.
func RuntimeForTest(workDir string, sessionConfig func(SessionOpts) (chat.Config, error)) *Runtime {
	ctx, cancel := context.WithCancel(context.Background())
	rt := &Runtime{
		sessionConfig: sessionConfig,
		events:        make(chan HostEvent, 64),
		jobEvents:     make(chan JobProgress, 256),
		workDir:       workDir,
		ctx:           ctx,
		cancel:        cancel,
		cleanup:       func() {},
		sessions:      map[string]*Session{},
	}
	rt.jobs = newJobManager(rt, 0)
	return rt
}

// SetTelegramForTest replaces the runtime's `telegram:` block. The front-end
// reads its token/chat id from here — deliberately, so a /reload picks up a
// changed one — and a test exercising the bot wiring needs some way to supply
// it. Like RuntimeForTest, this is for test harnesses in other packages, not
// public API.
func (rt *Runtime) SetTelegramForTest(tc TelegramConfig) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.telegram = tc
}

// SetCronForTest replaces the runtime's declared cron jobs, so front-end
// tests can exercise the declared-but-not-armed reporting without a config
// tree. Test harness only, like SetTelegramForTest.
func (rt *Runtime) SetCronForTest(jobs []CronJob) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.cron = jobs
}
