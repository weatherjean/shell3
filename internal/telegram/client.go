//go:build unix

package telegram

import "context"

// Msg is an inbound message, normalized from a Telegram update.
type Msg struct {
	ChatID  int64
	Text    string
	ReplyTo string  // text of the message this replies to (Telegram reply/quote), for model context
	Media   []Media // photos/voice/documents already resolved to bytes
}

// Media is a downloaded attachment.
type Media struct {
	Bytes    []byte
	MIME     string // e.g. "image/jpeg", "audio/ogg"
	Filename string // suggested name (with extension) for saving to disk
}

// Command is one bot command shown in Telegram's "/" autocomplete menu.
type Command struct {
	Command     string // without leading slash, e.g. "clear"
	Description string
}

// Callback is an inline-keyboard button press, normalized from a Telegram
// callback query. ID acknowledges the press (stops the button spinner); Data is
// the pressed button's callback_data, which routes it to a pending Ask.
type Callback struct {
	ID   string
	Data string
}

// MenuOption is one inline-keyboard button in a SendMenu row: Label is the
// button text, Data is the callback_data returned via the Callbacks channel
// when the button is pressed.
type MenuOption struct {
	Label string
	Data  string
}

// tgClient is the transport surface the Bot depends on. The real impl wraps
// github.com/go-telegram/bot; tests inject a fake.
type tgClient interface {
	// Updates delivers normalized inbound messages until ctx is cancelled.
	Updates(ctx context.Context) <-chan Msg
	// Send posts plain text (no parse mode); returns the sent message id.
	Send(ctx context.Context, chatID int64, text string) (msgID int, err error)
	// SendHTML posts text with parse_mode=HTML. Callers must pass a valid
	// Telegram HTML subset; on any API error the caller should fall back to Send
	// with a plain-text version.
	SendHTML(ctx context.Context, chatID int64, html string) (msgID int, err error)
	// Typing shows the "typing…" chat action.
	Typing(ctx context.Context, chatID int64) error
	// SendDocument uploads a file to the chat with an optional caption.
	SendDocument(ctx context.Context, chatID int64, filename string, data []byte, caption string) error
	// SendPhoto uploads an image to the chat with an optional caption.
	SendPhoto(ctx context.Context, chatID int64, filename string, data []byte, caption string) error
	// SendVoice uploads a voice note (ogg/opus) to the chat with an optional caption.
	SendVoice(ctx context.Context, chatID int64, data []byte, caption string) error
	// SendAudio uploads a music/audio file to the chat with an optional caption.
	SendAudio(ctx context.Context, chatID int64, filename string, data []byte, caption string) error
	// SendVideo uploads a video file to the chat with an optional caption.
	SendVideo(ctx context.Context, chatID int64, filename string, data []byte, caption string) error
	// SendMenu posts text with one row of inline buttons; each option's Data is
	// returned via the Callbacks channel when pressed. Returns the sent message id.
	SendMenu(ctx context.Context, chatID int64, text string, options []MenuOption) (msgID int, err error)
	// SendConfirm posts text with two inline buttons (Allow/Deny) carrying the
	// given callback_data, and returns the sent message id so it can be edited
	// when the choice is made.
	SendConfirm(ctx context.Context, chatID int64, text, yesData, noData string) (msgID int, err error)
	// EditPlain replaces a message's text and removes its inline keyboard. Used
	// to make the confirm buttons disappear once a choice is made.
	EditPlain(ctx context.Context, chatID int64, msgID int, text string) error
	// AnswerCallback acknowledges a callback query, stopping the button's spinner.
	AnswerCallback(ctx context.Context, callbackID string) error
	// Callbacks returns the inline-keyboard button-press channel, live for the
	// client's lifetime. Consumers stop reading on their own ctx.
	Callbacks(ctx context.Context) <-chan Callback
}
