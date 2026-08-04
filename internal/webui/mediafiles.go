//go:build unix

package webui

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/weatherjean/shell3/internal/media"
)

// handleMediaList indexes the media directory: uploads the browser sent and
// images the agent generated. Without it a generated image is only reachable
// while its chat message is still on screen, even though the file is kept
// forever.
func (s *Server) handleMediaList(w http.ResponseWriter, _ *http.Request) {
	out := []fileEntry{}

	dir, err := media.Dir()
	if err != nil {
		writeJSON(w, map[string]any{"entries": out})
		return
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		writeJSON(w, map[string]any{"entries": out})
		return
	}

	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		entry := fileEntry{Name: e.Name(), Path: e.Name()}
		if info, err := e.Info(); err == nil {
			entry.Size = info.Size()
			entry.Modified = info.ModTime().UTC().Format(time.RFC3339)
		}
		out = append(out, entry)
	}
	// Newest first: the thing you just made is the thing you want.
	sort.Slice(out, func(i, j int) bool { return out[i].Modified > out[j].Modified })
	writeJSON(w, map[string]any{"entries": out})
}

// handleMediaFile serves one file from the media directory — the uploads the
// browser sent and the images the agent generated. This is what makes
// `![](/api/media/img-123.png)` in a reply render.
//
// Read-only, and flat by construction: the media dir has no subdirectories, so
// any path separator in the request is a traversal attempt and is refused
// outright rather than cleaned.
func (s *Server) handleMediaFile(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/api/media/")
	if name == "" || strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	dir, err := media.Dir()
	if err != nil {
		http.Error(w, "no media directory", http.StatusInternalServerError)
		return
	}
	full := filepath.Join(dir, name)

	info, err := os.Stat(full)
	if err != nil || info.IsDir() {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	// Media filenames carry a timestamp, so a given URL always names the same
	// bytes and can be cached hard.
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	http.ServeFile(w, r, full)
}
