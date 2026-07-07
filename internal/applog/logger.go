// Package applog provides rotating file + stderr structured logging.
//
// Debug lines go to the log file only; Warn and Error are mirrored to stderr
// so users see actionable messages in their terminal. Open rotates the file
// when it exceeds a size bound, keeping a bounded number of archives.
package applog

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// Rotation bounds for the shared global app log. Every opener of that file
// (e.g. agentsetup's runtime logger) MUST use these same constants: rotation
// happens at open time by renaming the path, so two openers with different
// bounds could rotate the file out from under each other's file descriptor,
// sending lines to an orphaned inode.
const (
	// DefaultMaxBytes is the size beyond which the log is rotated at open.
	DefaultMaxBytes = 2 * 1024 * 1024
	// DefaultMaxArchives is how many rotated archives (.1, .2, …) are kept.
	DefaultMaxArchives = 3
)

// Logger is the application-wide logging interface. Fields are key/value
// pairs: logger.Warn("msg", "key", val, "key2", val2).
type Logger interface {
	Debug(msg string, fields ...any)
	Warn(msg string, fields ...any)
	Error(msg string, err error, fields ...any)
}

// Noop discards all log output. Use in tests.
type Noop struct{}

func (Noop) Debug(string, ...any)        {}
func (Noop) Warn(string, ...any)         {}
func (Noop) Error(string, error, ...any) {}

// fileLogger writes structured log lines to w (mirroring Warn/Error to stderr).
type fileLogger struct {
	mu     sync.Mutex
	w      io.WriteCloser
	stderr io.Writer
}

func (l *fileLogger) Debug(msg string, fields ...any) {
	l.write("DEBUG", msg, nil, fields, false)
}

func (l *fileLogger) Warn(msg string, fields ...any) {
	l.write("WARN", msg, nil, fields, true)
}

func (l *fileLogger) Error(msg string, err error, fields ...any) {
	l.write("ERROR", msg, err, fields, true)
}

// write formats and emits a log line, also mirroring to stderr when mirror is
// true. Both writes happen under l.mu so their ordering stays consistent.
func (l *fileLogger) write(level, msg string, err error, fields []any, mirror bool) {
	var b strings.Builder
	b.WriteString(time.Now().UTC().Format(time.RFC3339))
	b.WriteString(" [")
	b.WriteString(level)
	b.WriteString("] ")
	b.WriteString(msg)
	if err != nil {
		b.WriteString(" error=")
		b.WriteString(err.Error())
	}
	for i := 0; i+1 < len(fields); i += 2 {
		fmt.Fprintf(&b, " %v=%v", fields[i], fields[i+1])
	}
	if len(fields)%2 == 1 {
		fmt.Fprintf(&b, " %v=<MISSING>", fields[len(fields)-1])
	}
	b.WriteString("\n")
	line := b.String()
	l.mu.Lock()
	_, _ = io.WriteString(l.w, line)
	if mirror {
		fmt.Fprint(l.stderr, line)
	}
	l.mu.Unlock()
}

// Open creates a Logger that writes to path, rotating the file if it exceeds
// maxBytes (keeping up to maxArchives archives). The caller closes the returned
// closer when done.
func Open(path string, maxBytes int64, maxArchives int) (Logger, io.Closer, error) {
	f, err := OpenFile(path, maxBytes, maxArchives)
	if err != nil {
		return Noop{}, nil, err // callers must check err before using the closer
	}
	lg := &fileLogger{w: f, stderr: os.Stderr}
	return lg, f, nil
}

// OpenFile rotates path if it exceeds maxBytes (keeping up to maxArchives
// archives), then opens it for append and returns the file. Unlike Open it
// returns the raw *os.File so callers can use it as a subprocess's stdout/stderr
// (see internal/modelproxy). The caller owns closing the file.
func OpenFile(path string, maxBytes int64, maxArchives int) (*os.File, error) {
	if err := rotate(path, maxBytes, maxArchives); err != nil {
		return nil, fmt.Errorf("applog: rotate: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("applog: open %s: %w", path, err)
	}
	return f, nil
}

// rotate renames path → path.1 → path.2 … up to maxArchives if path
// exceeds maxBytes. Files beyond maxArchives are deleted.
func rotate(path string, maxBytes int64, maxArchives int) error {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Size() <= maxBytes {
		return nil
	}
	// Delete the oldest archive to make room.
	oldest := fmt.Sprintf("%s.%d", path, maxArchives)
	_ = os.Remove(oldest)
	// Shift archives: .2 → .3, .1 → .2, etc.
	for i := maxArchives - 1; i >= 1; i-- {
		from := fmt.Sprintf("%s.%d", path, i)
		to := fmt.Sprintf("%s.%d", path, i+1)
		_ = os.Rename(from, to)
	}
	// Rotate current log to .1.
	return os.Rename(path, path+".1")
}
