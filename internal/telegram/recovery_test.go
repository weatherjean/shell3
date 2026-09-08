//go:build unix

package telegram

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/weatherjean/shell3/internal/applog"
)

func TestFreshRoomAcceptsReplyToBot(t *testing.T) {
	if !roomAddressed(nil, Msg{ReplyToBot: true}, "bot") {
		t.Fatal("reply lost after restart")
	}
	if roomAddressed(nil, Msg{Text: "unaddressed"}, "bot") {
		t.Fatal("unaddressed message opened a room")
	}
	client := newFakeClient()
	rt, _ := newFakeRuntime(t, "resumed from reply")
	b := newBot(t, client, rt)
	b.handleMsg(t.Context(), Msg{ChatID: "-100", ChatType: "supergroup", SenderID: 42, ID: "fresh", Text: "continue", ReplyToBot: true})
	if !waitForReply(t, client, "resumed from reply") {
		t.Fatal("fresh-room reply did not reach a turn")
	}
}

func TestUpdateQueueUnblocksOnCancellation(t *testing.T) {
	c := &BotAPIClient{out: make(chan Msg), log: applog.Noop{}, health: newPollHealth()}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.onUpdate(ctx, nil, &models.Update{Message: &models.Message{ID: 1}})
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("update delivery blocked shutdown")
	}
}

func TestMediaDownloadRejectsHTTPFailure(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusNotFound, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/getFile") {
					_, _ = w.Write([]byte(`{"ok":true,"result":{"file_path":"media","file_id":"f"}}`))
					return
				}
				w.WriteHeader(status)
				_, _ = w.Write([]byte("body"))
			}))
			defer srv.Close()
			b, err := bot.New("token", bot.WithServerURL(srv.URL), bot.WithSkipGetMe())
			if err != nil {
				t.Fatal(err)
			}
			c := &BotAPIClient{b: b}
			media, ok := c.downloadFile(context.Background(), "f", "text/plain", "file")
			if ok != (status == http.StatusOK) {
				t.Fatalf("HTTP %d accepted=%t", status, ok)
			}
			if ok && string(media.Bytes) != "body" {
				t.Fatalf("body = %q", media.Bytes)
			}
		})
	}
}

func TestSessionIndexKeepsPreviousValueOnFailedClear(t *testing.T) {
	store := testStore(t)
	idx := NewSessionIndex(store, "telegram")
	if err := idx.SetCurrent("original"); err != nil {
		t.Fatal(err)
	}
	if err := store().Close(); err != nil {
		t.Fatal(err)
	}
	if err := idx.SetCurrent(""); err == nil {
		t.Fatal("expected persistence failure")
	}
	if id, ok := idx.Current(); !ok || id != "original" {
		t.Fatalf("lost previous marker: %q, %t", id, ok)
	}
}

func TestRoomTransitionFailsBeforePublishingOnMarkerError(t *testing.T) {
	for _, fresh := range []bool{false, true} {
		t.Run(map[bool]string{false: "create", true: "new"}[fresh], func(t *testing.T) {
			client := newFakeClient()
			rt, _ := newFakeRuntime(t, "unused")
			b := newBot(t, client, rt)
			c := tconv(b)
			previous, err := c.mainSession()
			if err != nil {
				t.Fatal(err)
			}
			store := testStore(t)
			if err := store().Close(); err != nil {
				t.Fatal(err)
			}
			c.mu.Lock()
			c.index = NewSessionIndex(store, "failed")
			if !fresh {
				c.main = nil
			}
			c.mu.Unlock()
			if fresh {
				c.handleNewCommand(t.Context())
				c.mu.Lock()
				same := c.main == previous
				c.mu.Unlock()
				if !same {
					t.Fatal("detached session after failed clear")
				}
			} else {
				if session, err := c.mainSession(); err == nil || session != nil {
					t.Fatalf("session=%v err=%v", session, err)
				}
				c.mu.Lock()
				empty := c.main == nil
				c.mu.Unlock()
				if !empty {
					t.Fatal("published session after marker failure")
				}
			}
		})
	}
}
