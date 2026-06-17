//go:build unix

package telegram

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

const maxMediaBytes = 25 * 1024 * 1024 // 25 MB

// mediaHTTPClient bounds attachment downloads. The per-request context still
// applies; this timeout is the hard ceiling for a single hung connection.
var mediaHTTPClient = &http.Client{Timeout: 60 * time.Second}

type botAPIClient struct {
	b   *bot.Bot
	out chan Msg
}

// NewBotAPIClient builds the real Telegram transport. token comes from config
// (rt.Telegram().Token) — never print it.
func NewBotAPIClient(ctx context.Context, token string) (*botAPIClient, error) {
	c := &botAPIClient{out: make(chan Msg, 32)}
	b, err := bot.New(token, bot.WithDefaultHandler(c.onUpdate))
	if err != nil {
		return nil, err
	}
	c.b = b
	go b.Start(ctx) // long-polls until ctx cancelled
	return c, nil
}

func (c *botAPIClient) onUpdate(ctx context.Context, b *bot.Bot, u *models.Update) {
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
		return orDefault(r.Text, r.Caption)
	}
	return ""
}

// resolveMedia downloads every attachment on m (photo/voice/audio/video/
// animation/document) to bytes. Errors fetching one attachment are skipped.
func resolveMedia(ctx context.Context, c *botAPIClient, m *models.Message) []Media {
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
		add(m.Voice.FileID, orDefault(m.Voice.MimeType, "audio/ogg"), "voice.ogg", m.Voice.FileSize)
	}
	if m.Audio != nil {
		add(m.Audio.FileID, orDefault(m.Audio.MimeType, "audio/mpeg"), orDefault(m.Audio.FileName, "audio.mp3"), m.Audio.FileSize)
	}
	if m.Video != nil {
		add(m.Video.FileID, orDefault(m.Video.MimeType, "video/mp4"), orDefault(m.Video.FileName, "video.mp4"), m.Video.FileSize)
	}
	if m.Animation != nil {
		add(m.Animation.FileID, orDefault(m.Animation.MimeType, "video/mp4"), orDefault(m.Animation.FileName, "animation.mp4"), m.Animation.FileSize)
	}
	if m.Document != nil {
		add(m.Document.FileID, orDefault(m.Document.MimeType, "application/octet-stream"), orDefault(m.Document.FileName, "document.bin"), m.Document.FileSize)
	}
	return out
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// downloadFile fetches one Telegram file by its file_id and returns a Media.
// Returns (zero, false) on any error.
func (c *botAPIClient) downloadFile(ctx context.Context, fileID, mime, filename string) (Media, bool) {
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
func (c *botAPIClient) Updates(ctx context.Context) <-chan Msg { return c.out }

// Send posts a plain-text message. ParseMode is omitted; this is the safe
// fallback path when SendHTML is rejected.
func (c *botAPIClient) Send(ctx context.Context, chatID int64, text string) (int, error) {
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
func (c *botAPIClient) SendHTML(ctx context.Context, chatID int64, html string) (int, error) {
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

// Typing shows the "typing…" chat action.
func (c *botAPIClient) Typing(ctx context.Context, chatID int64) error {
	_, err := c.b.SendChatAction(ctx, &bot.SendChatActionParams{
		ChatID: chatID,
		Action: models.ChatActionTyping,
	})
	return err
}

// SetMenuButton sets the bot's default in-chat menu button to a Web App that
// opens url (the bottom-left "Open App" button). Best-effort; safe to ignore the
// error on startup.
func (c *botAPIClient) SetMenuButton(ctx context.Context, text, url string) error {
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
func (c *botAPIClient) ShowReplyKeyboard(ctx context.Context, chatID int64, text string, rows [][]ReplyKey) error {
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
func (c *botAPIClient) SetCommands(ctx context.Context, cmds []Command) error {
	bc := make([]models.BotCommand, len(cmds))
	for i, cmd := range cmds {
		bc[i] = models.BotCommand{Command: cmd.Command, Description: cmd.Description}
	}
	_, err := c.b.SetMyCommands(ctx, &bot.SetMyCommandsParams{Commands: bc})
	return err
}

// SendDocument uploads a file to the chat as a document.
func (c *botAPIClient) SendDocument(ctx context.Context, chatID int64, filename string, data []byte, caption string) error {
	_, err := c.b.SendDocument(ctx, &bot.SendDocumentParams{
		ChatID:   chatID,
		Document: &models.InputFileUpload{Filename: filename, Data: bytes.NewReader(data)},
		Caption:  caption,
	})
	return err
}
