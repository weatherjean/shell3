package runs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/weatherjean/shell3/internal/llm"
)

// healTornTail repairs a crash-left partial record before an append. A write
// cut off mid-way (power loss, SIGKILL) leaves a record with no terminating
// newline; if the next append landed after it via O_APPEND, the two would fuse
// into one line, and once a further complete line followed, that fused interior
// line would fail the whole-session strict decode — silently dropping ALL
// history. Truncating the unterminated tail back to the last complete line
// keeps the append on a clean record boundary. The common case (file already
// ends in a newline) is a stat + single-byte read; the full read + truncate
// only runs on the rare torn tail. Safe because each session file has a single
// writer (its turn goroutine).
func healTornTail(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0o644)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // appendLine's O_CREATE will make it
		}
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.Size() == 0 {
		return nil
	}
	last := make([]byte, 1)
	if _, err := f.ReadAt(last, info.Size()-1); err != nil {
		return err
	}
	if last[0] == '\n' {
		return nil // clean boundary — the normal path
	}
	data := make([]byte, info.Size())
	if _, err := f.ReadAt(data, 0); err != nil {
		return err
	}
	keep := int64(bytes.LastIndexByte(data, '\n') + 1) // 0 when there is no newline at all
	return f.Truncate(keep)
}

// appendLine marshals v and appends it as a single JSON line to path,
// creating the file when missing. what names the record kind in errors
// ("message", "reminder").
func appendLine(path, what string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("runs: marshal %s: %w", what, err)
	}
	// Heal any crash-left partial tail so O_APPEND lands on a clean boundary.
	if err := healTornTail(path); err != nil {
		return fmt.Errorf("runs: repair %s: %w", what, err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("runs: open %s: %w", what, err)
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		_ = f.Close()
		return fmt.Errorf("runs: append %s: %w", what, err)
	}
	// Close is the last chance to observe a flush failure on this append-only
	// file — swallowing it would hide a full disk from the caller.
	if err := f.Close(); err != nil {
		return fmt.Errorf("runs: close %s: %w", what, err)
	}
	return nil
}

// decodeLines leniently unmarshals each non-empty line of a JSONL blob into
// T, skipping malformed lines — for renderers that must tolerate a
// still-streaming or hand-edited file. The resume path uses the stricter
// decodeLinesTolerantTail instead.
func decodeLines[T any](raw string) []T {
	var out []T
	for _, line := range strings.Split(strings.TrimRight(raw, "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var v T
		if err := json.Unmarshal([]byte(line), &v); err != nil {
			continue
		}
		out = append(out, v)
	}
	return out
}

// decodeLinesTolerantTail decodes strictly except for the FINAL line, which
// may be a half-written tail from a crash mid-append; a malformed tail is
// dropped rather than failing the whole decode. Interior corruption still
// errors. Used by the resume path (Store.LoadMessages).
func decodeLinesTolerantTail[T any](raw string) ([]T, error) {
	lines := strings.Split(strings.TrimRight(raw, "\n"), "\n")
	var out []T
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var v T
		if err := json.Unmarshal([]byte(line), &v); err != nil {
			if i == len(lines)-1 {
				break // half-written tail: tolerate
			}
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// ParseMessages leniently parses a messages.jsonl blob (one llm.Message per
// line), skipping blank and malformed lines. For transcript renderers that
// must tolerate a still-streaming file; the strict counterpart is
// Store.LoadMessages.
func ParseMessages(raw string) []llm.Message {
	return decodeLines[llm.Message](raw)
}
