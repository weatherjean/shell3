package tui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/weatherjean/shell3/internal/llm"
)

// ChunkMsg carries one streaming text delta from the LLM.
type ChunkMsg string

// TurnDoneMsg signals that the current LLM turn completed.
type TurnDoneMsg struct{ Usage llm.Usage }

// TurnErrMsg carries an error to display inline.
type TurnErrMsg struct{ Err error }

// AppendMsg appends pre-formatted text to the nonLLM buffer.
type AppendMsg string

// StatusMsg replaces the status bar text.
type StatusMsg string

// SetCancelMsg delivers a cancel func for the active turn to the model.
type SetCancelMsg struct{ Cancel func() }

// TTYExecMsg requests the TUI to suspend and hand the terminal to a command.
// The caller blocks on ReplyC until the command finishes. This is the correct
// mechanism for tools that need an interactive terminal (editors, pagers, etc.).
// The goroutine that sends TTYExecMsg must block on <-ReplyC before proceeding.
type TTYExecMsg struct {
	Cmd     string // shell command to execute
	WorkDir string // working directory; empty = inherit
	ReplyC  chan<- string
}

// resumeStreamMsg is sent internally after a TTY exec completes.
// It signals the model to resume draining the LLM stream channel.
type resumeStreamMsg struct{}

// shellDoneMsg is sent when a user-initiated ! shell command finishes.
// It unlocks the input and optionally appends an error message.
type shellDoneMsg struct{ errMsg string }

// streamMsg wraps a content message with a command to read the next item from the stream.
type streamMsg struct {
	msg  tea.Msg
	next tea.Cmd
}

// ReadCh returns a Cmd that reads one message from ch and wraps it in streamMsg.
// When ch is closed it delivers TurnDoneMsg{} with no next Cmd.
func ReadCh(ch <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		inner, ok := <-ch
		if !ok {
			return streamMsg{msg: TurnDoneMsg{}, next: nil}
		}
		return streamMsg{msg: inner, next: ReadCh(ch)}
	}
}
