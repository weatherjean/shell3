//go:build unix

package web

import "embed"

//go:embed static/index.html
var indexHTML []byte

// staticFS serves the vendored frontend assets (e.g. highlight.js) under
// /static/. Kept separate from indexHTML, which is served inline at "/".
//
//go:embed static/vendor
var staticFS embed.FS
