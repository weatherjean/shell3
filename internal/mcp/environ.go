package mcp

import "os"

// cmdEnviron is a seam so tests can keep the parent environment.
func cmdEnviron() []string { return os.Environ() }
