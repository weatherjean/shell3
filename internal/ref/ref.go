package ref

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/weatherjean/shell3/internal/paths"
)

// Meta is the content of ~/.shell3/projects/<uuid>/meta.json.
// It lets AIs (and humans) restore the .ref file if lost.
type Meta struct {
	UUID      string    `json:"uuid"`
	CWD       string    `json:"cwd"`
	Name      string    `json:"name"` // basename of CWD
	CreatedAt time.Time `json:"created_at"`
}

// Load reads the UUID from l.Ref. Returns ("", nil) if the file is absent.
func Load(l paths.Local) (string, error) {
	b, err := os.ReadFile(l.Ref)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("ref: read: %w", err)
	}
	return strings.TrimSpace(string(b)), nil
}

// reloadWinner re-reads .ref after losing the create race to a concurrent
// Init. The winner's id must be present; an empty .ref means the winner
// has created but not yet written it, which we surface rather than return "".
func reloadWinner(l paths.Local) (string, error) {
	id, err := Load(l)
	if err != nil {
		return "", err
	}
	if id == "" {
		return "", fmt.Errorf("ref: .ref exists but is empty after create race")
	}
	return id, nil
}

// Init creates the .ref file and project dir if they don't exist.
// Idempotent: returns existing UUID if .ref already present.
//
// If .ref is absent but a meta.json already records this cwd (e.g. .ref was
// lost), the existing project UUID is recovered rather than minting a new one.
// The .ref file is created with O_EXCL so that, under concurrent Init calls in
// the same cwd, exactly one writer wins; losers re-read the winner's UUID and
// clean up any project dir they speculatively created.
func Init(l paths.Local, g paths.Global, cwd string) (string, error) {
	if id, err := Load(l); err != nil {
		return "", err
	} else if id != "" {
		return id, nil
	}

	// .ref is absent. Try to recover an existing project for this cwd before
	// minting a new one.
	found, err := FindByCWD(g, cwd)
	if err != nil {
		return "", err
	}
	if found != "" {
		// Reuse the existing project: just (atomically) write .ref. Do not
		// mint a new dir/meta.
		if err := writeRefExcl(l.Ref, found); err != nil {
			if errors.Is(err, fs.ErrExist) {
				// Another writer created .ref first; trust it.
				return reloadWinner(l)
			}
			return "", err
		}
		return found, nil
	}

	// No existing project: mint a new one.
	id := uuid.New().String()
	p := paths.NewProject(g, id)

	if err := os.MkdirAll(p.Dir, 0700); err != nil {
		return "", fmt.Errorf("ref: mkdir project dir: %w", err)
	}

	meta := Meta{
		UUID:      id,
		CWD:       cwd,
		Name:      filepath.Base(cwd),
		CreatedAt: time.Now().UTC(),
	}
	if err := writeMeta(p, meta); err != nil {
		_ = os.RemoveAll(p.Dir) // don't leave an orphan project dir behind
		return "", err
	}

	if err := writeRefExcl(l.Ref, id); err != nil {
		// On any failure after MkdirAll, remove the orphan project dir.
		_ = os.RemoveAll(p.Dir)
		if errors.Is(err, fs.ErrExist) {
			// Lost a race: another Init in this cwd created .ref first. Return
			// the winner's UUID (our dir/meta were just cleaned up).
			return reloadWinner(l)
		}
		return "", err
	}
	return id, nil
}

// writeRefExcl atomically creates l.Ref containing id, failing with an
// fs.ErrExist-wrapped error if the file already exists. The O_EXCL create is
// the serialization point for concurrent Init calls in the same cwd.
func writeRefExcl(refPath, id string) error {
	f, err := os.OpenFile(refPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("ref: create .ref: %w", err)
	}
	if _, err := f.WriteString(id + "\n"); err != nil {
		_ = f.Close()
		_ = os.Remove(refPath)
		return fmt.Errorf("ref: write .ref: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("ref: close .ref: %w", err)
	}
	return nil
}

// ReadMeta reads ~/.shell3/projects/<uuid>/meta.json.
func ReadMeta(p paths.Project) (Meta, error) {
	b, err := os.ReadFile(p.Meta)
	if err != nil {
		return Meta{}, fmt.Errorf("ref: read meta: %w", err)
	}
	var m Meta
	if err := json.Unmarshal(b, &m); err != nil {
		return Meta{}, fmt.Errorf("ref: parse meta: %w", err)
	}
	return m, nil
}

// FindByCWD scans ~/.shell3/projects/*/meta.json for a matching CWD.
// Returns ("", nil) if not found.
func FindByCWD(g paths.Global, cwd string) (string, error) {
	entries, err := os.ReadDir(g.Projects)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("ref: scan projects: %w", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p := paths.NewProject(g, e.Name())
		m, err := ReadMeta(p)
		if err != nil {
			// A project dir without a meta.json is simply not a match; skip it.
			// Any other error (corrupt JSON, permission denied) is real and
			// must be surfaced rather than masquerading as "no such project".
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return "", fmt.Errorf("ref: read meta %s: %w", p.Meta, err)
		}
		if m.CWD == cwd {
			return m.UUID, nil
		}
	}
	return "", nil
}

func writeMeta(p paths.Project, m Meta) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("ref: marshal meta: %w", err)
	}
	tmp := p.Meta + ".tmp"
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		return fmt.Errorf("ref: write meta tmp: %w", err)
	}
	defer func() { _ = os.Remove(tmp) }() // no-op if rename succeeded
	if err := os.Rename(tmp, p.Meta); err != nil {
		return fmt.Errorf("ref: rename meta: %w", err)
	}
	return nil
}
