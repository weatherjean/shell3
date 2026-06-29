package tui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/atotto/clipboard"
)

// copyToClipboard copies text via two transports so it works everywhere:
// tea.SetClipboard emits OSC52 (works over SSH and in OSC52-capable terminals),
// and atotto/clipboard shells out to the native clipboard (pbcopy / wl-copy /
// xclip), which covers terminals without OSC52 support — e.g. Apple Terminal.
// The native write is best-effort: a headless host or a missing helper binary
// is ignored.
func copyToClipboard(text string) tea.Cmd {
	return tea.Sequence(
		tea.SetClipboard(text),
		func() tea.Msg {
			_ = clipboard.WriteAll(text)
			return nil
		},
	)
}
