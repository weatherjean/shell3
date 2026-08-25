//go:build unix

package telegram

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/weatherjean/shell3/internal/applog"
)

const maxMediaBytes = 25 * 1024 * 1024 // 25 MB

// mediaHTTPClient is the hard ceiling for one hung connection; the
// per-request context still applies.
var mediaHTTPClient = &http.Client{Timeout: 60 * time.Second}

type BotAPIClient struct {
	b      *bot.Bot
	out    chan Msg
	log    applog.Logger
	health *pollHealth

	// identity caches getMe: the bot's own @username, needed on every group
	// message. One lookup per process.
	identityMu   sync.Mutex
	username     string
	selfID       int64
	usernameSeen bool
}

// selfIdentity resolves this bot's user id once. Zero before it succeeds,
// which costs only a reply-trigger miss.
func (c *BotAPIClient) selfIdentity() int64 {
	c.identityMu.Lock()
	defer c.identityMu.Unlock()
	return c.selfID
}

// Username reports the bot's own @name without the "@", cached after the
// first successful lookup.
func (c *BotAPIClient) Username(ctx context.Context) (string, error) {
	c.identityMu.Lock()
	if c.usernameSeen {
		defer c.identityMu.Unlock()
		return c.username, nil
	}
	c.identityMu.Unlock()

	me, err := c.b.GetMe(ctx)
	if err != nil {
		return "", err
	}
	c.identityMu.Lock()
	c.username, c.selfID, c.usernameSeen = me.Username, me.ID, true
	c.identityMu.Unlock()
	return me.Username, nil
}

// ChatInfo is a chat's title and description, the room brief's raw material.
// A private chat has neither in the sense meant here — its title is the other
// party's name — so the caller decides what to use.
func (c *BotAPIClient) ChatInfo(ctx context.Context, chatID int64) (string, string, error) {
	ch, err := c.b.GetChat(ctx, &bot.GetChatParams{ChatID: chatID})
	if err != nil {
		return "", "", err
	}
	return ch.Title, ch.Description, nil
}

// NewBotAPIClient builds the real transport. Never print token. lg records
// transport errors in the app log; nil logs nowhere.
func NewBotAPIClient(ctx context.Context, token string, lg applog.Logger) (*BotAPIClient, error) {
	if lg == nil {
		lg = applog.Noop{}
	}
	c := &BotAPIClient{
		out: make(chan Msg, 32),
		log: lg, health: newPollHealth(),
	}
	b, err := bot.New(token,
		bot.WithDefaultHandler(c.onUpdate),
		// The library retries failed polls itself; this only makes outages
		// visible, throttled so a long drop is a handful of lines.
		bot.WithErrorsHandler(func(err error) {
			if errors.Is(err, context.Canceled) {
				return // clean shutdown aborting the pending poll, not an outage
			}
			if logNow, fails := c.health.fail(); logNow {
				c.log.Warn("telegram transport error (bot keeps retrying)", "error", err, "errors_this_outage", fails)
			}
		}),
	)
	if err != nil {
		return nil, err
	}
	c.b = b
	go b.Start(ctx) // long-polls until ctx cancelled
	go c.watchHealth(ctx)
	return c, nil
}

// watchHealth closes out an outage a quiet chat never proves is over: the
// library calls onUpdate only when someone sends something, so a recovered
// transport looks identical to a dead one until this ticker notices the
// errors stopped. Observability only — nothing here reconnects.
func (c *BotAPIClient) watchHealth(ctx context.Context) {
	t := time.NewTicker(pollQuietRecovery / 5)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if recovered, outage, fails := c.health.sweep(); recovered {
				c.log.Warn("telegram transport recovered", "outage", outage.Round(time.Second).String(), "errors", fails)
			}
		}
	}
}

func (c *BotAPIClient) onUpdate(ctx context.Context, b *bot.Bot, u *models.Update) {
	// An update proves the poll loop is healthy again.
	if recovered, outage, fails := c.health.ok(); recovered {
		c.log.Warn("telegram transport recovered", "outage", outage.Round(time.Second).String(), "errors", fails)
	}
	if u.Message == nil {
		return
	}
	m := u.Message
	msg := normalizeMessage(m)
	// From Telegram's own author field, not what this process remembers
	// sending — that set is empty after a restart.
	if r := m.ReplyToMessage; r != nil && r.From != nil {
		if self := c.selfIdentity(); self != 0 && r.From.ID == self {
			msg.ReplyToBot = true
		}
	}
	// NOT downloaded here: the bot fetches once the message clears
	// authorization. With privacy mode off that is the difference between
	// fetching what was sent to the agent and every photo it can see.
	msg.HasMedia = hasAttachment(m)
	if msg.HasMedia {
		msg.FetchMedia = func(fctx context.Context) []Media { return resolveMedia(fctx, c, m) }
	}
	c.out <- msg
}

