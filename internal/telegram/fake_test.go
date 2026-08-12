//go:build unix

package telegram

import (
	"context"
	"errors"
	"strconv"
	"sync"
)

var errFakeHTML = errors.New("fake: html rejected")

type fakeClient struct {
	in       chan Msg
	cb       chan Callback
	mu       sync.Mutex
	sent     []sentMsg
	html     []string
	failHTML bool
	next     int
	docs     []sentDoc
	confirms []sentConfirm
	edits    []sentEdit
	answered []string
	photos   []sentPhoto
	voices   []sentVoice
	audios   []sentAudio
	videos   []sentVideo
	menus    []sentMenu
	replies  []sentReply
	deleted  []string
	silent   []bool // one entry per text/document send, true when SendOpt.Silent

	failReply error // when set, reply sends behave as a deleted target (fall back to plain)

	failDoc   error
	failPhoto error
	failVoice error
	failAudio error
	failVideo error
	failMenu  error
}

type sentPhoto struct {
	chatID   int64
	filename string
	data     []byte
	caption  string
}

type sentVoice struct {
	chatID  int64
	data    []byte
	caption string
}

type sentAudio struct {
	chatID   int64
	filename string
	data     []byte
	caption  string
}

type sentVideo struct {
	chatID   int64
	filename string
	data     []byte
	caption  string
}

type sentMenu struct {
	msgID   string
	chatID  int64
	text    string
	options []MenuOption
}

type sentConfirm struct {
	msgID           string
	text            string
	yesData, noData string
}

type sentEdit struct {
	msgID string
	text  string
}

type sentDoc struct {
	chatID   int64
	filename string
	data     []byte
	caption  string
}

type sentMsg struct {
	chatID int64
	text   string
}

type sentReply struct {
	msgID   string
	chatID  int64
	replyTo string
	text    string
	html    bool
}

func newFakeClient() *fakeClient {
	return &fakeClient{in: make(chan Msg, 16), cb: make(chan Callback, 8)}
}

func (f *fakeClient) Updates(ctx context.Context) <-chan Msg { return f.in }

func (f *fakeClient) Callbacks(ctx context.Context) <-chan Callback { return f.cb }

func (f *fakeClient) SendConfirm(ctx context.Context, chatID int64, text, yesData, noData string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next++
	id := strconv.Itoa(f.next)
	f.confirms = append(f.confirms, sentConfirm{msgID: id, text: text, yesData: yesData, noData: noData})
	return id, nil
}

func (f *fakeClient) EditPlain(ctx context.Context, chatID int64, msgID string, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.edits = append(f.edits, sentEdit{msgID: msgID, text: text})
	return nil
}

func (f *fakeClient) DeleteMessage(ctx context.Context, chatID int64, msgID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, msgID)
	return nil
}

// deletedSnapshot returns the ids deleted so far.
func (f *fakeClient) deletedSnapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.deleted...)
}

func (f *fakeClient) AnswerCallback(ctx context.Context, callbackID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answered = append(f.answered, callbackID)
	return nil
}

// reset clears everything the fake has recorded, so a test can assert on just
// the traffic after a setup step.
func (f *fakeClient) reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent, f.html, f.docs, f.replies = nil, nil, nil, nil
}

func (f *fakeClient) SendDocument(ctx context.Context, chatID int64, filename string, data []byte, caption string, opts ...SendOpt) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failDoc != nil {
		return "", f.failDoc
	}
	f.docs = append(f.docs, sentDoc{chatID: chatID, filename: filename, data: data, caption: caption})
	f.silent = append(f.silent, sendSilent(opts))
	f.next++
	return strconv.Itoa(f.next), nil
}

func (f *fakeClient) SendPhoto(ctx context.Context, chatID int64, filename string, data []byte, caption string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failPhoto != nil {
		return f.failPhoto
	}
	f.photos = append(f.photos, sentPhoto{chatID: chatID, filename: filename, data: data, caption: caption})
	return nil
}

func (f *fakeClient) SendVoice(ctx context.Context, chatID int64, data []byte, caption string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failVoice != nil {
		return f.failVoice
	}
	f.voices = append(f.voices, sentVoice{chatID: chatID, data: data, caption: caption})
	return nil
}

func (f *fakeClient) SendAudio(ctx context.Context, chatID int64, filename string, data []byte, caption string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failAudio != nil {
		return f.failAudio
	}
	f.audios = append(f.audios, sentAudio{chatID: chatID, filename: filename, data: data, caption: caption})
	return nil
}

