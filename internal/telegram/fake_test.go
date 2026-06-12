//go:build unix

package telegram

import (
	"context"
	"sync"
)

type fakeClient struct {
	in   chan Msg
	mu   sync.Mutex
	sent []sentMsg
	next int
	docs []sentDoc
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
func (f *fakeClient) Typing(ctx context.Context, chatID int64) error { return nil }

func (f *fakeClient) sentTexts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.sent))
	for i, m := range f.sent {
		out[i] = m.text
	}
	return out
}
