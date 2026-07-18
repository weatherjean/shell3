package chat

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// resolvePath expands ~ and resolves a relative path against workDir. Used by
// the media loaders (read_media) to resolve model-supplied paths.
func resolvePath(p, workDir string) string {
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, p[2:])
		}
	}
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(workDir, p)
}

// mediaTooLarge renders the shared over-cap error for every media loader,
// path- and bytes-based alike, with the max derived from the cap constant.
// The actual size rounds UP to whole MB so a file just over the cap never
// prints the same number as the max ("26 MB, max 25 MB", never "25 MB, max
// 25 MB").
func mediaTooLarge(kind string, size, maxBytes int64) error {
	return fmt.Errorf("%s too large (%d MB, max %d MB)", kind, (size+(1<<20)-1)>>20, maxBytes>>20)
}

// readMediaFile is the shared front half of the media loaders: it resolves
// path against workDir (~ expands), enforces the per-kind size cap, and reads
// the raw bytes. kind labels the too-large error ("image", "audio", "pdf",
// "video"). Returns the bytes and the resolved path.
func readMediaFile(path, workDir, kind string, maxBytes int64) ([]byte, string, error) {
	path = resolvePath(path, workDir)
	f, err := os.Open(path)
	if err != nil {
		return nil, path, fmt.Errorf("cannot read %q: %w", path, err)
	}
	defer f.Close()
	// Stat the open fd and cap the read itself: with a path-stat followed by a
	// separate read, a file that grows in between (or a swapped symlink) would
	// bypass the cap; reading through the same fd under a hard limit cannot.
	info, err := f.Stat()
	if err != nil {
		return nil, path, fmt.Errorf("cannot read %q: %w", path, err)
	}
	if info.Size() > maxBytes {
		return nil, path, mediaTooLarge(kind, info.Size(), maxBytes)
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		return nil, path, fmt.Errorf("cannot read %q: %w", path, err)
	}
	if int64(len(raw)) > maxBytes {
		return nil, path, mediaTooLarge(kind, int64(len(raw)), maxBytes)
	}
	return raw, path, nil
}