func (f *fakeClient) SendVideo(ctx context.Context, chatID int64, filename string, data []byte, caption string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failVideo != nil {
		return f.failVideo
	}
	f.videos = append(f.videos, sentVideo{chatID: chatID, filename: filename, data: data, caption: caption})
	return nil
}

func (f *fakeClient) SendMenu(ctx context.Context, chatID int64, text string, options []MenuOption) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failMenu != nil {
		return "", f.failMenu
	}
	f.next++
	id := strconv.Itoa(f.next)
	f.menus = append(f.menus, sentMenu{msgID: id, chatID: chatID, text: text, options: options})
	return id, nil
}

func (f *fakeClient) lastDoc() (sentDoc, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.docs) == 0 {
		return sentDoc{}, false
	}
	return f.docs[len(f.docs)-1], true
}

func (f *fakeClient) Send(ctx context.Context, chatID int64, text string, opts ...SendOpt) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next++
	f.sent = append(f.sent, sentMsg{chatID: chatID, text: text})
	f.silent = append(f.silent, sendSilent(opts))
	return strconv.Itoa(f.next), nil
}

// lastSilent reports whether the most recent text/document send was silent.
func (f *fakeClient) lastSilent() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.silent) > 0 && f.silent[len(f.silent)-1]
}

func (f *fakeClient) SendHTML(ctx context.Context, chatID int64, html string, opts ...SendOpt) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failHTML {
		return "", errFakeHTML
	}
	f.next++
	f.html = append(f.html, html)
	f.silent = append(f.silent, sendSilent(opts))
	return strconv.Itoa(f.next), nil
}

// SendReply records a plain threaded reply. failReply simulates a deleted
// target: the reply degrades to a plain Send (replyTo cleared), mirroring the
// real client's fallback.
func (f *fakeClient) SendReply(ctx context.Context, chatID int64, text string, replyTo string, opts ...SendOpt) (string, error) {
	if f.failReply != nil {
		return f.Send(ctx, chatID, text, opts...)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next++
	id := strconv.Itoa(f.next)
	f.replies = append(f.replies, sentReply{msgID: id, chatID: chatID, replyTo: replyTo, text: text})
	f.silent = append(f.silent, sendSilent(opts))
	return id, nil
}

func (f *fakeClient) SendHTMLReply(ctx context.Context, chatID int64, html string, replyTo string, opts ...SendOpt) (string, error) {
	if f.failHTML {
		return "", errFakeHTML
	}
	if f.failReply != nil {
		return f.SendHTML(ctx, chatID, html, opts...)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next++
	id := strconv.Itoa(f.next)
	f.replies = append(f.replies, sentReply{msgID: id, chatID: chatID, replyTo: replyTo, text: html, html: true})
	f.silent = append(f.silent, sendSilent(opts))
	return id, nil
}

// lastReply returns the most recent threaded reply, or ok=false if none.
func (f *fakeClient) lastReply() (sentReply, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.replies) == 0 {
		return sentReply{}, false
	}
	return f.replies[len(f.replies)-1], true
}

// sentReplies returns a copy of every threaded reply sent so far.
func (f *fakeClient) sentReplies() []sentReply {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]sentReply, len(f.replies))
	copy(out, f.replies)
	return out
}

// lastSentID returns the message id of the most recent send of any kind.
func (f *fakeClient) lastSentID() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strconv.Itoa(f.next)
}

func (f *fakeClient) htmlTexts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.html))
	copy(out, f.html)
	return out
}

func (f *fakeClient) Typing(ctx context.Context, chatID int64) error { return nil }

// sentTexts returns every user-facing message regardless of parse mode: the
// HTML messages (the normal path) plus any plain-text fallbacks. Tests assert
// on substrings that survive Markdown→HTML conversion unchanged.
func (f *fakeClient) sentTexts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.html)+len(f.sent)+len(f.replies))
	out = append(out, f.html...)
	for _, m := range f.sent {
		out = append(out, m.text)
	}
	for _, r := range f.replies {
		out = append(out, r.text)
	}
	return out
}

// plainTexts returns only messages sent without a parse mode (the fallback path).
func (f *fakeClient) plainTexts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.sent))
	for i, m := range f.sent {
		out[i] = m.text
	}
	return out
}
