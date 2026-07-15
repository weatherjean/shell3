package shell3

import (
	"context"

	"github.com/weatherjean/shell3/internal/chat"
)

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

// SetHeartbeatForTest installs a heartbeat config on a test runtime, arming
// the front-ends' HEARTBEAT_OK suppression. Test-only seam, same caveats as
// RuntimeForTest.
func (rt *Runtime) SetHeartbeatForTest(hb *Heartbeat) { rt.heartbeat = hb }
