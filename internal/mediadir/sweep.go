//go:build unix

package mediadir

import (
	"os"
	"path/filepath"
	"time"
)

// Sweep deletes regular files in dir whose mtime is older than keep before
// now. keep <= 0 means keep forever (no-op). Fail-open: an unremovable file
// is skipped and sweeping continues; the first error is returned for the
// caller's warning line. Subdirectories are left alone — the media dir is
// flat by construction.
func Sweep(dir string, keep time.Duration, now time.Time) (removed int, err error) {
	if keep <= 0 {
		return 0, nil
	}
	entries, rerr := os.ReadDir(dir)
	if rerr != nil {
		if os.IsNotExist(rerr) {
			return 0, nil
		}
		return 0, rerr
	}
	cutoff := now.Add(-keep)
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		info, ierr := e.Info()
		if ierr != nil || !info.ModTime().Before(cutoff) {
			continue
		}
		if derr := os.Remove(filepath.Join(dir, e.Name())); derr != nil {
			if err == nil {
				err = derr
			}
			continue
		}
		removed++
	}
	return removed, err
}
