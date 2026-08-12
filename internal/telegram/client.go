//go:build unix

package telegram

import "context"

// Msg is an inbound message, normalized from a transport update. Message ids
// are opaque strings owned by the transport: the Telegram client stringifies
// the API's int message_ids, the JSONL client passes the front-end's ids
// through verbatim, the console client renders its own counter.
type Msg struct {
	ChatID int64
	// SenderID is the Telegram user id of whoever sent this message, 0 when the
	// transport cannot tell (a channel post has no author). It is the thing
	// authorization is decided on: it comes from Telegram's servers and cannot
	// be set by the sender, unlike anything in the message text. In a DM it
	// equals ChatID; in a group it does not, which is the whole point — group
	// membership must not be authorization.
	SenderID  int64
	ID        string // this message's id ("" if unknown)
	ReplyToID string // id this replies to, for thread resolution ("" = not a reply)
	Text      string
	ReplyTo   string  // text of the message this replies to (Telegram reply/quote), for model context
	Media     []Media // photos/voice/documents already resolved to bytes
}

// Media is a downloaded attachment.
type Media struct {
	Bytes    []byte
	MIME     string // e.g. "image/jpeg", "audio/ogg"
	Filename string // suggested name (with extension) for saving to disk
}

// Command is one bot command shown in Telegram's "/" autocomplete menu.
type Command struct {
	Command     string `json:"command"` // without leading slash, e.g. "clear"
	Description string `json:"description"`
}

// SendOpt carries per-send delivery options. Variadic on the send methods so
// the many call sites that don't care stay unchanged.
type SendOpt struct {
	// Silent sends without a notification ping (Telegram
	// disable_notification): the message arrives, the phone stays quiet.
	Silent bool
}

// sendSilent reports whether any opt asks for a silent send.
func sendSilent(opts []SendOpt) bool {
	for _, o := range opts {
		if o.Silent {
			return true
		}
	}
	return false
}

// tgClient is the transport surface the Bot depends on. The real impl wraps
// github.com/go-telegram/bot; tests inject a fake.
type tgClient interface {
	// Updates delivers normalized inbound messages until ctx is cancelled.
	Updates(ctx context.Context) <-chan Msg
	// Send posts plain text (no parse mode); returns the sent message id.
	Send(ctx context.Context, chatID int64, text string, opts ...SendOpt) (msgID string, err error)
	// SendHTML posts text with parse_mode=HTML. Callers must pass a valid
	// Telegram HTML subset; on any API error the caller should fall back to Send
	// with a plain-text version.
	SendHTML(ctx context.Context, chatID int64, html string, opts ...SendOpt) (msgID string, err error)
	// SendReply posts plain text as a reply to replyTo, threading it in the
	// chat. If replyTo no longer exists (deleted message) it falls back to a
	// plain Send so a thread whose anchor vanished never fails a turn.
	SendReply(ctx context.Context, chatID int64, text string, replyTo string, opts ...SendOpt) (msgID string, err error)
	// SendHTMLReply is SendReply with parse_mode=HTML; callers fall back to
	// SendReply with plain text on an HTML rejection.
	SendHTMLReply(ctx context.Context, chatID int64, html string, replyTo string, opts ...SendOpt) (msgID string, err error)
	// Typing shows the "typing…" chat action.
	Typing(ctx context.Context, chatID int64) error
	// SendDocument uploads a file to the chat with an optional caption,
	// returning the sent message id so thread anchors can advance onto it.
	SendDocument(ctx context.Context, chatID int64, filename string, data []byte, caption string, opts ...SendOpt) (msgID string, err error)
	// SendPhoto uploads an image to the chat with an optional caption.
	SendPhoto(ctx context.Context, chatID int64, filename string, data []byte, caption string) error
	// SendVoice uploads a voice note (ogg/opus) to the chat with an optional caption.
	SendVoice(ctx context.Context, chatID int64, data []byte, caption string) error
	// SendAudio uploads a music/audio file to the chat with an optional caption.
	SendAudio(ctx context.Context, chatID int64, filename string, data []byte, caption string) error
	// SendVideo uploads a video file to the chat with an optional caption.
	SendVideo(ctx context.Context, chatID int64, filename string, data []byte, caption string) error
	// DeleteMessage removes a sent message (the progress bubble's cleanup).
	// Best-effort: an already-deleted or too-old message is not an error the
	// caller can act on.
	DeleteMessage(ctx context.Context, chatID int64, msgID string) error
	// EditPlain replaces a message's text (the progress bubble's own edits).
	EditPlain(ctx context.Context, chatID int64, msgID string, text string) error
}
