//go:build unix

package render

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// This file renders the dash's read-only config-file explorer: a directory
// listing (FilesListHTML) and a single-file view (FileViewHTML), both rooted
// at the config dir and reachable only behind the dash token gate.
//
// The security model is ported verbatim from the old Telegram Mini App
// dashboard's files.go, because it was the piece an adversarial review
// hardened: path traversal is clamped by a leading-slash Clean AND an
// EvalSymlinks + root-prefix check (a symlink cannot point out of the root),
// and credential files are reported as redacted WITHOUT their contents ever
// being read from disk. Do not "simplify" either guard.

// maxFileBytes caps how much of a file the viewer shows; larger files are
// truncated (the explorer is for glancing at config, not dumping blobs).
const maxFileBytes = 256 * 1024

// isCredentialFile reports whether a base name is a secrets file whose
// contents must never be sent to the browser — the `.env` beside shell3.sh and
// any legacy ai-do-not-read.* file. Mirrors the guard in the send-media host
// tool: these are listed but never read.
func isCredentialFile(base string) bool {
	lower := strings.ToLower(base)
	return lower == ".env" || strings.HasPrefix(lower, ".env.") ||
		strings.HasPrefix(lower, "ai-do-not-read")
}

// resolveInConfig maps a browser-supplied relative path to an absolute path
// guaranteed to live inside the config root, with symlinks resolved. The
// leading-slash Clean clamps any `../` escape at the root; the final
// EvalSymlinks + prefix check defends against a symlink that points outside.
// Returns ("", false) when no config dir is set or the path escapes/does not
// exist.
func resolveInConfig(configDir, rel string) (string, bool) {
	if configDir == "" {
		return "", false
	}
	root, err := filepath.EvalSymlinks(configDir)
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

// cleanRel normalises a browser-supplied relative path to a root-relative
// display string ("" = the root itself), stripping any climb above the root.
func cleanRel(rel string) string {
	return filepath.Clean("/" + rel)[1:]
}

// FilesListHTML renders a directory listing under the config root as a dash
// fragment. rel is the directory relative to the root ("" = root). ok reports
// whether the path resolved — a false lets the caller answer 404.
func FilesListHTML(configDir, rel, tok string) (frag string, ok bool) {
	if configDir == "" {
		return "<section><h1>Files</h1><p class=\"meta\">No config dir.</p></section>\n", true
	}
	dir, resolved := resolveInConfig(configDir, rel)
	if !resolved {
		return "", false
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return "", false
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	rel = cleanRel(rel)

	type row struct {
		name     string
		isDir    bool
		size     int64
		redacted bool
	}
	rows := make([]row, 0, len(ents))
	for _, e := range ents {
		r := row{name: e.Name(), isDir: e.IsDir()}
		if fi, err := e.Info(); err == nil {
			r.size = fi.Size()
		}
		if !r.isDir && isCredentialFile(e.Name()) {
			r.redacted = true
		}
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].isDir != rows[j].isDir {
			return rows[i].isDir
		}
		return strings.ToLower(rows[i].name) < strings.ToLower(rows[j].name)
	})

	var b strings.Builder
	b.WriteString("<section>\n<h1>Files</h1>\n")
	crumb := "/"
	if rel != "" {
		crumb = "/" + rel
	}
	fmt.Fprintf(&b, "<p class=\"meta\"><code>%s</code></p>\n", esc(crumb))
	b.WriteString("<table>\n<tr><th>name</th><th>size</th></tr>\n")
	if rel != "" {
		parent := filepath.Dir(rel)
		if parent == "." {
			parent = ""
		}
		fmt.Fprintf(&b, "<tr><td><a href=\"/files?path=%s&amp;t=%s\">../</a></td><td></td></tr>\n",
			urlq(parent), esc(tok))
	}
	for _, r := range rows {
		child := r.name
		if rel != "" {
			child = rel + "/" + r.name
		}
		if r.isDir {
			fmt.Fprintf(&b, "<tr><td><a href=\"/files?path=%s&amp;t=%s\">%s/</a></td><td class=\"meta\">dir</td></tr>\n",
				urlq(child), esc(tok), esc(r.name))
			continue
		}
		label := esc(r.name)
		if r.redacted {
			label += " 🔒"
		}
		fmt.Fprintf(&b, "<tr><td><a href=\"/file?path=%s&amp;t=%s\">%s</a></td><td class=\"meta\">%s</td></tr>\n",
			urlq(child), esc(tok), label, esc(humanSize(r.size)))
	}
	b.WriteString("</table>\n</section>\n")
	return b.String(), true
}

// FileViewHTML renders one file under the config root as a dash fragment.
// Credential files are shown as redacted without their contents being read;
// binary and oversized files are flagged, not dumped.
func FileViewHTML(configDir, rel, tok string) (frag string, ok bool) {
	full, resolved := resolveInConfig(configDir, rel)
	if !resolved {
		return "", false
	}
	info, err := os.Stat(full)
	if err != nil || info.IsDir() {
		return "", false
	}
	rel = cleanRel(rel)
	parent := filepath.Dir(rel)
	if parent == "." {
		parent = ""
	}

	var b strings.Builder
	b.WriteString("<section>\n<h1>Files</h1>\n")
	fmt.Fprintf(&b, "<p class=\"meta\"><a href=\"/files?path=%s&amp;t=%s\">← %s</a></p>\n",
		urlq(parent), esc(tok), esc("/"+rel))

	// Never read a secrets file: report it redacted without touching disk.
	if isCredentialFile(filepath.Base(full)) {
		b.WriteString("<p>🔒 redacted — credential file (contents withheld).</p>\n</section>\n")
		return b.String(), true
	}

	f, err := os.Open(full)
	if err != nil {
		return "", false
	}
	defer f.Close()
	// Read one byte past the cap so a partial os.File.Read can't fool the
	// truncation check: len(data) > maxFileBytes means there was more.
	data, err := io.ReadAll(io.LimitReader(f, maxFileBytes+1))
	if err != nil {
		return "", false
	}
	truncated := false
	if len(data) > maxFileBytes {
		data = data[:maxFileBytes]
		truncated = true
	}
	if bytes.IndexByte(data, 0) >= 0 {
		fmt.Fprintf(&b, "<p class=\"meta\">binary file (%s) — not shown</p>\n</section>\n", esc(humanSize(info.Size())))
		return b.String(), true
	}
	fmt.Fprintf(&b, "<pre><code>%s</code></pre>\n", esc(string(data)))
	if truncated {
		fmt.Fprintf(&b, "<p class=\"meta\">truncated at %s of %s</p>\n", esc(humanSize(int64(len(data)))), esc(humanSize(info.Size())))
	}
	b.WriteString("</section>\n")
	return b.String(), true
}

// urlq percent-encodes a path for a query-string value (spaces, &, etc.),
// keeping slashes readable.
func urlq(s string) string {
	var b strings.Builder
	for _, r := range []byte(s) {
		if r == '/' || r == '-' || r == '_' || r == '.' ||
			(r >= '0' && r <= '9') || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
			b.WriteByte(r)
			continue
		}
		fmt.Fprintf(&b, "%%%02X", r)
	}
	return b.String()
}

// humanSize renders a byte count compactly (e.g. "1.2K", "3.4M").
func humanSize(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%dB", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1fK", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1fM", float64(n)/(1024*1024))
	}
}
