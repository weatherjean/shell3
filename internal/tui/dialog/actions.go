package dialog

import tea "charm.land/bubbletea/v2"

// ActionClose closes the front dialog.
type ActionClose struct{}

// ActionOpenDialog requests opening a dialog by ID. Callers that need to open
// a specific pre-built dialog can return this from HandleMsg.
type ActionOpenDialog struct{ DialogID string }

// ActionCmd carries a tea.Cmd to be executed by the TUI after the action is handled.
type ActionCmd struct{ Cmd tea.Cmd }
