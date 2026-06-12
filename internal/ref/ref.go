package ref

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/google/uuid"

	"github.com/weatherjean/shell3/internal/paths"
)

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

// Init returns the project UUID for this cwd, minting and writing .ref on first
// use. Idempotent. The UUID is now purely a namespacing key for the single
// canonical DB — no project dir or meta is created. O_EXCL serialises concurrent
// first-use in the same cwd.
func Init(l paths.Local, g paths.Global) (string, error) {
	if id, err := Load(l); err != nil {
		return "", err
	} else if id != "" {
		return id, nil
	}
	id := uuid.New().String()
	if err := writeRefExcl(l.Ref, id); err != nil {
		if errors.Is(err, fs.ErrExist) {
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
