---
name: go-conventions
description: Go coding and testing conventions for this project
---

# Go Conventions

- Always run `go vet ./...` and `go test ./...` before considering any change complete.
- Use `go fmt` — never check in unformatted code.
- For test files, prefer table-driven tests.
- For error messages, use lowercase (no period at end), per Go convention.
- When editing, prefer minimal changes — don't rewrite working code.
- After any code change, run: `go vet ./... && go test ./...`
