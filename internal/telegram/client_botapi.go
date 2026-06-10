//go:build unix

package telegram

import (
	"context"
	"io"
	"net/http"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

const maxMediaBytes = 25 * 1024 * 1024 // 25 MB

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
	switch {
	case u.CallbackQuery != nil:
		cq := u.CallbackQuery
		// CRITICAL: cq.Message is MaybeInaccessibleMessage; inner may be nil.
		inner := cq.Message.Message
		if inner == nil {
			return // inaccessible message; nothing to act on
		}
		c.out <- Msg{
			ChatID:   inner.Chat.ID,
			Callback: &Callback{ID: cq.ID, Data: cq.Data, MsgID: inner.ID},
		}
	case u.Message != nil:
		m := u.Message
		msg := Msg{ChatID: m.Chat.ID, Text: m.Text}
		msg.Media = resolveMedia(ctx, c, m)
		c.out <- msg
	}
}

// resolveMedia downloads any photo/voice/document attached to m.
// Errors fetching one attachment are silently skipped.
func resolveMedia(ctx context.Context, c *botAPIClient, m *models.Message) []Media {
	var out []Media

	// Photo: pick largest size (last in the slice).
	if len(m.Photo) > 0 {
		ps := m.Photo[len(m.Photo)-1]
		if ps.FileSize <= maxMediaBytes {
			if media, ok := c.downloadFile(ctx, ps.FileID, "image/jpeg"); ok {
				out = append(out, media)
			}
		}
	}

	// Voice note (always OGG/Opus; MimeType is sometimes empty).
	if m.Voice != nil {
		if m.Voice.FileSize <= maxMediaBytes {
			mime := m.Voice.MimeType
			if mime == "" {
				mime = "audio/ogg"
			}
			if media, ok := c.downloadFile(ctx, m.Voice.FileID, mime); ok {
				out = append(out, media)
			}
		}
	}

	// Audio file (e.g. an mp3 sent as music).
	if m.Audio != nil {
		if m.Audio.FileSize <= maxMediaBytes {
			if media, ok := c.downloadFile(ctx, m.Audio.FileID, m.Audio.MimeType); ok {
				out = append(out, media)
			}
		}
	}

	// Document.
	if m.Document != nil {
		if m.Document.FileSize <= maxMediaBytes {
			if media, ok := c.downloadFile(ctx, m.Document.FileID, m.Document.MimeType); ok {
				out = append(out, media)
			}
		}
	}

	return out
}

// downloadFile fetches one Telegram file by its file_id and returns a Media.
// Returns (zero, false) on any error.
func (c *botAPIClient) downloadFile(ctx context.Context, fileID, mime string) (Media, bool) {
	f, err := c.b.GetFile(ctx, &bot.GetFileParams{FileID: fileID})
	if err != nil {
		return Media{}, false
	}
	link := c.b.FileDownloadLink(f)
	resp, err := http.Get(link) //nolint:noctx // best-effort; ctx applied above
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
	return Media{Bytes: data, MIME: mime}, true
}

// Updates delivers normalized inbound messages until ctx is cancelled.
func (c *botAPIClient) Updates(ctx context.Context) <-chan Msg { return c.out }

// Send posts a text message with optional inline keyboard buttons (one row).
// ParseMode is deliberately omitted: arbitrary agent output often contains
// unbalanced Markdown characters that cause Telegram to reject the message.
func (c *botAPIClient) Send(ctx context.Context, chatID int64, text string, buttons []Button) (int, error) {
	p := &bot.SendMessageParams{
		ChatID: chatID,
		Text:   text,
	}
	if len(buttons) > 0 {
		row := make([]models.InlineKeyboardButton, len(buttons))
		for i, btn := range buttons {
			if btn.WebApp != "" {
				row[i] = models.InlineKeyboardButton{Text: btn.Text, WebApp: &models.WebAppInfo{URL: btn.WebApp}}
				continue
			}
			row[i] = models.InlineKeyboardButton{Text: btn.Text, CallbackData: btn.Data}
		}
		p.ReplyMarkup = models.InlineKeyboardMarkup{
			InlineKeyboard: [][]models.InlineKeyboardButton{row},
		}
	}
	m, err := c.b.SendMessage(ctx, p)
	if err != nil {
		return 0, err
	}
	return m.ID, nil
}

// EditText replaces a message's text (and removes its inline buttons).
func (c *botAPIClient) EditText(ctx context.Context, chatID int64, msgID int, text string) error {
	_, err := c.b.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID:    chatID,
		MessageID: msgID,
		Text:      text,
	})
	return err
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

// AnswerCallback acknowledges an inline button press (stops the client spinner).
func (c *botAPIClient) AnswerCallback(ctx context.Context, id string) error {
	_, err := c.b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
		CallbackQueryID: id,
	})
	return err
}
