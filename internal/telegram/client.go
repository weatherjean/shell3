//go:build unix

package telegram

import "context"

// Msg is an inbound message, normalized from a transport update. Message ids
// are opaque strings owned by the transport: the Telegram client stringifies
// the API's int message_ids, the console client renders its own counter.
type Msg struct {
	ChatID int64
	// SenderID is who sent this, 0 when the transport cannot tell (a channel
	// post has no author). Authorization is decided on it because Telegram
	// sets it and the sender cannot. In a DM it equals ChatID; in a group it
	// does not, which is the point — membership must not be authorization.
	SenderID  int64
	ID        string // this message's id ("" if unknown)
	ReplyToID string // id this replies to, for thread resolution ("" = not a reply)
	Text      string
	ReplyTo   string // text of the message this replies to (Telegram reply/quote), for model context
	// ReplyToBot: the replied-to message was sent by THIS bot. Taken from
	// Telegram's own author field, so it survives a restart — the in-process
	// record of what the bot sent is empty after a reboot, and a group whose
	// only trigger was "reply to me" would go deaf.
	ReplyToBot bool
	// MigratedTo is the new chat id when the group became a supergroup
	// (migrate_to_chat_id). Every id-keyed thing — the conversation, its
	// thread marker — must follow or it is stranded under a dead id.
	MigratedTo int64
	// ChatType is "private", "group", "supergroup" or "channel". Anything but
	// private holds other people, so a message must be addressed to the bot
	// to count (trigger.go).
	ChatType string
	Media    []Media // photos/voice/documents already resolved to bytes
	// HasMedia: the message carries an attachment, fetched or not.
	HasMedia bool
	// FetchMedia downloads the attachments; nil when there are none or a
	// transport already filled Media in. Deferred so it runs AFTER
	// authorization: with privacy mode off the bot receives every group
	// message, and fetching eagerly pulled every stranger's photo into
	// memory to discard it a moment later at the gate.
	FetchMedia func(ctx context.Context) []Media
}

// Media is a downloaded attachment.
type Media struct {
	Bytes    []byte
	MIME     string // e.g. "image/jpeg", "audio/ogg"
	Filename string // suggested name (with extension) for saving to disk
}

// Command is one bot command shown in Telegram's "/" autocomplete menu.
type Command struct {
	Command     string `json:"command"` // without leading slash, e.g. "new"
	Description string `json:"description"`
}

// SendOpt is per-send delivery options, variadic so call sites that do not
// care stay unchanged.
type SendOpt struct {
	// Silent is Telegram disable_notification: arrives, phone stays quiet.
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
	// SendHTML posts a valid Telegram HTML subset; callers fall back to Send
	// with plain text on any API error.
	SendHTML(ctx context.Context, chatID int64, html string, opts ...SendOpt) (msgID string, err error)
	// SendReply threads plain text onto replyTo, falling back to a plain Send
	// if that message is gone — a vanished anchor must not fail a turn.
	SendReply(ctx context.Context, chatID int64, text string, replyTo string, opts ...SendOpt) (msgID string, err error)
	// SendHTMLReply is SendReply with parse_mode=HTML; callers fall back to
	// SendReply with plain text on an HTML rejection.
	SendHTMLReply(ctx context.Context, chatID int64, html string, replyTo string, opts ...SendOpt) (msgID string, err error)
	// Typing shows the "typing…" chat action.
	Typing(ctx context.Context, chatID int64) error

	// Username is the bot's own @name without the "@", for @mention matching.
	Username(ctx context.Context) (string, error)

	// ChatInfo is a chat's title and description, the room brief's raw
	// material. A private chat has no description.
	ChatInfo(ctx context.Context, chatID int64) (title, description string, err error)
	// SendDocument uploads a file, returning its id so an anchor can advance.
	SendDocument(ctx context.Context, chatID int64, filename string, data []byte, caption string, opts ...SendOpt) (msgID string, err error)
	// SendPhoto uploads an image.
	SendPhoto(ctx context.Context, chatID int64, filename string, data []byte, caption string) error
	// SendVoice uploads an ogg/opus voice note.
	SendVoice(ctx context.Context, chatID int64, data []byte, caption string) error
	// SendAudio uploads a music/audio file.
	SendAudio(ctx context.Context, chatID int64, filename string, data []byte, caption string) error
	// SendVideo uploads a video file.
	SendVideo(ctx context.Context, chatID int64, filename string, data []byte, caption string) error
	// DeleteMessage removes a sent message (the progress bubble's cleanup).
	// Best-effort — an already-deleted or too-old message is not actionable.
	DeleteMessage(ctx context.Context, chatID int64, msgID string) error
	// EditPlain replaces a message's text (the progress bubble's own edits).
	EditPlain(ctx context.Context, chatID int64, msgID string, text string) error
}
