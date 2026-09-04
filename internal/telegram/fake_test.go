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
	mu       sync.Mutex
	sent     []sentMsg
	html     []string
	failHTML bool
	next     int
	docs     []sentDoc
	edits    []sentEdit
	photos   []sentPhoto
	voices   []sentVoice
	audios   []sentAudio
	videos   []sentVideo
	replies  []sentReply
	deleted  []string
	silent   []bool

	failReply error

	chatTitle     string
	chatDesc      string
	failChatInfo  error
	blockChatInfo chan struct{}
	chatInfoN     int

	failDoc   error
	failPhoto error
	failVoice error
	failAudio error
	failVideo error
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
	return &fakeClient{in: make(chan Msg, 16)}
}

func (f *fakeClient) Updates(ctx context.Context) <-chan Msg { return f.in }

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

func (f *fakeClient) deletedSnapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.deleted...)
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

func (f *fakeClient) lastReply() (sentReply, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.replies) == 0 {
		return sentReply{}, false
	}
	return f.replies[len(f.replies)-1], true
}

func (f *fakeClient) sentReplies() []sentReply {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]sentReply, len(f.replies))
	copy(out, f.replies)
	return out
}

func (f *fakeClient) htmlTexts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.html))
	copy(out, f.html)
	return out
}

func (f *fakeClient) Typing(ctx context.Context, chatID int64) error { return nil }

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

func (f *fakeClient) plainTexts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.sent))
	for i, m := range f.sent {
		out[i] = m.text
	}
	return out
}

func (f *fakeClient) Username(context.Context) (string, error) { return "mybot", nil }

func (f *fakeClient) ChatInfo(_ context.Context, chatID int64) (string, string, error) {
	f.mu.Lock()
	f.chatInfoN++
	block, fail := f.blockChatInfo, f.failChatInfo
	title, desc := f.chatTitle, f.chatDesc
	f.mu.Unlock()
	if block != nil {
		<-block
	}
	if fail != nil {
		return "", "", fail
	}
	return title, desc, nil
}

func (f *fakeClient) chatInfoCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.chatInfoN
}
