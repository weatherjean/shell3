// Package fsx defines the pluggable file-I/O backend used by the read and
// edit_file tools. The OS backend does direct disk I/O; other backends (e.g.
// the ACP editor bridge) route reads/writes elsewhere. It is a leaf package:
// standard library only, so both internal/chat and internal/edittool can import
// it without a cycle.
package fsx

import (
	"context"
	"errors"
)

// ErrIsDir is returned by a FileSystem's ReadTextFile when the path is a
// directory. Callers detect it with errors.Is(err, ErrIsDir).
var ErrIsDir = errors.New("is a directory")

// FileSystem is whole-file text I/O over absolute paths. Path resolution
// (~ expansion, workdir joining) is the caller's job. ReadTextFile returns
// os.ErrNotExist for a missing file and ErrIsDir for a directory.
type FileSystem interface {
	ReadTextFile(ctx context.Context, absPath string) (string, error)
	WriteTextFile(ctx context.Context, absPath, content string) error
}
