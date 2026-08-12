//go:build unix

package telegram

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// jsonlProtocol is the wire protocol version EmitHello reports. Bumped only on
// breaking changes; additive event kinds don't count (clients ignore unknown
// types). Bumped 1 -> 2 when the inbound `callback` event and the outbound
// `menu`/`ack` events were removed along with the /voice inline-keyboard
// subsystem: a front-end still sending `callback` now silently hits the
// "ignoring unknown event type" path instead of getting a response, so the
// version number is a front-end's only signal that the wire it was built
// against no longer exists.
const jsonlProtocol = 2

// jsonlMaxMediaBytes caps how much of an inbound media file the transport
// reads into memory (mirroring the Telegram client's download cap in spirit;
// larger, since local disk is cheaper than a network fetch).
const jsonlMaxMediaBytes = 50 * 1024 * 1024

// ErrNoHTML is returned by the JSONL client's SendHTML/SendHTMLReply. The bot
// treats an HTML send error as "fall back to the plain path", which carries
// the reply's ORIGINAL markdown — so the wire gets markdown and Telegram's
// HTML subset never appears in the protocol.
var ErrNoHTML = errors.New("jsonl transport carries markdown, not HTML")

// JSONLClient is the tgClient for `shell3 serve`: newline-delimited JSON over
// an arbitrary reader/writer pair (stdin/stdout in production). It is the BYO
// front-end seam — a Discord bridge, a dashboard backend, or a test harness
// spawns `shell3 serve` and speaks this protocol. See docs/serve.md for the
// wire reference.
//
// Inbound (one JSON object per line):
//
//	{"type":"message","id":"…","reply_to_id":"…","text":"…","reply_to":"…","media":[{"path","mime","filename"}]}
//
// Malformed lines and unknown types are logged and ignored (forward compat);
// EOF closes the inbound channel, which stops Bot.Run — clean shutdown.
//
// Outbound: hello, send, media, edit, typing — each one line,
// mutex-serialized. Message ids are opaque strings: inbound ids are the
// client's own (assigned "c<boot>-<n>" when omitted), outbound ids are
// "a<boot>-<n>" where boot is the process start time, keeping agent ids unique
// across restarts so the persistent thread index stays valid.
type JSONLClient struct {
	in     chan Msg
	chatID int64
	spool  string // dir for outbound media files (created on first use)

	// Logf records skipped input (malformed lines, unreadable media). Defaults
	// to a no-op; the serve command points it at stderr.
	Logf func(format string, args ...any)

	mu   sync.Mutex // serializes writes to out and guards seq
	out  io.Writer
	seq  int
	boot int64

	r         io.Reader
	startOnce sync.Once
}

// NewJSONLClient builds a JSONL transport reading events from r and writing
// events to w. chatID is stamped on every inbound message (pass ConsoleChatID,
// which the Bot must be constructed with). spool is the directory outbound
// media bytes are written to before their path goes on the wire.
func NewJSONLClient(r io.Reader, w io.Writer, chatID int64, spool string) *JSONLClient {
	return &JSONLClient{
		in:     make(chan Msg, 16),
		chatID: chatID,
		spool:  spool,
		Logf:   func(string, ...any) {},
		out:    w,
		boot:   time.Now().Unix(),
		r:      r,
	}
}

// jsonlInEvent is the union of every inbound line's fields.
type jsonlInEvent struct {
	Type      string         `json:"type"`
	ID        string         `json:"id"`
	ReplyToID string         `json:"reply_to_id"`
	SenderID  int64          `json:"sender_id"`
	Text      string         `json:"text"`
	ReplyTo   string         `json:"reply_to"`
	Media     []jsonlInMedia `json:"media"`
}

type jsonlInMedia struct {
	Path     string `json:"path"`
	MIME     string `json:"mime"`
	Filename string `json:"filename"`
}

