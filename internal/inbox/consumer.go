//go:build unix

package inbox

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

// Listener owns the process-wide wake socket and exposes advisory destination
// hints. It never claims durable messages; consumers reread Store themselves.
type Listener struct {
	conn  *net.UnixConn
	lock  *os.File
	path  string
	hints chan string
	done  chan struct{}
	once  sync.Once
}

// StartListener binds the state root's wake socket. The socket is only a
// latency optimization; callers must periodically reconcile durable state.
func StartListener(ctx context.Context, store Store) (*Listener, error) {
	if err := os.MkdirAll(store.Root, 0o700); err != nil {
		return nil, fmt.Errorf("inbox: create state root: %w", err)
	}
	lockPath := filepath.Join(store.Root, "wake.lock")
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("inbox: open wake lock: %w", err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = lock.Close()
		return nil, errors.New("inbox: another wake listener owns this state root")
	}
	path := store.WakePath
	if path == "" {
		path = filepath.Join(store.Root, "wake.sock")
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		_ = lock.Close()
		return nil, fmt.Errorf("inbox: remove stale wake socket: %w", err)
	}
	addr := &net.UnixAddr{Name: path, Net: "unixgram"}
	conn, err := net.ListenUnixgram("unixgram", addr)
	if err != nil {
		_ = lock.Close()
		return nil, fmt.Errorf("inbox: listen for wake: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = conn.Close()
		_ = os.Remove(path)
		_ = lock.Close()
		return nil, fmt.Errorf("inbox: protect wake socket: %w", err)
	}
	l := &Listener{
		conn: conn, lock: lock, path: path,
		hints: make(chan string, 64), done: make(chan struct{}),
	}
	go l.run(ctx)
	return l, nil
}

// Hints reports durable destinations named by received datagrams. A hint may
// be dropped; callers must reconcile durable state independently.
func (l *Listener) Hints() <-chan string { return l.hints }

func (l *Listener) run(ctx context.Context) {
	defer close(l.hints)
	go func() {
		select {
		case <-ctx.Done():
			_ = l.Close()
		case <-l.done:
		}
	}()
	buf := make([]byte, 257)
	for {
		n, _, err := l.conn.ReadFromUnix(buf)
		if err != nil {
			return
		}
		target := string(buf[:n])
		if validateTarget(target) != nil {
			continue
		}
		select {
		case l.hints <- target:
		default:
		}
	}
}

// Close stops wake delivery and removes the socket.
func (l *Listener) Close() error {
	var closeErr error
	l.once.Do(func() {
		close(l.done)
		closeErr = l.conn.Close()
		_ = os.Remove(l.path)
		_ = syscall.Flock(int(l.lock.Fd()), syscall.LOCK_UN)
		_ = l.lock.Close()
	})
	return closeErr
}
