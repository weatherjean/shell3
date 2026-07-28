package runs

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Sweep deletes runs/<id>/ directories under root (the .shell3_project dir —
// the same root Open takes) whose newest-file mtime is older than
// now.Add(-keep). keep<=0 is a deliberate "keep forever" (the shell3.yaml
// runs_keep_days: 0 case), not an instant-expiry edge case: Sweep returns
// immediately with no directory walk at all.
//
// Age is the NEWEST mtime among the files inside a run dir, not the dir's own
// mtime (which some filesystems never bump on a nested write) and not any one
// fixed file (meta.json is written once at session start; messages.jsonl is
// what actually advances as a session runs) — so a session still being
// appended to is never swept out from under a live turn.
//
// Returns the ids of the removed sessions. runs deliberately knows nothing
// about the web thread index; a caller that also wants to drop thread entries
// pointing at a removed session does so itself (see webui.PruneThreadIndex),
// keeping the two packages decoupled.
//
// Sweep is per-dir fail-open: a dir whose mtime can't be read or whose
// removal fails is skipped (not fatal) and the sweep continues over the rest
// — one unreadable or undeletable run dir is cosmetic hygiene, never worth
// blocking startup over. The first such error is returned alongside whatever
// ids WERE removed, purely for the caller to report; a non-nil error here
// does not mean removedIDs is incomplete or wrong.
func Sweep(root string, keep time.Duration, now time.Time) (removedIDs []string, err error) {
	if keep <= 0 {
		return nil, nil
	}
	dir := filepath.Join(root, "runs")
	ents, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("runs: sweep %s: %w", dir, err)
	}
	cutoff := now.Add(-keep)
	var firstErr error
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		id := e.Name()
		sessDir := filepath.Join(dir, id)
		newest, nerr := newestMtime(sessDir)
		if nerr != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("runs: sweep %s: %w", sessDir, nerr)
			}
			continue
		}
		if newest.Before(cutoff) {
			if rerr := os.RemoveAll(sessDir); rerr != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("runs: sweep remove %s: %w", sessDir, rerr)
				}
				continue
			}
			removedIDs = append(removedIDs, id)
		}
	}
	return removedIDs, firstErr
}

// newestMtime returns the latest mtime among all regular files under dir
// (recursively, though run dirs are flat in practice). A dir with no files at
// all reports the zero time, which sorts as arbitrarily old — an empty stray
// dir is swept like anything else past the cutoff.
func newestMtime(dir string) (time.Time, error) {
	var newest time.Time
	err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
		return nil
	})
	return newest, err
}
