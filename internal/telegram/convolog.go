//go:build unix

package telegram

// The optional conversation log records all transport traffic as JSONL,
// including host replies, inbox alerts, and messages rejected before a
// room exists. It may contain every message visible to the bot.

import (
	"context"
	"encoding/json"
	"io"
	"sync"
	"time"

	"github.com/weatherjean/shell3/internal/strutil"
)

// convoTextCap bounds one event's text. Generous — the point is to read the
// conversation back — but a reply that overflows into a reply.md document
// must not put a megabyte on one line.
const convoTextCap = 16 * 1024

// convoEvent is one line of the log. Fields are omitempty so a line stays
// readable by eye; ts/dir/kind are always present.
type convoEvent struct {
	TS   string `json:"ts"`
	Dir  string `json:"dir"`  // "in" (received) or "out" (sent)
	Kind string `json:"kind"` // msg, send, reply, edit, delete, document, …

	Chat     int64  `json:"chat,omitempty"`
	Sender   int64  `json:"sender,omitempty"`
	ChatType string `json:"chat_type,omitempty"`
	ID       string `json:"id,omitempty"`
	ReplyTo  string `json:"reply_to,omitempty"`

	Text string `json:"text,omitempty"`
	// Silent marks a send that arrived without a ping — the difference between
	// "they were not told" and
	// "they were told quietly", which the text alone cannot show.
	Silent bool `json:"silent,omitempty"`

	// File/Bytes/MIME describe an attachment. The bytes THEMSELVES are never
	// written: a log that embeds every photo stops being greppable and starts
	// being a second copy of the media dir.
	File  string `json:"file,omitempty"`
	Bytes int    `json:"bytes,omitempty"`
	MIME  string `json:"mime,omitempty"`

	// Err is the transport's error, so a post the user never saw is
	// distinguishable from one they did. This is the whole reason sends are
	// logged after the call rather than before it.
	Err string `json:"err,omitempty"`
}

// convoLog serializes writes from turn goroutines and the progress bubble.
type convoLog struct {
	mu  sync.Mutex
	w   io.Writer
	now func() time.Time
}

