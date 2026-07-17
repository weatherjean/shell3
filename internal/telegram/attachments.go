//go:build unix

package telegram

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/weatherjean/shell3/internal/media"
)

// savedFile is one attachment written to disk for the agent to inspect.
type savedFile struct {
	Name string
	MIME string
	Size int
	Path string
}

// saveAttachments writes each downloaded attachment to shell3's durable
// media directory (~/.shell3/media — see media.Dir) and returns the
// saved-file metadata, so every file the user has sent keeps a stable path
// the agent can re-read or re-send later. The tg-* prefix distinguishes
// uploads from img-* generated files. Files that fail to write are skipped.
func saveAttachments(files []Media) []savedFile {
	if len(files) == 0 {
		return nil
	}
	dir, err := media.Dir()
	if err != nil {
		// Fall back to the old temp location rather than dropping the files.
		dir = filepath.Join(os.TempDir(), "shell3-telegram")
		_ = os.MkdirAll(dir, 0o755)
	}
	var out []savedFile
	for _, m := range files {
		ext := filepath.Ext(m.Filename)
		f, err := os.CreateTemp(dir, "tg-*"+ext)
		if err != nil {
			continue
		}
		_, werr := f.Write(m.Bytes)
		cerr := f.Close()
		if werr != nil || cerr != nil {
			_ = os.Remove(f.Name())
			continue
		}
		name := m.Filename
		if name == "" {
			name = filepath.Base(f.Name())
		}
		out = append(out, savedFile{Name: name, MIME: m.MIME, Size: len(m.Bytes), Path: f.Name()})
	}
	return out
}

// attachmentNote turns saved attachments into a text note for the agent,
// naming read_media only when that tool is actually enabled for the agent.
func attachmentNote(saved []savedFile, hasReadMedia bool) string {
	if len(saved) == 0 {
		return ""
	}
	how := "Use `bash` (cat, file, …) to inspect them."
	if hasReadMedia {
		how = "Use the `read_media` tool to view images/audio, or `bash` for other files."
	}
	var lines []string
	for _, s := range saved {
		lines = append(lines, fmt.Sprintf("- %s (%s, %s) saved at %s", s.Name, s.MIME, humanBytes(s.Size), s.Path))
	}
	noun := "a file"
	if len(saved) > 1 {
		noun = fmt.Sprintf("%d files", len(saved))
	}
	return fmt.Sprintf("[The user sent %s. %s\n%s]", noun, how, strings.Join(lines, "\n"))
}

func humanBytes(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