// jsonlOutEvent is the union of every outbound line's fields; omitempty keeps
// each event to its own vocabulary.
type jsonlOutEvent struct {
	Type      string    `json:"type"`
	Protocol  int       `json:"protocol,omitempty"`
	Commands  []Command `json:"commands,omitempty"`
	ID        string    `json:"id,omitempty"`
	ReplyToID string    `json:"reply_to_id,omitempty"`
	Text      string    `json:"text,omitempty"`
	Kind      string    `json:"kind,omitempty"`
	Path      string    `json:"path,omitempty"`
	Filename  string    `json:"filename,omitempty"`
	Caption   string    `json:"caption,omitempty"`
	Silent    bool      `json:"silent,omitempty"`
}

// Updates starts the read loop (once) and returns the inbound channel. The
// loop closes the channel on EOF or ctx cancellation, so Bot.Run returns.
func (c *JSONLClient) Updates(ctx context.Context) <-chan Msg {
	c.startOnce.Do(func() { go c.readLoop(ctx) })
	return c.in
}

// readLoop parses input lines into inbound messages until EOF or ctx done.
func (c *JSONLClient) readLoop(ctx context.Context) {
	defer close(c.in)
	sc := bufio.NewScanner(c.r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		if ctx.Err() != nil {
			return
		}
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev jsonlInEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			c.Logf("jsonl: ignoring malformed line: %v", err)
			continue
		}
		switch ev.Type {
		case "message":
			select {
			case c.in <- c.toMsg(ev):
			case <-ctx.Done():
				return
			}
		default:
			c.Logf("jsonl: ignoring unknown event type %q", ev.Type)
		}
	}
}

// toMsg projects a message event onto the transport-agnostic Msg, reading each
// media path into bytes. An unreadable or oversized file is skipped with a log
// line — the turn still runs with the message text.
func (c *JSONLClient) toMsg(ev jsonlInEvent) Msg {
	m := Msg{ChatID: c.chatID, ID: ev.ID, ReplyToID: ev.ReplyToID, Text: ev.Text, ReplyTo: ev.ReplyTo, SenderID: ev.SenderID}
	if m.SenderID == 0 {
		// A front-end that does not model senders (the common case for a
		// single-operator BYO front-end) is treated as the chat's owner
		// rather than as an anonymous — and therefore blocked — caller.
		m.SenderID = c.chatID
	}
	if m.ID == "" {
		m.ID = c.nextID("c")
	}
	for _, att := range ev.Media {
		info, err := os.Stat(att.Path)
		if err != nil {
			c.Logf("jsonl: skipping media %q: %v", att.Path, err)
			continue
		}
		if info.Size() > jsonlMaxMediaBytes {
			c.Logf("jsonl: skipping media %q: %d bytes exceeds the %d cap", att.Path, info.Size(), jsonlMaxMediaBytes)
			continue
		}
		data, err := os.ReadFile(att.Path)
		if err != nil {
			c.Logf("jsonl: skipping media %q: %v", att.Path, err)
			continue
		}
		name := att.Filename
		if name == "" {
			name = filepath.Base(att.Path)
		}
		m.Media = append(m.Media, Media{Bytes: data, MIME: att.MIME, Filename: name})
	}
	return m
}

// nextID returns the next outbound ("a") or assigned-inbound ("c") id,
// unique across restarts via the boot-time prefix.
func (c *JSONLClient) nextID(prefix string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seq++
	return fmt.Sprintf("%s%d-%d", prefix, c.boot, c.seq)
}

// emit writes one event line under the write lock.
func (c *JSONLClient) emit(ev jsonlOutEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, err := json.Marshal(ev)
	if err != nil {
		return
	}
	_, _ = c.out.Write(append(b, '\n'))
	if f, ok := c.out.(flusher); ok {
		_ = f.Flush()
	}
}

