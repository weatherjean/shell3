//go:build unix

package telegram

import (
	"context"
	"time"

	"github.com/weatherjean/shell3/internal/inbox"
)

// WakeInbox schedules one coalesced home-chat continuation. Durable filesystem
// state is authoritative; wake hints neither carry bodies nor bypass turn slots.
func (b *Bot) WakeInbox(ctx context.Context, store inbox.Store) error {
	_, count, err := store.List("main", inbox.StatusPending, 0, 1)
	if err != nil || count == 0 {
		return err
	}
	c := b.homeConv()
	c.mu.Lock()
	c.inboxStore = &store
	c.inboxPending = true
	paused := c.inboxPaused
	c.mu.Unlock()
	if paused {
		return nil
	}
	if _, err := c.mainSession(); err != nil {
		return err
	}
	c.startNextWork(ctx)
	return nil
}

func (c *conversation) startInboxTurn(ctx context.Context, now time.Time) bool {
	c.mu.Lock()
	if ctx.Err() != nil || c.turnActive || c.main == nil || !c.inboxPending || c.inboxPaused || c.inboxStore == nil || now.Before(c.inboxRetryAt) || len(c.pendingMessages) > 0 || len(c.burst) > 0 || c.main.HasQueuedSteer() || c.b.RestartPending() {
		c.mu.Unlock()
		return false
	}
	turnCtx, cancel, ok := c.takeSlotLocked(ctx)
	if !ok {
		c.mu.Unlock()
		return false
	}
	sess, anchor, store := c.main, c.mainAnchor, *c.inboxStore
	c.mu.Unlock()
	go func() {
		batch, err := store.PrepareBatch()
		if err != nil || batch == nil {
			c.mu.Lock()
			if err == nil {
				_, count, listErr := store.List("main", inbox.StatusPending, 0, 1)
				if listErr == nil && count == 0 {
					c.inboxPending = false
				}
			}
			c.inboxRetryAt = time.Now().Add(time.Second)
			if err != nil {
				c.inboxRetryAt = time.Now().Add(time.Minute)
			}
			c.cancelTurn = nil
			c.turnActive = false
			c.b.freeTurn()
			c.mu.Unlock()
			cancel()
			if err != nil {
				c.b.log.Warn("inbox delivery preparation failed", "error", err)
			}
			c.b.finishRestartDrain(nil)
			c.b.startNextWorkAll(ctx, c)
			return
		}
		stopTyping := c.keepTyping(ctx)
		reply, failed := c.drainTurnProgress(ctx, sess.SendInbox(turnCtx, batch))
		stopTyping()
		c.mu.Lock()
		if failed {
			c.inboxRetryAt = time.Now().Add(time.Minute)
		} else {
			c.inboxRetryAt = time.Time{}
			// Reconcile before releasing the turn slot. Otherwise an idle room
			// briefly advertises pending work after the final notice archived,
			// competing with checks until a second empty turn clears the flag.
			if _, count, err := store.List("main", inbox.StatusPending, 0, 1); err == nil {
				c.inboxPending = count > 0
			}
		}
		c.mu.Unlock()
		c.finishPostedTurn(ctx, sess, anchor, reply, cancel)
	}()
	return true
}
