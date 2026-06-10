//go:build unix

package telegram

import "context"

// Msg is an inbound message, normalized from a Telegram update.
type Msg struct {
	ChatID   int64
	Text     string
	Media    []Media // photos/voice/documents already resolved to bytes
	Callback *Callback
}

// Media is a downloaded attachment.
type Media struct {
	Bytes    []byte
	MIME     string // e.g. "image/jpeg", "audio/ogg"
	Filename string // suggested name (with extension) for saving to disk
}

// Callback is an inline-button press.
type Callback struct {
	ID    string // callback query id (answer it to stop the spinner)
	Data  string // opaque payload we set on the button
	MsgID int    // message the buttons were attached to (to edit it)
}

// Command is one bot command shown in Telegram's "/" autocomplete menu.
type Command struct {
	Command     string // without leading slash, e.g. "clear"
	Description string
}

// Button is one inline keyboard button.
type Button struct {
	Text string
	Data string // callback payload (for approval-style buttons)
	// WebApp, when non-empty, makes this a Telegram Web App button that opens
	// the given HTTPS URL as a Mini App inside the client (Data is ignored).
	WebApp string
}

// tgClient is the transport surface the Bot depends on. The real impl wraps
// github.com/go-telegram/bot; tests inject a fake.
type tgClient interface {
	// Updates delivers normalized inbound messages until ctx is cancelled.
	Updates(ctx context.Context) <-chan Msg
	// Send posts text; returns the sent message id. buttons optional (one row).
	Send(ctx context.Context, chatID int64, text string, buttons []Button) (msgID int, err error)
	// EditText replaces a message's text and clears its buttons.
	EditText(ctx context.Context, chatID int64, msgID int, text string) error
	// Typing shows the "typing…" chat action.
	Typing(ctx context.Context, chatID int64) error
	// AnswerCallback acknowledges a button press (stops the client spinner).
	AnswerCallback(ctx context.Context, callbackID string) error
	// SendDocument uploads a file to the chat with an optional caption.
	SendDocument(ctx context.Context, chatID int64, filename string, data []byte, caption string) error
}
