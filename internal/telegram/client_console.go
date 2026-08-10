//go:build unix

package telegram

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
)

// ConsoleChatID is the fixed chat id stamped on every inbound console message
// (and expected by the Bot). Console mode has no real Telegram chat, so a single
// constant stands in for one.
const ConsoleChatID int64 = 1

// ConsoleClient is a tgClient that speaks stdin/stdout instead of Telegram, for
// driving the REAL bot loop (commands, cron ⏰ posts, threading, courtesy
// rejection, /jobs, hook asks) in a headless script without a phone. It is a
// dev/debug transport, not a chat front-end.
//
// Inbound (stdin, one line per message):
//   - a plain line              → a fresh message
//   - "@<id> text"              → a reply to message <id> (thread continuation)
//   - "/..."                    → a bot command, as usual
//   - a blank line              → skipped
//
// EOF closes the inbound channel, which stops Bot.Run — a clean shutdown when
// the process is driven by a pipe or file.
//
// Outbound (stdout, one line per message):
//   - "[#<id>] text"            → a plain/HTML send (HTML is printed raw, unrendered)
//   - "[#<id> ↩#<replyto>] text" → a threaded reply
//   - "[#<id> menu] …" for menu sends
//   - "[media …]" markers for document/photo/voice/audio/video uploads (no-ops)
//
// Message ids come from one monotonic counter shared by inbound and outbound
// (mirroring Telegram's single message_id space), so a script can reply to any
// printed "[#<id>]" to continue that thread. Inline menus (SendMenu) print
// their option labels; presses are not readable from the single stdin loop,
// so a menu is display-only in console mode.
type ConsoleClient struct {
	in     chan Msg
	cb     chan Callback
	chatID int64

	mu  sync.Mutex // guards seq and serializes writes to out
	out io.Writer
	seq int

	r         io.Reader
	startOnce sync.Once
}

// flusher is the optional line-flush surface of a buffered writer; a plain
// *os.File (the production wiring) needs none, but a bufio.Writer test sink does.
type flusher interface{ Flush() error }

// NewConsoleClient builds a console transport reading messages from r and
// printing outbound messages to out. chatID is stamped on every inbound message
// (pass ConsoleChatID, which the Bot must be constructed with).
func NewConsoleClient(r io.Reader, out io.Writer, chatID int64) *ConsoleClient {
	return &ConsoleClient{
		in:     make(chan Msg, 16),
		cb:     make(chan Callback, 16),
		chatID: chatID,
		out:    out,
		r:      r,
	}
}

// Updates starts the stdin reader (once) and returns the inbound channel. The
// reader closes the channel on EOF or ctx cancellation, so Bot.Run returns.
func (c *ConsoleClient) Updates(ctx context.Context) <-chan Msg {
	c.startOnce.Do(func() { go c.readLoop(ctx) })
	return c.in
}

// readLoop parses stdin lines into inbound messages until EOF or ctx done.
func (c *ConsoleClient) readLoop(ctx context.Context) {
	defer close(c.in)
	sc := bufio.NewScanner(c.r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		if ctx.Err() != nil {
			return
		}
		line := strings.TrimRight(sc.Text(), "\r")
		if strings.TrimSpace(line) == "" {
			continue // blank line: nothing actionable
		}
		// "&<data>" answers an inline menu (a button tap): the console has no
		// buttons, so the callback_data is typed — e.g. "&nt|new" for the
		// thread-choice ask.
		if data := strings.TrimPrefix(line, "&"); data != line && strings.TrimSpace(data) != "" {
			select {
			case c.cb <- Callback{ChatID: c.chatID, ID: "console", Data: strings.TrimSpace(data)}:
			case <-ctx.Done():
				return
			}
			continue
		}
		msg := c.parseLine(line)
		select {
		case c.in <- msg:
		case <-ctx.Done():
			return
		}
	}
}

// parseLine turns one stdin line into an inbound Msg, assigning it a fresh id.
// "@<id> text" becomes a reply to <id>; anything else is a plain/command message.
func (c *ConsoleClient) parseLine(line string) Msg {
	m := Msg{ChatID: c.chatID, ID: strconv.Itoa(c.nextID())}
	if strings.HasPrefix(line, "@") {
		rest := line[1:]
		idStr, text, _ := strings.Cut(rest, " ")
		if idStr != "" {
			m.ReplyToID = idStr
			m.Text = strings.TrimSpace(text)
			return m
		}
	}
	m.Text = line
	return m
}

