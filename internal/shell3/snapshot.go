package shell3

// ToolInfo names an exposed tool and its one-line description.
type ToolInfo struct {
	Name        string
	Description string
}

// Snapshot is a point-in-time copy of the session's model configuration.
// It is safe to call concurrently with a running turn.
type Snapshot struct {
	ContextWindow int
	// Tools lists the orchestrator's advertised tools.
	Tools []ToolInfo
}

// Snapshot returns the current agent state (see Snapshot).
func (s *Session) Snapshot() Snapshot {
	// Copy the cfg fields out under s.mu so a concurrent cfg writer (reload or
	// RegisterHostTool, both between turns) can't race the read. Release
	// before collecting the tool schema.
	s.mu.Lock()
	snap := Snapshot{
		ContextWindow: s.cfg.ContextWindow,
	}
	for _, t := range s.cfg.Profile.Tools {
		snap.Tools = append(snap.Tools, ToolInfo{Name: t.Name, Description: t.Description})
	}
	s.mu.Unlock()

	return snap
}