// normalizeMessage projects a Telegram message onto Msg, minus attachments —
// resolveMedia needs the network. A MEDIA message's words are in Caption, not
// Text, so a photo sent with "translate this" has an empty Text.
func normalizeMessage(m *models.Message) Msg {
	msg := Msg{ChatID: m.Chat.ID, ChatType: string(m.Chat.Type), ID: strconv.Itoa(m.ID),
		Text: cmp.Or(m.Text, m.Caption), ReplyTo: replyContext(m)}
	if m.From != nil {
		// Set by Telegram, not the client. A channel post has no From, which
		// leaves SenderID 0 and therefore unauthorized.
		msg.SenderID = m.From.ID
	}
	if r := m.ReplyToMessage; r != nil {
		msg.ReplyToID = strconv.Itoa(r.ID)
	}
	msg.MigratedTo = m.MigrateToChatID
	return msg
}

// replyContext is the text being replied to: the user's highlighted Quote if
// they selected one, else the whole replied-to message.
func replyContext(m *models.Message) string {
	if m.Quote != nil && m.Quote.Text != "" {
		return m.Quote.Text
	}
	if r := m.ReplyToMessage; r != nil {
		return cmp.Or(r.Text, r.Caption)
	}
	return ""
}

// hasAttachment reports an attachment resolveMedia can fetch — what the
// routing gates test before the bytes exist.
func hasAttachment(m *models.Message) bool {
	return len(m.Photo) > 0 || m.Voice != nil || m.Audio != nil ||
		m.Video != nil || m.Animation != nil || m.Document != nil
}

// resolveMedia downloads every attachment on m (photo/voice/audio/video/
// animation/document) to bytes. Errors fetching one attachment are skipped.
func resolveMedia(ctx context.Context, c *BotAPIClient, m *models.Message) []Media {
	var out []Media
	add := func(fileID, mime, filename string, size int64) {
		if fileID == "" || size > maxMediaBytes {
			return
		}
		if media, ok := c.downloadFile(ctx, fileID, mime, filename); ok {
			out = append(out, media)
		}
	}
	if len(m.Photo) > 0 {
		ps := m.Photo[len(m.Photo)-1] // largest size
		add(ps.FileID, "image/jpeg", "photo.jpg", int64(ps.FileSize))
	}
	if m.Voice != nil {
		add(m.Voice.FileID, cmp.Or(m.Voice.MimeType, "audio/ogg"), "voice.ogg", m.Voice.FileSize)
	}
	if m.Audio != nil {
		add(m.Audio.FileID, cmp.Or(m.Audio.MimeType, "audio/mpeg"), cmp.Or(m.Audio.FileName, "audio.mp3"), m.Audio.FileSize)
	}
	if m.Video != nil {
		add(m.Video.FileID, cmp.Or(m.Video.MimeType, "video/mp4"), cmp.Or(m.Video.FileName, "video.mp4"), m.Video.FileSize)
	}
	if m.Animation != nil {
		add(m.Animation.FileID, cmp.Or(m.Animation.MimeType, "video/mp4"), cmp.Or(m.Animation.FileName, "animation.mp4"), m.Animation.FileSize)
	}
	if m.Document != nil {
		add(m.Document.FileID, cmp.Or(m.Document.MimeType, "application/octet-stream"), cmp.Or(m.Document.FileName, "document.bin"), m.Document.FileSize)
	}
	return out
}