// EmitHello writes the handshake line: protocol version + the host-answered
// command menu, so a front-end can populate its own command UI.
func (c *JSONLClient) EmitHello(commands []Command) {
	c.emit(jsonlOutEvent{Type: "hello", Protocol: jsonlProtocol, Commands: commands})
}

func (c *JSONLClient) Send(_ context.Context, _ int64, text string, opts ...SendOpt) (string, error) {
	id := c.nextID("a")
	c.emit(jsonlOutEvent{Type: "send", ID: id, Text: text, Silent: sendSilent(opts)})
	return id, nil
}

// SendHTML never emits: the bot's HTML→plain fallback re-sends through
// Send/SendReply with the original markdown, which is what belongs on the wire.
func (c *JSONLClient) SendHTML(_ context.Context, _ int64, _ string, _ ...SendOpt) (string, error) {
	return "", ErrNoHTML
}

func (c *JSONLClient) SendReply(_ context.Context, _ int64, text string, replyTo string, opts ...SendOpt) (string, error) {
	id := c.nextID("a")
	c.emit(jsonlOutEvent{Type: "send", ID: id, ReplyToID: replyTo, Text: text, Silent: sendSilent(opts)})
	return id, nil
}

func (c *JSONLClient) SendHTMLReply(_ context.Context, _ int64, _ string, _ string, _ ...SendOpt) (string, error) {
	return "", ErrNoHTML
}

// DeleteMessage emits a delete event; a front-end that can't delete may
// ignore it.
func (c *JSONLClient) DeleteMessage(_ context.Context, _ int64, msgID string) error {
	c.emit(jsonlOutEvent{Type: "delete", ID: msgID})
	return nil
}

func (c *JSONLClient) Typing(_ context.Context, _ int64) error {
	c.emit(jsonlOutEvent{Type: "typing"})
	return nil
}

// spoolFile writes data to the spool dir under a sequence-unique name and
// returns its path — outbound media crosses the wire as a file path.
func (c *JSONLClient) spoolFile(filename string, data []byte) (string, error) {
	if err := os.MkdirAll(c.spool, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(c.spool, fmt.Sprintf("%s-%s", c.nextID("f"), filepath.Base(filename)))
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// sendMedia spools one outbound file and emits its media event. id may be ""
// (only document sends carry an id — the seam returns one only there).
func (c *JSONLClient) sendMedia(id, kind, filename string, data []byte, caption string, silent bool) error {
	path, err := c.spoolFile(filename, data)
	if err != nil {
		return err
	}
	c.emit(jsonlOutEvent{Type: "media", ID: id, Kind: kind, Path: path, Filename: filepath.Base(filename), Caption: caption, Silent: silent})
	return nil
}

func (c *JSONLClient) SendDocument(_ context.Context, _ int64, filename string, data []byte, caption string, opts ...SendOpt) (string, error) {
	id := c.nextID("a")
	if err := c.sendMedia(id, "document", filename, data, caption, sendSilent(opts)); err != nil {
		return "", err
	}
	return id, nil
}

func (c *JSONLClient) SendPhoto(_ context.Context, _ int64, filename string, data []byte, caption string) error {
	return c.sendMedia("", "photo", filename, data, caption, false)
}

func (c *JSONLClient) SendVoice(_ context.Context, _ int64, data []byte, caption string) error {
	return c.sendMedia("", "voice", "voice.ogg", data, caption, false)
}

func (c *JSONLClient) SendAudio(_ context.Context, _ int64, filename string, data []byte, caption string) error {
	return c.sendMedia("", "audio", filename, data, caption, false)
}

func (c *JSONLClient) SendVideo(_ context.Context, _ int64, filename string, data []byte, caption string) error {
	return c.sendMedia("", "video", filename, data, caption, false)
}

func (c *JSONLClient) EditPlain(_ context.Context, _ int64, msgID string, text string) error {
	c.emit(jsonlOutEvent{Type: "edit", ID: msgID, Text: text})
	return nil
}
