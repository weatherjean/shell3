package ref

import (
	"encoding/json"
	"fmt"
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
	Name      string    `json:"name"`       // basename of CWD
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

// Init creates the .ref file and project dir if they don't exist.
// Idempotent: returns existing UUID if .ref already present.
func Init(l paths.Local, g paths.Global, cwd string) (string, error) {
	if id, err := Load(l); err != nil {
		return "", err
	} else if id != "" {
		return id, nil
	}

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
		return "", err
	}

	if err := os.WriteFile(l.Ref, []byte(id+"\n"), 0600); err != nil {
		return "", fmt.Errorf("ref: write .ref: %w", err)
	}
	return id, nil
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
			continue
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
	defer os.Remove(tmp) // no-op if rename succeeded
	if err := os.Rename(tmp, p.Meta); err != nil {
		return fmt.Errorf("ref: rename meta: %w", err)
	}
	return nil
}
