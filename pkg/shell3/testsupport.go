package shell3

import (
	"context"

	"github.com/weatherjean/shell3/internal/chat"
)

// RuntimeForTest builds a Runtime around a caller-supplied per-session config
// builder, for test harnesses in other packages (see pkg/shell3/shell3test). It
// exists so those tests can inject a fake model without the production shell3
// package importing `testing` or fakellm; it is NOT part of the stable embedding
// API. The returned Runtime owns no shared parts (cleanup is a no-op) and must be
// Closed by the caller.
func RuntimeForTest(workDir string, sessionConfig func(SessionOpts) (chat.Config, error)) *Runtime {
	ctx, cancel := context.WithCancel(context.Background())
	return &Runtime{
		sessionConfig: sessionConfig,
		events:        make(chan HostEvent, 64),
		workDir:       workDir,
		ctx:           ctx,
		cancel:        cancel,
		cleanup:       func() {},
		sessions:      map[string]*Session{},
	}
}
