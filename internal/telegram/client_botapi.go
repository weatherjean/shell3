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
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/weatherjean/shell3/internal/applog"
)

const maxMediaBytes = 25 * 1024 * 1024 // 25 MB

// mediaHTTPClient bounds attachment downloads. The per-request context still
// applies; this timeout is the hard ceiling for a single hung connection.
var mediaHTTPClient = &http.Client{Timeout: 60 * time.Second}

type BotAPIClient struct {
	b      *bot.Bot
	out    chan Msg
	log    applog.Logger
	health *pollHealth
}

// NewBotAPIClient builds the real Telegram transport. token comes from config
// (rt.Telegram().Token) — never print it. lg records transport errors
// (getUpdates failures during a network outage) in the app log; nil is
// allowed and logs nowhere.
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
		// The library retries failed polls forever on its own; this handler
		// only makes outages visible. Throttled so a long network drop is a
		// handful of log lines, not thousands.
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
	return c, nil
}

func (c *BotAPIClient) onUpdate(ctx context.Context, b *bot.Bot, u *models.Update) {
	// An update arriving proves the poll loop is healthy again; close out any
	// outage the errors handler recorded.
	if recovered, outage, fails := c.health.ok(); recovered {
		c.log.Warn("telegram transport recovered", "outage", outage.Round(time.Second).String(), "errors", fails)
	}
	if u.Message == nil {
		return
	}
	m := u.Message
	msg := normalizeMessage(m)
	msg.Media = resolveMedia(ctx, c, m)
	c.out <- msg
}

// normalizeMessage projects a Telegram message onto the transport-agnostic Msg,
// minus its attachments (resolveMedia fills those in — it needs the network).
// Telegram puts the words of a MEDIA message in Caption, not Text, so a photo
// sent with "translate this" has an empty Text; the caption is the message.
func normalizeMessage(m *models.Message) Msg {
	msg := Msg{ChatID: m.Chat.ID, ID: strconv.Itoa(m.ID), Text: cmp.Or(m.Text, m.Caption), ReplyTo: replyContext(m)}
	if m.From != nil {
		// From is set by Telegram, not the client. A channel post has no
		// From at all, which leaves SenderID 0 and therefore unauthorized.
		msg.SenderID = m.From.ID
	}
	if r := m.ReplyToMessage; r != nil {
		msg.ReplyToID = strconv.Itoa(r.ID)
	}
	return msg
}

// replyContext returns the text the message is replying to, for model context.
// Prefers the user's highlighted Quote (the specific portion they selected);
// otherwise falls back to the full replied-to message's text (or caption).
func replyContext(m *models.Message) string {
	if m.Quote != nil && m.Quote.Text != "" {
		return m.Quote.Text
	}
	if r := m.ReplyToMessage; r != nil {
		return cmp.Or(r.Text, r.Caption)
	}
	return ""
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
	// Bound the body fetch so a hung file-CDN connection can't park this
	// goroutine (and, since onUpdate downloads media inline, stall the whole
	// update loop) indefinitely.
	resp, err := mediaHTTPClient.Do(req)
	if err != nil {
		return Media{}, false
	}
	defer resp.Body.Close()

	// Cap the read at maxMediaBytes+1 so we can detect an over-limit body.
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

// transientSendErr reports whether a send failure is worth retrying: network
// interruptions and server-side hiccups, never Telegram API rejections (a 400
// retried is a 400 again). 429s retry too — the throttle asks for patience.
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

// sendBackoff is the retry schedule for transient outbound failures: a chat
// message is worth ~4.5s of patience before it is reported lost.
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

// Send posts a plain-text message. ParseMode is omitted; this is the safe
// fallback path when SendHTML is rejected.
func (c *BotAPIClient) Send(ctx context.Context, chatID int64, text string, opts ...SendOpt) (string, error) {
	m, err := withSendRetry(ctx, func() (*models.Message, error) {
		return c.b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:              chatID,
			Text:                text,
			DisableNotification: sendSilent(opts),
		})
	})
	if err != nil {
		return "", err
	}
	return strconv.Itoa(m.ID), nil
}

// SendHTML posts a message with parse_mode=HTML so the agent's formatting
// (bold, italics, code, links) renders. Telegram rejects malformed HTML with a
// 400, so callers fall back to Send on error.
func (c *BotAPIClient) SendHTML(ctx context.Context, chatID int64, html string, opts ...SendOpt) (string, error) {
	m, err := withSendRetry(ctx, func() (*models.Message, error) {
		return c.b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:              chatID,
			Text:                html,
			ParseMode:           models.ParseModeHTML,
			DisableNotification: sendSilent(opts),
		})
	})
	if err != nil {
		return "", err
	}
	return strconv.Itoa(m.ID), nil
}

// replyNotFound reports whether err is Telegram's "message to be replied not
// found" — the reply target (a thread's anchor message) was deleted. The reply
// sends treat it as a signal to fall back to a plain send, never a turn failure.
func replyNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), "message to be replied not found")
}

// SendReply posts plain text as a reply to replyTo. On a deleted reply target
// it retries as a plain Send so a thread whose anchor vanished never fails.
func (c *BotAPIClient) SendReply(ctx context.Context, chatID int64, text string, replyTo string, opts ...SendOpt) (string, error) {
	rid, err := strconv.Atoi(replyTo)
	if err != nil {
		return c.Send(ctx, chatID, text, opts...) // non-numeric anchor (foreign transport): plain send
	}
	m, err := withSendRetry(ctx, func() (*models.Message, error) {
		return c.b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:              chatID,
			Text:                text,
			ReplyParameters:     &models.ReplyParameters{MessageID: rid},
			DisableNotification: sendSilent(opts),
		})
	})
	if replyNotFound(err) {
		return c.Send(ctx, chatID, text, opts...)
	}
	if err != nil {
		return "", err
	}
	return strconv.Itoa(m.ID), nil
}

// SendHTMLReply is SendReply with parse_mode=HTML. On a deleted reply target it
// falls back to a plain (HTML) Send; callers fall back to SendReply on an HTML
// rejection.
func (c *BotAPIClient) SendHTMLReply(ctx context.Context, chatID int64, html string, replyTo string, opts ...SendOpt) (string, error) {
	rid, err := strconv.Atoi(replyTo)
	if err != nil {
		return c.SendHTML(ctx, chatID, html, opts...)
	}
	m, err := withSendRetry(ctx, func() (*models.Message, error) {
		return c.b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:              chatID,
			Text:                html,
			ParseMode:           models.ParseModeHTML,
			ReplyParameters:     &models.ReplyParameters{MessageID: rid},
			DisableNotification: sendSilent(opts),
		})
	})
	if replyNotFound(err) {
		return c.SendHTML(ctx, chatID, html, opts...)
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
