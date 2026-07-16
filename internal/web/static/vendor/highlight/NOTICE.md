# Vendored: highlight.js

Syntax highlighting for the Telegram dashboard's read-only file viewer.
Vendored (rather than loaded from a CDN) so the dashboard has no third-party
runtime dependency and works on an offline tunnel.

- **Project:** highlight.js — https://highlightjs.org
- **Source:** https://github.com/highlightjs/highlight.js
- **Version:** 11.9.0 (common build — includes lua, markdown, python, javascript, typescript, json, yaml, and others)
- **License:** BSD-3-Clause (see `LICENSE` in this directory)
- **Fetched from:** https://cdnjs.cloudflare.com/ajax/libs/highlight.js/11.9.0/

Files:
- `highlight.min.js` — core + common language grammars
- `github.min.css` / `github-dark.min.css` — themes (selected at runtime by Telegram color scheme)

To update: re-fetch the same three files at the new version from cdnjs and bump
the version above.