// downloadFile fetches one Telegram file by its file_id and returns a Media.
// Returns (zero, false) on any error.
func (c *BotAPIClient) downloadFile(ctx context.Context, fileID, mime, filename string) (Media, bool) {
	f, err := c.b.GetFile(ctx, &bot.GetFileParams{FileID: fileID})
	if err != nil {
		return Media{}, false
	}
	link := c.b.FileDownloadLink(f)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return Media{}, false
	}
	// Bound the body fetch: onUpdate downloads media inline, so a hung
	// file-CDN connection would stall the whole update loop.
	resp, err := mediaHTTPClient.Do(req)
	if err != nil {
		return Media{}, false
	}
	defer resp.Body.Close()

	// +1 so an over-limit body is detectable.
	lr := io.LimitReader(resp.Body, maxMediaBytes+1)
	data, err := io.ReadAll(lr)
	if err != nil {
		return Media{}, false
	}
	if len(data) > maxMediaBytes {
		return Media{}, false // body exceeded the cap
	}
	return Media{Bytes: data, MIME: mime, Filename: filename}, true
}

// Updates delivers normalized inbound messages until ctx is cancelled.
func (c *BotAPIClient) Updates(ctx context.Context) <-chan Msg { return c.out }

// transientSendErr: network interruptions and server-side hiccups are worth
// retrying, API rejections are not — a 400 retried is a 400 again. A 429 does
// retry; the throttle is asking for patience.
func transientSendErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false // the caller gave up; retrying past it delivers zombies
	}
	msg := strings.ToLower(err.Error())
	for _, marker := range []string{
		"timeout", "connection", "reset", "eof", "temporarily",
		"429", "too many requests", "500", "502", "503", "504", "gateway",
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// sendBackoff: a chat message is worth ~4.5s before it is reported lost.
var sendBackoff = []time.Duration{300 * time.Millisecond, 1200 * time.Millisecond, 3 * time.Second}

// withSendRetry runs send, retrying transient failures on the backoff
// schedule. Returns the first non-transient error or the last failure.
func withSendRetry[T any](ctx context.Context, send func() (T, error)) (T, error) {
	out, err := send()
	for _, wait := range sendBackoff {
		if err == nil || !transientSendErr(err) {
			return out, err
		}
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return out, err
		}
		out, err = send()
	}
	return out, err
}

// Send posts plain text with no ParseMode — the fallback when SendHTML is
// rejected.
func (c *BotAPIClient) Send(ctx context.Context, chatID int64, text string, opts ...SendOpt) (string, error) {
	return c.sendText(ctx, chatID, text, false, "", opts)
}

// SendHTML posts with parse_mode=HTML so the agent's formatting renders.
// Malformed HTML is a 400, so callers fall back to Send.
func (c *BotAPIClient) SendHTML(ctx context.Context, chatID int64, html string, opts ...SendOpt) (string, error) {
	return c.sendText(ctx, chatID, html, true, "", opts)
}

// SendReply threads plain text onto replyTo, retrying as a plain Send if that
// message is gone.
func (c *BotAPIClient) SendReply(ctx context.Context, chatID int64, text string, replyTo string, opts ...SendOpt) (string, error) {
	return c.sendText(ctx, chatID, text, false, replyTo, opts)
}

// SendHTMLReply is SendReply with parse_mode=HTML; callers fall back to
// SendReply on an HTML rejection.
func (c *BotAPIClient) SendHTMLReply(ctx context.Context, chatID int64, html string, replyTo string, opts ...SendOpt) (string, error) {
	return c.sendText(ctx, chatID, html, true, replyTo, opts)
}

// replyNotFound reports whether err is Telegram's "message to be replied not
// found" — the reply target (a thread's anchor message) was deleted. sendText
// treats it as a signal to re-send unthreaded, never a turn failure.
func replyNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), "message to be replied not found")
}

// sendText is the one outbound text path: plain or HTML, threaded or not.
// A non-numeric replyTo is a foreign transport's id (the console client
// numbers its own), and a vanished anchor answers replyNotFound — both send
// unthreaded rather than failing the turn.
func (c *BotAPIClient) sendText(ctx context.Context, chatID int64, text string, asHTML bool, replyTo string, opts []SendOpt) (string, error) {
	p := &bot.SendMessageParams{
		ChatID:              chatID,
		Text:                text,
		DisableNotification: sendSilent(opts),
	}
	if asHTML {
		p.ParseMode = models.ParseModeHTML
	}
	if replyTo != "" {
		if rid, err := strconv.Atoi(replyTo); err == nil {
			p.ReplyParameters = &models.ReplyParameters{MessageID: rid}
		}
	}
	m, err := withSendRetry(ctx, func() (*models.Message, error) { return c.b.SendMessage(ctx, p) })
	if replyNotFound(err) {
		p.ReplyParameters = nil
		m, err = withSendRetry(ctx, func() (*models.Message, error) { return c.b.SendMessage(ctx, p) })
	}
	if err != nil {
		return "", err
	}
	return strconv.Itoa(m.ID), nil
}

