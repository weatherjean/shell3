//go:build unix

package telegram

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"io"
	"net/http"
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
	cb     chan Callback
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
		out: make(chan Msg, 32), cb: make(chan Callback, 64),
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
	// Inline-keyboard button presses (tool-call hook approval) arrive as callback
	// queries, not messages. Route them to the callback channel and stop.
	if u.CallbackQuery != nil {
		select {
		case c.cb <- Callback{ID: u.CallbackQuery.ID, Data: u.CallbackQuery.Data}:
		default:
			// Buffer full (64 deep): drop the press. The waiting Ask then resolves
			// via its tool-call hook ask-timeout → deny, which is the fail-safe
			// direction. A human tap rate filling 64 is not realistic in practice.
		}
		return
	}
	if u.Message == nil {
		return
	}
	m := u.Message
	msg := Msg{ChatID: m.Chat.ID, Text: m.Text, ReplyTo: replyContext(m)}
	msg.Media = resolveMedia(ctx, c, m)
	c.out <- msg
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

// Send posts a plain-text message. ParseMode is omitted; this is the safe
// fallback path when SendHTML is rejected.
func (c *BotAPIClient) Send(ctx context.Context, chatID int64, text string) (int, error) {
	m, err := c.b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatID,
		Text:   text,
	})
	if err != nil {
		return 0, err
	}
	return m.ID, nil
}

// SendHTML posts a message with parse_mode=HTML so the agent's formatting
// (bold, italics, code, links) renders. Telegram rejects malformed HTML with a
// 400, so callers fall back to Send on error.
func (c *BotAPIClient) SendHTML(ctx context.Context, chatID int64, html string) (int, error) {
	m, err := c.b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:    chatID,
		Text:      html,
		ParseMode: models.ParseModeHTML,
	})
	if err != nil {
		return 0, err
	}
	return m.ID, nil
}

// Callbacks returns the inline-keyboard button-press channel. The channel lives
// for the client's lifetime (its source goroutine stops when the ctx passed to
// NewBotAPIClient is cancelled); the ctx argument here is unused. Consumers stop
// reading on their own ctx (see consumeCallbacks).
func (c *BotAPIClient) Callbacks(_ context.Context) <-chan Callback { return c.cb }

// SendConfirm posts text with a single row of two inline buttons — "✅ Allow"
// (yesData) and "🚫 Deny" (noData) — and returns the sent message id. Plain
// text (no parse mode) so an arbitrary command string can't break formatting.
func (c *BotAPIClient) SendConfirm(ctx context.Context, chatID int64, text, yesData, noData string) (int, error) {
	m, err := c.b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatID,
		Text:   text,
		ReplyMarkup: models.InlineKeyboardMarkup{
			InlineKeyboard: [][]models.InlineKeyboardButton{{
				{Text: "✅ Allow", CallbackData: yesData},
				{Text: "🚫 Deny", CallbackData: noData},
			}},
		},
	})
	if err != nil {
		return 0, err
	}
	return m.ID, nil
}

// EditPlain replaces a message's text and removes its inline keyboard (omitting
// ReplyMarkup on editMessageText clears it), so the confirm buttons disappear.
func (c *BotAPIClient) EditPlain(ctx context.Context, chatID int64, msgID int, text string) error {
	_, err := c.b.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID:    chatID,
		MessageID: msgID,
		Text:      text,
	})
	return err
}

// AnswerCallback acknowledges a callback query so the button stops showing a
// loading spinner. Best-effort.
func (c *BotAPIClient) AnswerCallback(ctx context.Context, callbackID string) error {
	_, err := c.b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{CallbackQueryID: callbackID})
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

// SetMenuButton sets the bot's default in-chat menu button to a Web App that
// opens url (the bottom-left "Open App" button). Best-effort; safe to ignore the
// error on startup.
func (c *BotAPIClient) SetMenuButton(ctx context.Context, text, url string) error {
	_, err := c.b.SetChatMenuButton(ctx, &bot.SetChatMenuButtonParams{
		MenuButton: models.MenuButtonWebApp{
			Type:   models.MenuButtonTypeWebApp,
			Text:   text,
			WebApp: models.WebAppInfo{URL: url},
		},
	})
	return err
}

// ReplyKey is one button on the persistent reply keyboard (the bar above the
// text input). A text-only button auto-sends its Text as a message when tapped;
// a button with WebApp opens that HTTPS URL as a Mini App instead.
type ReplyKey struct {
	Text   string
	WebApp string // HTTPS Mini App URL; empty ⇒ a plain command button
}

// ShowReplyKeyboard installs a persistent reply-keyboard bar for the chat by
// sending text with the given button rows attached. IsPersistent keeps the bar
// visible across messages until it is replaced; a tap on a text button sends
// that text (e.g. "/stop"), a tap on a WebApp button opens the Mini App.
// Best-effort, mirroring SetCommands/SetMenuButton.
func (c *BotAPIClient) ShowReplyKeyboard(ctx context.Context, chatID int64, text string, rows [][]ReplyKey) error {
	kb := make([][]models.KeyboardButton, len(rows))
	for i, row := range rows {
		kb[i] = make([]models.KeyboardButton, len(row))
		for j, k := range row {
			if k.WebApp != "" {
				kb[i][j] = models.KeyboardButton{Text: k.Text, WebApp: &models.WebAppInfo{URL: k.WebApp}}
				continue
			}
			kb[i][j] = models.KeyboardButton{Text: k.Text}
		}
	}
	_, err := c.b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatID,
		Text:   text,
		ReplyMarkup: models.ReplyKeyboardMarkup{
			Keyboard:       kb,
			IsPersistent:   true,
			ResizeKeyboard: true,
		},
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
func (c *BotAPIClient) SendDocument(ctx context.Context, chatID int64, filename string, data []byte, caption string) error {
	_, err := c.b.SendDocument(ctx, &bot.SendDocumentParams{
		ChatID:   chatID,
		Document: &models.InputFileUpload{Filename: filename, Data: bytes.NewReader(data)},
		Caption:  caption,
	})
	return err
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

// SendMenu posts text with one row of inline buttons, one per option; a
// press's Data is delivered via the Callbacks channel. Returns the sent
// message id.
func (c *BotAPIClient) SendMenu(ctx context.Context, chatID int64, text string, options []MenuOption) (int, error) {
	row := make([]models.InlineKeyboardButton, len(options))
	for i, o := range options {
		row[i] = models.InlineKeyboardButton{Text: o.Label, CallbackData: o.Data}
	}
	m, err := c.b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatID,
		Text:   text,
		ReplyMarkup: models.InlineKeyboardMarkup{
			InlineKeyboard: [][]models.InlineKeyboardButton{row},
		},
	})
	if err != nil {
		return 0, err
	}
	return m.ID, nil
}
