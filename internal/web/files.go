//go:build unix

package web

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// maxFileBytes caps how much of a file the reader returns; larger files are
// truncated (the explorer is for glancing at config, not dumping blobs).
const maxFileBytes = 256 * 1024

// fileEntry is one row in a directory listing.
type fileEntry struct {
	Name     string `json:"name"`
	Dir      bool   `json:"dir"`
	Size     int64  `json:"size"`
	Redacted bool   `json:"redacted,omitempty"` // credential file — contents withheld on read
}

// filesResp is the directory-listing DTO. Path is the requested directory
// relative to the config root ("" = root itself).
type filesResp struct {
	Path    string      `json:"path"`
	Entries []fileEntry `json:"entries"`
}

// fileResp is the single-file-read DTO.
type fileResp struct {
	Path      string `json:"path"`
	Content   string `json:"content"`
	Size      int64  `json:"size"`
	Redacted  bool   `json:"redacted,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
	Binary    bool   `json:"binary,omitempty"`
}

// isCredentialFile reports whether a base name is a secrets file whose contents
// must never be sent to the browser. Mirrors the guard in sendtool.go: the
// `.env` beside shell3.lua and any legacy ai-do-not-read.* file.
func isCredentialFile(base string) bool {
	lower := strings.ToLower(base)
	return lower == ".env" || strings.HasPrefix(lower, "ai-do-not-read")
}

// resolveInConfig maps a browser-supplied relative path to an absolute path
// guaranteed to live inside the config root, with symlinks resolved. The
// leading-slash Clean trick clamps any `../` escape at the root; the final
// EvalSymlinks + prefix check defends against symlinks that point outside.
// Returns ("", false) when no config dir is set or the path escapes/does not
// exist.
func (s *Server) resolveInConfig(rel string) (string, bool) {
	if s.configDir == "" {
		return "", false
	}
	root, err := filepath.EvalSymlinks(s.configDir)
	if err != nil {
		return "", false
	}
	// Clean("/"+rel) collapses any ".." that would climb above the root.
	full := filepath.Join(root, filepath.Clean("/"+rel))
	resolved, err := filepath.EvalSymlinks(full)
	if err != nil {
		return "", false // missing or unreadable
	}
	if resolved != root && !strings.HasPrefix(resolved, root+string(os.PathSeparator)) {
		return "", false // symlink escaped the root
	}
	return resolved, true
}

// handleFiles lists a directory under the config root (?path=<rel>, default
// root). Directories sort before files, each alphabetical.
func (s *Server) handleFiles(w http.ResponseWriter, r *http.Request) {
	rel := r.URL.Query().Get("path")

	out := filesResp{Path: filepath.Clean("/" + rel)[1:], Entries: []fileEntry{}}
	if s.configDir == "" {
		writeJSON(w, out) // dashboard ran without a config dir
		return
	}
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
	for _, e := range ents {
		fe := fileEntry{Name: e.Name(), Dir: e.IsDir()}
		if info, err := e.Info(); err == nil {
			fe.Size = info.Size()
		}
		if !fe.Dir && isCredentialFile(e.Name()) {
			fe.Redacted = true
		}
		out.Entries = append(out.Entries, fe)
	}
	sort.Slice(out.Entries, func(i, j int) bool {
		a, b := out.Entries[i], out.Entries[j]
		if a.Dir != b.Dir {
			return a.Dir // dirs first
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
	writeJSON(w, out)
}

// handleFile returns the contents of one file under the config root
// (?path=<rel>). Credential files are redacted; binary and oversized files are
// flagged rather than dumped.
func (s *Server) handleFile(w http.ResponseWriter, r *http.Request) {
	rel := r.URL.Query().Get("path")

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
	out := fileResp{Path: filepath.Clean("/" + rel)[1:], Size: info.Size()}

	// Never read a secrets file: report it as redacted without touching disk.
	if isCredentialFile(filepath.Base(full)) {
		out.Redacted = true
		out.Content = "🔒 redacted — credential file (contents withheld)"
		writeJSON(w, out)
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
	out.Truncated = info.Size() > int64(n)
	if bytes.IndexByte(data, 0) >= 0 {
		out.Binary = true
		out.Content = "(binary file — not shown)"
	} else {
		out.Content = string(data)
	}
	writeJSON(w, out)
}
