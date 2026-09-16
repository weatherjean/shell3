package procutil

import (
	"context"
	"fmt"
	"os"
	"syscall"
)

// ParentContext cancels when the spawning process closes its lifetime pipe,
// including on abrupt exit. No messages or credentials travel on this pipe.
// The child must close it on exec so the external runner cannot keep it alive.
func ParentContext(ctx context.Context, pipe *os.File) (context.Context, context.CancelFunc, error) {
	info, err := pipe.Stat()
	if err != nil {
		return nil, nil, err
	}
	if info.Mode()&os.ModeNamedPipe == 0 {
		return nil, nil, fmt.Errorf("parent lifetime descriptor is not a pipe")
	}
	syscall.CloseOnExec(int(pipe.Fd()))
	childCtx, cancel := context.WithCancel(ctx)
	go func() {
		var b [1]byte
		_, _ = pipe.Read(b[:])
		cancel()
	}()
	return childCtx, func() {
		cancel()
		_ = pipe.Close()
	}, nil
}
