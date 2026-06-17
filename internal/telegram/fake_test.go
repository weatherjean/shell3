//go:build unix

package telegram

import (
	"context"
	"errors"
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

func newFakeClient() *fakeClient { return &fakeClient{in: make(chan Msg, 16)} }

func (f *fakeClient) Updates(ctx context.Context) <-chan Msg { return f.in }

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
