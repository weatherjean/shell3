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

// ConsoleClient drives the bot loop over stdin/stdout for development and tests.
//
// Inbound (stdin, one line per message):
//   - a plain line              → a fresh message
//   - "@<id> text"              → a reply to message <id> (thread continuation)
//   - "/..."                    → a bot command, as usual
//   - a blank line              → skipped
//
// Outbound (stdout, one line per message):
//   - "[#<id>] text"            → a plain/HTML send (HTML is printed raw, unrendered)
//   - "[#<id> ↩#<replyto>] text" → a threaded reply
//   - "[media …]" markers for document/photo/voice/audio/video uploads (no-ops)
//
// EOF closes the inbound channel. Inbound and outbound messages share one
// monotonic ID sequence, so scripts can reply to printed IDs.
type ConsoleClient struct {
	in     chan Msg
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
		msg := c.parseLine(line)
		select {
		case c.in <- msg:
		case <-ctx.Done():
			return
		}
	}
}

// parseLine turns one stdin line into an inbound Msg, assigning it a fresh id.
// "#<chatid> text" sends into another room (the way to drive several
// conversations by hand); "@<id> text" becomes a reply to <id>; anything else
// is a plain/command message in the default chat.
func (c *ConsoleClient) parseLine(line string) Msg {
	// The console has no Telegram identity. Treat input as coming from the
	// configured chat's owner: the operator is already at the keyboard, and
	// pretending otherwise would only lock them out of their own console.
	m := Msg{ChatID: c.chatID, SenderID: c.chatID, ID: strconv.Itoa(c.nextID()), ChatType: "private"}
	// A "#<chatid> " prefix routes the line into another room, so the whole
	// multi-room loop — separate conversations, concurrent turns, per-room
	// /new — is drivable with no credentials and no network. The room is
	// treated as a GROUP (any chat that is not the console's own default is
	// one), which means the line must also address the bot to count: exactly
	// what a live group demands.
	if rest, ok := strings.CutPrefix(line, "#"); ok {
		idStr, text, found := strings.Cut(rest, " ")
		if id, err := strconv.ParseInt(idStr, 10, 64); err == nil && found {
			m.ChatID = id
			if id != c.chatID {
				m.ChatType = "supergroup"
			}
			line = strings.TrimSpace(text)
			m.Text = line
			if line == "" {
				return m
			}
			// fall through so "@<id>" reply syntax still works after the room
			// prefix
			if strings.HasPrefix(line, "@") {
				idStr, text, _ := strings.Cut(line[1:], " ")
				if _, err := strconv.Atoi(idStr); err == nil {
					m.ReplyToID, m.ReplyToBot = idStr, true
					m.Text = strings.TrimSpace(text)
				}
			}
			return m
		}
	}
	if strings.HasPrefix(line, "@") {
		rest := line[1:]
		idStr, text, _ := strings.Cut(rest, " ")
		if idStr != "" {
			// One bot on this transport, so a reply is a reply to us.
			m.ReplyToID, m.ReplyToBot = idStr, true
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

func (c *ConsoleClient) EditPlain(_ context.Context, _ int64, msgID string, text string) error {
	c.mark("[edit #%s] %s", msgID, text)
	return nil
}

func (c *ConsoleClient) DeleteMessage(_ context.Context, _ int64, msgID string) error {
	c.mark("[delete #%s]", msgID)
	return nil
}

// Username is the console transport's fixed identity: the bot loop needs a
// name to spot @mentions, and a dev transport has no Bot API to ask.
func (c *ConsoleClient) Username(context.Context) (string, error) { return "shell3console", nil }

// ChatInfo gives the console transport's rooms a stable title so a room brief
// renders in --console exactly as it does live.
func (c *ConsoleClient) ChatInfo(_ context.Context, chatID int64) (string, string, error) {
	return "console", "", nil
}
