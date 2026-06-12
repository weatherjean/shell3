//go:build unix

package main

import (
	"strings"
	"testing"
)

func TestRunCommand_HasNewFlagsNotOld(t *testing.T) {
	cmd := newRunCommand()
	fs := cmd.Flags()
	for _, name := range []string{"prompt", "resume", "parent-session"} {
		if fs.Lookup(name) == nil {
			t.Errorf("run is missing --%s", name)
		}
	}
	for _, gone := range []string{"append-sinkfile", "no-subagents"} {
		if fs.Lookup(gone) != nil {
			t.Errorf("run still has retired flag --%s", gone)
		}
	}
	if cmd.Use == "" || strings.HasPrefix(cmd.Use, "shell3 ") {
		t.Errorf("unexpected Use %q", cmd.Use)
	}
}
