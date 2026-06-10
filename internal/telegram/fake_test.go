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
}

type sentMsg struct {
	chatID  int64
	text    string
	buttons []Button
	edited  bool
}

func newFakeClient() *fakeClient { return &fakeClient{in: make(chan Msg, 16)} }

func (f *fakeClient) Updates(ctx context.Context) <-chan Msg { return f.in }

func (f *fakeClient) Send(ctx context.Context, chatID int64, text string, buttons []Button) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next++
	f.sent = append(f.sent, sentMsg{chatID: chatID, text: text, buttons: buttons})
	return f.next, nil
}
func (f *fakeClient) EditText(ctx context.Context, chatID int64, msgID int, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, sentMsg{chatID: chatID, text: text, edited: true})
	return nil
}
func (f *fakeClient) Typing(ctx context.Context, chatID int64) error              { return nil }
func (f *fakeClient) AnswerCallback(ctx context.Context, callbackID string) error { return nil }

func (f *fakeClient) sentTexts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.sent))
	for i, m := range f.sent {
		out[i] = m.text
	}
	return out
}

// lastButtons returns the buttons of the last sent (non-edited) message, or nil.
func (f *fakeClient) lastButtons() []Button {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.sent) - 1; i >= 0; i-- {
		if !f.sent[i].edited {
			return f.sent[i].buttons
		}
	}
	return nil
}
