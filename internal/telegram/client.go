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
}