func (l *convoLog) write(ev convoEvent) {
	if l == nil || l.w == nil {
		return
	}
	ev.TS = l.now().UTC().Format(time.RFC3339Nano)
	ev.Text = strutil.Truncate(ev.Text, convoTextCap)
	line, err := json.Marshal(ev)
	if err != nil {
		return // a log must never be the thing that fails a turn
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = l.w.Write(append(line, '\n'))
}

// errText renders an error for the log, "" when there was none.
func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// convoLogClient wraps a transport and records both directions.
//
// It EMBEDS tgClient rather than listing every method as a field: a method
// added to the interface later then still compiles and still works, silently
// unlogged, instead of breaking the build — and an unlogged send is a hole in
// exactly the record this exists to keep. That trade is deliberate. The
// counterpart is that every method below is an explicit override, so adding
// one to the interface should mean adding one here; the test pins the current
// set so the omission is at least visible.
type convoLogClient struct {
	tgClient
	log *convoLog
	// once guards the wrapped update stream: Updates must return the SAME
	// channel every call. Bot.Run calls it once per loop iteration — harmless
	// for a client that hands back a field, fatal for one that spawns a
	// forwarder, because each extra goroutine competes for the upstream and
	// the ones whose channel nobody reads any more swallow whatever they win.
	// Shipped that way for one hour on 2026-08-25: messages were logged (the
	// forwarder logs before it forwards) and then never reached a turn, so the
	// user watched roughly every other message vanish with no error anywhere.
	once sync.Once
	out  chan Msg
}

// newConvoLogClient wraps c so every message in and out is recorded to w.
func newConvoLogClient(c tgClient, w io.Writer) tgClient {
	return &convoLogClient{tgClient: c, log: &convoLog{w: w, now: time.Now}}
}

// Updates records every inbound message BEFORE the bot sees it — before the
// sender allowlist and before the group trigger gate, so a message that was
// deliberately dropped still appears in the log with the context needed to
// see why it was dropped (sender, chat type, whether it was a reply).
func (c *convoLogClient) Updates(ctx context.Context) <-chan Msg {
	c.once.Do(func() {
		in := c.tgClient.Updates(ctx)
		c.out = make(chan Msg)
		go func() {
			defer close(c.out)
			for m := range in {
				c.log.write(inboundEvent(m))
				select {
				case c.out <- m:
				case <-ctx.Done():
					return
				}
			}
		}()
	})
	return c.out
}

// inboundEvent renders a received message. Attachments are described, never
// embedded, and an unfetched one (FetchMedia deferred past authorization)
// still shows as media so a dropped photo is not mistaken for a bare message.
func inboundEvent(m Msg) convoEvent {
	ev := convoEvent{
		Dir: "in", Kind: "msg",
		Chat: m.ChatID, Sender: m.SenderID, ChatType: m.ChatType,
		ID: m.ID, ReplyTo: m.ReplyToID, Text: m.Text,
	}
	if m.MigratedTo != 0 {
		ev.Kind = "migrate"
		ev.Text = "migrated to chat " + itoa(m.MigratedTo)
	}
	switch {
	case len(m.Media) > 0:
		med := m.Media[0]
		ev.File, ev.Bytes, ev.MIME = med.Filename, len(med.Bytes), med.MIME
	case m.HasMedia:
		ev.File = "(not fetched)"
	}
	return ev
}

// itoa avoids a strconv import for the one numeric field rendered as text.
func itoa(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func (c *convoLogClient) Send(ctx context.Context, chatID int64, text string, opts ...SendOpt) (string, error) {
	id, err := c.tgClient.Send(ctx, chatID, text, opts...)
	c.log.write(convoEvent{Dir: "out", Kind: "send", Chat: chatID, ID: id,
		Text: text, Silent: sendSilent(opts), Err: errText(err)})
	return id, err
}

func (c *convoLogClient) SendHTML(ctx context.Context, chatID int64, html string, opts ...SendOpt) (string, error) {
	id, err := c.tgClient.SendHTML(ctx, chatID, html, opts...)
	c.log.write(convoEvent{Dir: "out", Kind: "send_html", Chat: chatID, ID: id,
		Text: html, Silent: sendSilent(opts), Err: errText(err)})
	return id, err
}

func (c *convoLogClient) SendReply(ctx context.Context, chatID int64, text, replyTo string, opts ...SendOpt) (string, error) {
	id, err := c.tgClient.SendReply(ctx, chatID, text, replyTo, opts...)
	c.log.write(convoEvent{Dir: "out", Kind: "reply", Chat: chatID, ID: id, ReplyTo: replyTo,
		Text: text, Silent: sendSilent(opts), Err: errText(err)})
	return id, err
}

func (c *convoLogClient) SendHTMLReply(ctx context.Context, chatID int64, html, replyTo string, opts ...SendOpt) (string, error) {
	id, err := c.tgClient.SendHTMLReply(ctx, chatID, html, replyTo, opts...)
	c.log.write(convoEvent{Dir: "out", Kind: "reply_html", Chat: chatID, ID: id, ReplyTo: replyTo,
		Text: html, Silent: sendSilent(opts), Err: errText(err)})
	return id, err
}

func (c *convoLogClient) SendDocument(ctx context.Context, chatID int64, filename string, data []byte, caption string, opts ...SendOpt) (string, error) {
	id, err := c.tgClient.SendDocument(ctx, chatID, filename, data, caption, opts...)
	c.log.write(convoEvent{Dir: "out", Kind: "document", Chat: chatID, ID: id,
		File: filename, Bytes: len(data), Text: caption, Silent: sendSilent(opts), Err: errText(err)})
	return id, err
}

func (c *convoLogClient) SendPhoto(ctx context.Context, chatID int64, filename string, data []byte, caption string) error {
	err := c.tgClient.SendPhoto(ctx, chatID, filename, data, caption)
	c.log.write(convoEvent{Dir: "out", Kind: "photo", Chat: chatID,
		File: filename, Bytes: len(data), Text: caption, Err: errText(err)})
	return err
}

func (c *convoLogClient) SendVoice(ctx context.Context, chatID int64, data []byte, caption string) error {
	err := c.tgClient.SendVoice(ctx, chatID, data, caption)
	c.log.write(convoEvent{Dir: "out", Kind: "voice", Chat: chatID,
		Bytes: len(data), Text: caption, Err: errText(err)})
	return err
}

func (c *convoLogClient) SendAudio(ctx context.Context, chatID int64, filename string, data []byte, caption string) error {
	err := c.tgClient.SendAudio(ctx, chatID, filename, data, caption)
	c.log.write(convoEvent{Dir: "out", Kind: "audio", Chat: chatID,
		File: filename, Bytes: len(data), Text: caption, Err: errText(err)})
	return err
}

func (c *convoLogClient) SendVideo(ctx context.Context, chatID int64, filename string, data []byte, caption string) error {
	err := c.tgClient.SendVideo(ctx, chatID, filename, data, caption)
	c.log.write(convoEvent{Dir: "out", Kind: "video", Chat: chatID,
		File: filename, Bytes: len(data), Text: caption, Err: errText(err)})
	return err
}

// EditPlain is the progress bubble's own edits, which fire every 1.5s for the
// length of a turn and so dominate the log by line count. They stay in anyway:
// a bubble that stopped updating, or one left behind after an error, is a
// symptom whose only evidence is here.
func (c *convoLogClient) EditPlain(ctx context.Context, chatID int64, msgID, text string) error {
	err := c.tgClient.EditPlain(ctx, chatID, msgID, text)
	c.log.write(convoEvent{Dir: "out", Kind: "edit", Chat: chatID, ID: msgID,
		Text: text, Err: errText(err)})
	return err
}

// DeleteMessage is how the progress bubble disappears after a clean turn.
// Logging it is what makes an earlier "send" line that has no counterpart in
// the chat explicable rather than a mystery.
func (c *convoLogClient) DeleteMessage(ctx context.Context, chatID int64, msgID string) error {
	err := c.tgClient.DeleteMessage(ctx, chatID, msgID)
	c.log.write(convoEvent{Dir: "out", Kind: "delete", Chat: chatID, ID: msgID, Err: errText(err)})
	return err
}

// Typing is deliberately NOT logged: it carries no content, fires on a timer
// for the whole of every turn, and would outnumber the messages severalfold.
