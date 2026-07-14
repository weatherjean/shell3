//go:build unix

package telegram

import (
	"context"
	"errors"
	"strings"
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
}

type sentConfirm struct {
	msgID           int
	text            string
	yesData, noData string
}

type sentEdit struct {
	msgID int
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

func newFakeClient() *fakeClient {
	return &fakeClient{in: make(chan Msg, 16), cb: make(chan Callback, 8)}
}

func (f *fakeClient) Updates(ctx context.Context) <-chan Msg { return f.in }

func (f *fakeClient) Callbacks(ctx context.Context) <-chan Callback { return f.cb }

func (f *fakeClient) SendConfirm(ctx context.Context, chatID int64, text, yesData, noData string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next++
	f.confirms = append(f.confirms, sentConfirm{msgID: f.next, text: text, yesData: yesData, noData: noData})
	return f.next, nil
}

func (f *fakeClient) EditPlain(ctx context.Context, chatID int64, msgID int, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.edits = append(f.edits, sentEdit{msgID: msgID, text: text})
	return nil
}

func (f *fakeClient) AnswerCallback(ctx context.Context, callbackID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answered = append(f.answered, callbackID)
	return nil
}

// lastConfirm returns the most recent inline-confirm sent, or ok=false if none.
func (f *fakeClient) lastConfirm() (sentConfirm, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.confirms) == 0 {
		return sentConfirm{}, false
	}
	return f.confirms[len(f.confirms)-1], true
}

// confirmMatching returns the most recent inline-confirm whose text contains
// sub, or ok=false if none. Lets a test pick out one of several pending asks.
func (f *fakeClient) confirmMatching(sub string) (sentConfirm, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.confirms) - 1; i >= 0; i-- {
		if strings.Contains(f.confirms[i].text, sub) {
			return f.confirms[i], true
		}
	}
	return sentConfirm{}, false
}

// editTexts returns every edit's text in order.
func (f *fakeClient) editTexts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.edits))
	for i, e := range f.edits {
		out[i] = e.text
	}
	return out
}

func (f *fakeClient) SendDocument(ctx context.Context, chatID int64, filename string, data []byte, caption string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.docs = append(f.docs, sentDoc{chatID: chatID, filename: filename, data: data, caption: caption})
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

func (f *fakeClient) Send(ctx context.Context, chatID int64, text string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next++
	f.sent = append(f.sent, sentMsg{chatID: chatID, text: text})
	return f.next, nil
}
func (f *fakeClient) SendHTML(ctx context.Context, chatID int64, html string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failHTML {
		return 0, errFakeHTML
	}
	f.next++
	f.html = append(f.html, html)
	return f.next, nil
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
	out := make([]string, 0, len(f.html)+len(f.sent))
	out = append(out, f.html...)
	for _, m := range f.sent {
		out = append(out, m.text)
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