// DeleteMessage removes a sent message by id (non-numeric ids are foreign and
// ignored).
func (c *BotAPIClient) DeleteMessage(ctx context.Context, chatID int64, msgID string) error {
	id, err := strconv.Atoi(msgID)
	if err != nil {
		return nil
	}
	_, err = c.b.DeleteMessage(ctx, &bot.DeleteMessageParams{ChatID: chatID, MessageID: id})
	return err
}

// EditPlain replaces a message's text (omitting ReplyMarkup on
// editMessageText leaves any existing keyboard alone; nothing sends one
// anymore).
func (c *BotAPIClient) EditPlain(ctx context.Context, chatID int64, msgID string, text string) error {
	mid, err := strconv.Atoi(msgID)
	if err != nil {
		return err
	}
	_, err = c.b.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID:    chatID,
		MessageID: mid,
		Text:      text,
	})
	return err
}

// Typing shows the "typing…" chat action.
func (c *BotAPIClient) Typing(ctx context.Context, chatID int64) error {
	_, err := c.b.SendChatAction(ctx, &bot.SendChatActionParams{
		ChatID: chatID,
		Action: models.ChatActionTyping,
	})
	return err
}

// SetCommands registers the bot's command list, shown in Telegram's "/"
// autocomplete menu. Best-effort.
func (c *BotAPIClient) SetCommands(ctx context.Context, cmds []Command) error {
	bc := make([]models.BotCommand, len(cmds))
	for i, cmd := range cmds {
		bc[i] = models.BotCommand{Command: cmd.Command, Description: cmd.Description}
	}
	_, err := c.b.SetMyCommands(ctx, &bot.SetMyCommandsParams{Commands: bc})
	return err
}

// SendDocument uploads a file to the chat as a document.
func (c *BotAPIClient) SendDocument(ctx context.Context, chatID int64, filename string, data []byte, caption string, opts ...SendOpt) (string, error) {
	m, err := withSendRetry(ctx, func() (*models.Message, error) {
		return c.b.SendDocument(ctx, &bot.SendDocumentParams{
			ChatID:              chatID,
			Document:            &models.InputFileUpload{Filename: filename, Data: bytes.NewReader(data)},
			Caption:             caption,
			DisableNotification: sendSilent(opts),
		})
	})
	if err != nil {
		return "", err
	}
	return strconv.Itoa(m.ID), nil
}

// SendPhoto uploads an image to the chat with an optional caption.
func (c *BotAPIClient) SendPhoto(ctx context.Context, chatID int64, filename string, data []byte, caption string) error {
	_, err := c.b.SendPhoto(ctx, &bot.SendPhotoParams{
		ChatID:  chatID,
		Photo:   &models.InputFileUpload{Filename: filename, Data: bytes.NewReader(data)},
		Caption: caption,
	})
	return err
}

// SendVoice uploads a voice note to the chat with an optional caption. The
// upload is given a fixed filename since SendVoiceParams takes no filename
// field of its own.
func (c *BotAPIClient) SendVoice(ctx context.Context, chatID int64, data []byte, caption string) error {
	_, err := c.b.SendVoice(ctx, &bot.SendVoiceParams{
		ChatID:  chatID,
		Voice:   &models.InputFileUpload{Filename: "voice.ogg", Data: bytes.NewReader(data)},
		Caption: caption,
	})
	return err
}

// SendAudio uploads a music/audio file to the chat with an optional caption.
func (c *BotAPIClient) SendAudio(ctx context.Context, chatID int64, filename string, data []byte, caption string) error {
	_, err := c.b.SendAudio(ctx, &bot.SendAudioParams{
		ChatID:  chatID,
		Audio:   &models.InputFileUpload{Filename: filename, Data: bytes.NewReader(data)},
		Caption: caption,
	})
	return err
}

// SendVideo uploads a video file to the chat with an optional caption.
func (c *BotAPIClient) SendVideo(ctx context.Context, chatID int64, filename string, data []byte, caption string) error {
	_, err := c.b.SendVideo(ctx, &bot.SendVideoParams{
		ChatID:  chatID,
		Video:   &models.InputFileUpload{Filename: filename, Data: bytes.NewReader(data)},
		Caption: caption,
	})
	return err
}
