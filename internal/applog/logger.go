// Package applog provides the bounded project diagnostic log.
package applog

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

const (
	// DefaultMaxBytes and DefaultMaxArchives bound the active log plus five
	// archives to approximately 60 MiB.
	DefaultMaxBytes    int64 = 10 << 20
	DefaultMaxArchives       = 5
	maxRecordBytes           = 256 << 10
)

// Logger is the application-wide logging interface. Fields are key/value
// pairs: logger.Warn("msg", "key", val, "key2", val2).
type Logger interface {
	Debug(msg string, fields ...any)
	Info(msg string, fields ...any)
	Warn(msg string, fields ...any)
	Error(msg string, err error, fields ...any)
}

// Noop discards all log output. It is useful for isolated tests.
type Noop struct{}

func (Noop) Debug(string, ...any)        {}
func (Noop) Info(string, ...any)         {}
func (Noop) Warn(string, ...any)         {}
func (Noop) Error(string, error, ...any) {}

// fileLogger serializes records and rotates before a write would cross the
// active-file bound.
type fileLogger struct {
	mu       sync.Mutex
	path     string
	file     *os.File
	lock     *os.File
	size     int64
	maxBytes int64
	archives int
}

// Open opens an append-only JSONL diagnostic log. Rotation continues while
// the process is running; the returned closer owns the active file.
func Open(path string) (Logger, io.Closer, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, nil, fmt.Errorf("applog: create directory: %w", err)
	}
	l := &fileLogger{path: path, maxBytes: DefaultMaxBytes, archives: DefaultMaxArchives}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("applog: open lock: %w", err)
	}
	if err := lock.Chmod(0o600); err != nil {
		_ = lock.Close()
		return nil, nil, fmt.Errorf("applog: protect lock: %w", err)
	}
	l.lock = lock
	if err := l.lockExclusive(); err != nil {
		_ = lock.Close()
		return nil, nil, err
	}
	defer l.unlock()
	if err := l.open(); err != nil {
		_ = lock.Close()
		return nil, nil, err
	}
	if l.size >= l.maxBytes {
		if err := l.rotate(); err != nil {
			_ = l.file.Close()
			_ = l.lock.Close()
			return nil, nil, err
		}
	}
	return l, l, nil
}

func (l *fileLogger) Debug(msg string, fields ...any) { l.write("debug", msg, nil, fields) }
func (l *fileLogger) Info(msg string, fields ...any)  { l.write("info", msg, nil, fields) }
func (l *fileLogger) Warn(msg string, fields ...any)  { l.write("warn", msg, nil, fields) }
func (l *fileLogger) Error(msg string, err error, fields ...any) {
	l.write("error", msg, err, fields)
}

func (l *fileLogger) write(level, msg string, cause error, fields []any) {
	record := map[string]any{
		"time":    time.Now().UTC().Format(time.RFC3339Nano),
		"level":   level,
		"message": msg,
	}
	if cause != nil {
		record["error"] = cause.Error()
	}
	if len(fields) > 0 {
		values := make(map[string]any, (len(fields)+1)/2)
		for i := 0; i < len(fields); i += 2 {
			key := fmt.Sprint(fields[i])
			if i+1 == len(fields) {
				values[key] = "<missing>"
			} else {
				values[key] = fieldValue(fields[i+1])
			}
		}
		record["fields"] = values
	}
	line, err := json.Marshal(record)
	if err != nil || len(line) > maxRecordBytes {
		line, _ = json.Marshal(map[string]any{
			"time": time.Now().UTC().Format(time.RFC3339Nano), "level": level,
			"message": "diagnostic record omitted", "fields": map[string]any{"reason": "record exceeded encoding or size bound"},
		})
	}
	line = append(line, '\n')

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.lock == nil {
		return
	}
	if err := l.lockExclusive(); err != nil {
		return
	}
	defer l.unlock()
	if err := l.refresh(); err != nil {
		return
	}
	if l.size+int64(len(line)) > l.maxBytes {
		if err := l.rotate(); err != nil {
			return
		}
	}
	n, err := l.file.Write(line)
	l.size += int64(n)
	if err != nil {
		_ = l.file.Close()
		l.file = nil
	}
}

func (l *fileLogger) lockExclusive() error {
	if err := syscall.Flock(int(l.lock.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("applog: lock: %w", err)
	}
	return nil
}

func (l *fileLogger) unlock() {
	_ = syscall.Flock(int(l.lock.Fd()), syscall.LOCK_UN)
}

// refresh follows a rotation performed by another process and reloads the
// authoritative active size while the cross-process lock is held.
func (l *fileLogger) refresh() error {
	active, err := os.Stat(l.path)
	if errors.Is(err, os.ErrNotExist) {
		if l.file != nil {
			_ = l.file.Close()
			l.file = nil
		}
		return l.open()
	}
	if err != nil {
		return fmt.Errorf("applog: stat active log: %w", err)
	}
	if l.file == nil {
		return l.open()
	}
	opened, err := l.file.Stat()
	if err != nil || !os.SameFile(opened, active) {
		_ = l.file.Close()
		l.file = nil
		return l.open()
	}
	l.size = active.Size()
	return nil
}

func fieldValue(value any) any {
	if err, ok := value.(error); ok {
		return err.Error()
	}
	return value
}

func (l *fileLogger) open() error {
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("applog: open %s: %w", l.path, err)
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return fmt.Errorf("applog: protect %s: %w", l.path, err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return fmt.Errorf("applog: stat %s: %w", l.path, err)
	}
	l.file, l.size = f, info.Size()
	return nil
}

func (l *fileLogger) rotate() error {
	if l.file != nil {
		if err := l.file.Close(); err != nil {
			return fmt.Errorf("applog: close before rotation: %w", err)
		}
		l.file = nil
	}
	oldest := fmt.Sprintf("%s.%d", l.path, l.archives)
	if err := os.Remove(oldest); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("applog: remove oldest archive: %w", err)
	}
	for i := l.archives - 1; i >= 1; i-- {
		from, to := fmt.Sprintf("%s.%d", l.path, i), fmt.Sprintf("%s.%d", l.path, i+1)
		if err := os.Rename(from, to); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("applog: rotate archive: %w", err)
		}
	}
	if err := os.Rename(l.path, l.path+".1"); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("applog: rotate active log: %w", err)
	}
	return l.open()
}

func (l *fileLogger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	var first error
	if l.file != nil {
		first = l.file.Close()
		l.file = nil
	}
	if l.lock != nil {
		if err := l.lock.Close(); first == nil {
			first = err
		}
		l.lock = nil
	}
	return first
}
