package dialog

import tea "charm.land/bubbletea/v2"

// Action is the result of a dialog handling a message.
type Action any

// Dialog is a modal component rendered over the main TUI.
type Dialog interface {
	ID() string
	// HandleMsg processes a message. Returns non-nil Action when the dialog
	// consumes the message; nil means the caller may pass it onward.
	HandleMsg(msg tea.Msg) Action
	// View renders the dialog given the terminal dimensions.
	View(width, height int) string
}

// Overlay manages a stack of dialogs. The last entry is the active (front) dialog.
type Overlay struct {
	dialogs []Dialog
}

func NewOverlay() *Overlay { return &Overlay{} }

func (o *Overlay) HasDialogs() bool { return len(o.dialogs) > 0 }

func (o *Overlay) ContainsDialog(id string) bool {
	for _, d := range o.dialogs {
		if d.ID() == id {
			return true
		}
	}
	return false
}

func (o *Overlay) Open(d Dialog) { o.dialogs = append(o.dialogs, d) }

func (o *Overlay) Close(id string) {
	for i, d := range o.dialogs {
		if d.ID() == id {
			o.dialogs = append(o.dialogs[:i], o.dialogs[i+1:]...)
			return
		}
	}
}

func (o *Overlay) CloseFront() {
	if len(o.dialogs) > 0 {
		o.dialogs = o.dialogs[:len(o.dialogs)-1]
	}
}

// Front returns the active dialog, or nil.
func (o *Overlay) Front() Dialog {
	if len(o.dialogs) == 0 {
		return nil
	}
	return o.dialogs[len(o.dialogs)-1]
}

// Update routes msg to the front dialog and returns its action.
// Returns nil if no dialogs are open.
func (o *Overlay) Update(msg tea.Msg) Action {
	d := o.Front()
	if d == nil {
		return nil
	}
	return d.HandleMsg(msg)
}

// View renders the front dialog within the given dimensions.
// Returns empty string if no dialogs are open.
func (o *Overlay) View(width, height int) string {
	d := o.Front()
	if d == nil {
		return ""
	}
	return d.View(width, height)
}