// nextID returns the next shared monotonic message id.
func (c *ConsoleClient) nextID() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seq++
	return c.seq
}

// emit prints one outbound message under the write lock, assigning it an id.
// replyTo != "" renders the "↩#<replyto>" thread marker; tag (e.g. "menu") is an
// optional extra label after the id.
func (c *ConsoleClient) emit(replyTo string, tag, text string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seq++
	id := strconv.Itoa(c.seq)
	switch {
	case tag != "" && replyTo != "":
		fmt.Fprintf(c.out, "[#%s %s ↩#%s] %s\n", id, tag, replyTo, text)
	case tag != "":
		fmt.Fprintf(c.out, "[#%s %s] %s\n", id, tag, text)
	case replyTo != "":
		fmt.Fprintf(c.out, "[#%s ↩#%s] %s\n", id, replyTo, text)
	default:
		fmt.Fprintf(c.out, "[#%s] %s\n", id, text)
	}
	if f, ok := c.out.(flusher); ok {
		_ = f.Flush()
	}
	return id
}

// mark prints a non-message marker line (media uploads, edits) under the lock,
// without consuming a message id.
func (c *ConsoleClient) mark(format string, args ...any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	fmt.Fprintf(c.out, format+"\n", args...)
	if f, ok := c.out.(flusher); ok {
		_ = f.Flush()
	}
}

// silentTag renders a send's options as an emit tag: silent sends carry 🔕 so
// console runs (and their tests) can see what the real transport would hush.
func silentTag(opts []SendOpt) string {
	if sendSilent(opts) {
		return "🔕"
	}
	return ""
}

func (c *ConsoleClient) Send(_ context.Context, _ int64, text string, opts ...SendOpt) (string, error) {
	return c.emit("", silentTag(opts), text), nil
}

func (c *ConsoleClient) SendHTML(_ context.Context, _ int64, html string, opts ...SendOpt) (string, error) {
	return c.emit("", silentTag(opts), html), nil
}

func (c *ConsoleClient) SendReply(_ context.Context, _ int64, text string, replyTo string, opts ...SendOpt) (string, error) {
	return c.emit(replyTo, silentTag(opts), text), nil
}

func (c *ConsoleClient) SendHTMLReply(_ context.Context, _ int64, html string, replyTo string, opts ...SendOpt) (string, error) {
	return c.emit(replyTo, silentTag(opts), html), nil
}

func (c *ConsoleClient) Typing(_ context.Context, _ int64) error { return nil }

func (c *ConsoleClient) SendDocument(_ context.Context, _ int64, filename string, _ []byte, caption string, opts ...SendOpt) (string, error) {
	tag := "document " + filename
	if s := silentTag(opts); s != "" {
		tag = s + " " + tag
	}
	return c.emit("", tag, caption), nil
}

func (c *ConsoleClient) SendPhoto(_ context.Context, _ int64, filename string, _ []byte, caption string) error {
	c.mark("[media photo %s] %s", filename, caption)
	return nil
}

func (c *ConsoleClient) SendVoice(_ context.Context, _ int64, _ []byte, caption string) error {
	c.mark("[media voice] %s", caption)
	return nil
}

func (c *ConsoleClient) SendAudio(_ context.Context, _ int64, filename string, _ []byte, caption string) error {
	c.mark("[media audio %s] %s", filename, caption)
	return nil
}

func (c *ConsoleClient) SendVideo(_ context.Context, _ int64, filename string, _ []byte, caption string) error {
	c.mark("[media video %s] %s", filename, caption)
	return nil
}

func (c *ConsoleClient) SendMenu(_ context.Context, _ int64, text string, options []MenuOption) (string, error) {
	labels := make([]string, len(options))
	for i, o := range options {
		labels[i] = o.Label
	}
	return c.emit("", "menu", text+" {"+strings.Join(labels, " | ")+"}"), nil
}

func (c *ConsoleClient) EditPlain(_ context.Context, _ int64, msgID string, text string) error {
	c.mark("[edit #%s] %s", msgID, text)
	return nil
}

func (c *ConsoleClient) DeleteMessage(_ context.Context, _ int64, msgID string) error {
	c.mark("[delete #%s]", msgID)
	return nil
}

func (c *ConsoleClient) AnswerCallback(_ context.Context, _ string) error { return nil }

func (c *ConsoleClient) Callbacks(_ context.Context) <-chan Callback { return c.cb }
