//go:build unix

package webui

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The read-only config-directory explorer. Same guarantees as always: nothing
// outside the config root is reachable, and a credential file is reported
// without ever being opened.

// maxFileBytes caps how much of a file the reader returns; larger files are
// truncated (this is for glancing at config, not dumping blobs).
const maxFileBytes = 256 * 1024

type fileEntry struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Dir      bool   `json:"dir"`
	Size     int64  `json:"size"`
	Modified string `json:"modified"`
	Redacted bool   `json:"redacted"`
}

// isCredentialFile reports whether a base name is a secrets file whose
// contents must never be sent to the browser: the `.env` beside shell3.yaml,
// plus the dotenv siblings operators commonly create.
func isCredentialFile(base string) bool {
	b := strings.ToLower(base)
	return b == ".env" || strings.HasPrefix(b, ".env.")
}

// resolveInConfig maps a browser-supplied relative path to an absolute path
// guaranteed to live inside the config root, with symlinks resolved. The
// leading-slash Clean clamps any `../` escape at the root; the final
// EvalSymlinks + prefix check defends against symlinks pointing outside.
func (s *Server) resolveInConfig(rel string) (string, bool) {
	if s.configDir == "" {
		return "", false
	}
	root, err := filepath.EvalSymlinks(s.configDir)
	if err != nil {
		return "", false
	}
	full := filepath.Join(root, filepath.Clean("/"+rel))
	resolved, err := filepath.EvalSymlinks(full)
	if err != nil {
		return "", false
	}
	if resolved != root && !strings.HasPrefix(resolved, root+string(os.PathSeparator)) {
		return "", false
	}
	return resolved, true
}

// cleanRel normalizes a request path to a root-relative slug ("" for the root).
func cleanRel(rel string) string {
	return strings.TrimPrefix(filepath.Clean("/"+rel), "/")
}

// handleFiles lists a directory under the config root (?path=<rel>, default
// root). Directories sort before files, each alphabetical.
func (s *Server) handleFiles(w http.ResponseWriter, r *http.Request) {
	rel := cleanRel(r.URL.Query().Get("path"))

	dir, ok := s.resolveInConfig(rel)
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		http.Error(w, "not a directory", http.StatusBadRequest)
		return
	}

	entries := make([]fileEntry, 0, len(ents))
	for _, e := range ents {
		entry := fileEntry{
			Name: e.Name(),
			Path: strings.TrimPrefix(filepath.Join(rel, e.Name()), "/"),
			Dir:  e.IsDir(),
		}
		if info, err := e.Info(); err == nil {
			entry.Size = info.Size()
			entry.Modified = info.ModTime().UTC().Format(time.RFC3339)
		}
		entry.Redacted = !entry.Dir && isCredentialFile(e.Name())
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.Dir != b.Dir {
			return a.Dir
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})

	var parent *string
	if rel != "" {
		up := filepath.Dir(rel)
		if up == "." {
			up = ""
		}
		parent = &up
	}
	writeJSON(w, map[string]any{"path": rel, "parent": parent, "entries": entries})
}

// handleFileContent returns one file's contents (?path=<rel>). Credential
// files are reported redacted without being read; binary and oversized files
// are flagged rather than dumped.
func (s *Server) handleFileContent(w http.ResponseWriter, r *http.Request) {
	rel := cleanRel(r.URL.Query().Get("path"))

	full, ok := s.resolveInConfig(rel)
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	info, err := os.Stat(full)
	if err != nil || info.IsDir() {
		http.Error(w, "not a file", http.StatusBadRequest)
		return
	}

	if isCredentialFile(filepath.Base(full)) {
		writeJSON(w, map[string]any{
			"path":      rel,
			"content":   "This file holds credentials and is never read from disk.",
			"size":      info.Size(),
			"redacted":  true,
			"truncated": false,
			"binary":    false,
		})
		return
	}

	f, err := os.Open(full)
	if err != nil {
		http.Error(w, "cannot read", http.StatusInternalServerError)
		return
	}
	defer f.Close()

	buf := make([]byte, maxFileBytes)
	n, _ := f.Read(buf)
	data := buf[:n]

	binary := bytes.IndexByte(data, 0) >= 0
	content := string(data)
	if binary {
		content = ""
	}
	writeJSON(w, map[string]any{
		"path":      rel,
		"content":   content,
		"size":      info.Size(),
		"redacted":  false,
		"binary":    binary,
		"truncated": !binary && info.Size() > int64(n),
	})
}
