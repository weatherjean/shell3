// Package docs embeds the shell3 documentation markdown so any package
// (the CLI's `docs` subcommand and the agentsetup builder) can read it
// without depending on package main.
package docs

import _ "embed"

//go:embed shell3.md
var Content string
