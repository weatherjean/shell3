//go:build unix

package webui

import (
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"strings"
)

// Go's built-in MIME table doesn't know the PWA manifest extension, and
// http.FileServer would fall back to sniffing — Chrome then refuses to treat
// the page as installable.
func init() {
	_ = mime.AddExtensionType(".webmanifest", "application/manifest+json")
}

// The built single-page app, compiled into the binary so a shell3 install is
// one file. The source lives in webui/ at the repo root; `make webui` builds it
// and copies the output here. Committed on purpose — `go install` has no way to
// run npm.
//
//go:embed all:dist
var distFS embed.FS

// appHandler serves the app: hashed assets with a long cache, and index.html
// for everything else so client-side routes survive a refresh.
func appHandler() http.Handler {
	root, err := fs.Sub(distFS, "dist")
	if err != nil {
		return http.HandlerFunc(missingBuild)
	}
	index, err := fs.ReadFile(root, "index.html")
	if err != nil {
		return http.HandlerFunc(missingBuild)
	}
	files := http.FileServer(http.FS(root))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path != "" {
			if f, err := root.Open(path); err == nil {
				f.Close()
				// Vite fingerprints asset filenames, so they are immutable.
				if strings.HasPrefix(path, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(index)
	})
}

func missingBuild(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte(`<!doctype html><meta charset="utf-8">
<title>shell3</title>
<body style="font:16px system-ui;max-width:34rem;margin:4rem auto;padding:0 1rem">
<h1>The interface isn't built</h1>
<p>This binary was compiled without the web app. Run <code>make webui</code>
and rebuild to bundle it.</p>
</body>`))
}
