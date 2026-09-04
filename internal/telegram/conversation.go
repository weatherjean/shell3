//go:build unix

package telegram

// conversation is ONE chat's conversation: the long-lived session every
// message there continues, plus that room's turn slot, anchors and queues.
// One per chat, so two rooms never share a context window, an anchor or a turn.
//
// Locking: c.mu guards this room's state, b.mu the Bot-wide registry and
// wiring. Take b.mu inside c.mu, never the other way round.

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/weatherjean/shell3/internal/shell3"
)

type conversation struct {
	b       *Bot
	chatID  int64
	isGroup bool
	// index persists which store session is this room's conversation, under
	// the room's own surface key, so a restart resumes every room.
	index *SessionIndex

	mu sync.Mutex // guards this room's turn slot, session, anchors and queues
	// main is the long-lived session every message here continues, replaced
	// only by a /new in this room.
	main *shell3.Session
	// mainAnchor is the latest chat message id replies thread onto.
	mainAnchor string
	// steerAnchor is the newest steered message id, separate so the running
	// turn's own reply cannot clobber it.
	steerAnchor string
	// contextMilestone is the highest fullness threshold announced in this
	// growth cycle. Compaction and /new reset it.
	contextMilestone int
	cancelTurn       context.CancelFunc // non-nil while a turn runs in this room
	turnActive       bool
	pendingMessages  []inboundMessage
	burst            []inboundMessage
	burstTimer       *time.Timer
	// sentIDs rings the message ids the bot posted here: a group message
	// replying to one is addressed to the bot, which short of an @mention is
	// the only way to tell "answer me" from room chatter.
	sentIDs []string
}

// sentIDsCap bounds the ring: enough that a reply to anything still on screen
// resolves, small enough to stay free.
const sentIDsCap = 200

// rememberSent records a message id the bot posted in this room.
func (c *conversation) rememberSent(msgID string) {
	if msgID == "" {
		return
	}
	c.mu.Lock()
	c.sentIDs = append(c.sentIDs, msgID)
	if len(c.sentIDs) > sentIDsCap {
		c.sentIDs = c.sentIDs[len(c.sentIDs)-sentIDsCap:]
	}
	c.mu.Unlock()
}

// wasSent reports whether msgID is a message this bot posted in this room.
func (c *conversation) wasSent(msgID string) bool {
	if msgID == "" {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, id := range c.sentIDs {
		if id == msgID {
			return true
		}
	}
	return false
}

// flushBurst dispatches the buffered text burst, if any.
func (c *conversation) flushBurst(ctx context.Context) {
	c.mu.Lock()
	batch := c.burst
	if c.burstTimer != nil {
		c.burstTimer.Stop()
	}
	c.burst, c.burstTimer = nil, nil
	c.mu.Unlock()
	if len(batch) > 0 {
		c.dispatchMessages(ctx, batch)
	}
}

// dispatchMessages routes a batch: mid-turn TEXT steers the running turn at the
// next round boundary, so "stop, wrong file" redirects work in flight rather
// than waiting behind it; media queues; an idle bot runs the batch as one
// turn. A steer landing after the final boundary gets a catch-up turn.
func (c *conversation) dispatchMessages(ctx context.Context, batch []inboundMessage) {
	if len(batch) == 0 {
		return
	}
	hasMedia := false
	for _, item := range batch {
		hasMedia = hasMedia || len(item.saved) > 0
	}
	var steerText string
	if !hasMedia {
		parts := make([]string, 0, len(batch))
		for _, item := range batch {
			parts = append(parts, withReplyContext(item.text, item.m.ReplyTo))
		}
		steerText = strings.Join(parts, "\n\n")
	}
	c.mu.Lock()
	if c.turnActive {
		if !hasMedia && c.main != nil {
			sess := c.main
			c.mainAnchor = batch[len(batch)-1].m.ID
			c.steerAnchor = c.mainAnchor
			// Queue while holding c.mu. Turn completion takes the same lock
			// before scanning for catch-up work, so it cannot miss this steer.
			sess.Interject(steerText)
			c.mu.Unlock()
			return
		}
		c.pendingMessages = append(c.pendingMessages, batch...)
		c.mu.Unlock()
		return
	}
	// Slot before session, so another message landing during creation queues
	// instead of racing onto it.
	turnCtx, cancel, ok := c.takeSlotLocked(ctx)
	if !ok {
		// Cap full: queue rather than refuse. Sending always succeeds, and the
		// backlog drains as one batch turn when a slot frees.
		c.pendingMessages = append(c.pendingMessages, batch...)
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()

	go c.runUserTurn(ctx, turnCtx, cancel, batch)
}

// runUserTurn runs one user turn over batch — one message or the backlog. The
// slot is held on entry; this goroutine owns delivery, release, and starting
// the next queued work.
func (c *conversation) runUserTurn(ctx, turnCtx context.Context, cancel context.CancelFunc, batch []inboundMessage) {
	last := batch[len(batch)-1]
	sess, err := c.mainSession()
	if err != nil {
		c.releaseSlot(cancel)
		c.sendReply(ctx, "⚠️ could not start a session: "+err.Error())
		// Work queued against the held slot during the failed creation would
		// otherwise wait for the next event.
		c.startNextWork(ctx)
		return
	}
	c.setAnchor(last.m.ID) // the reply anchors at the newest message

	composeText := func() string {
		parts := make([]string, 0, len(batch))
		for _, item := range batch {
			out := item.text
			if injected := attachmentNote(item.saved); injected != "" {
				if out != "" {
					out += "\n\n" + injected
				} else {
					out = injected
				}
			}
			parts = append(parts, withReplyContext(out, item.m.ReplyTo))
		}
		return strings.Join(parts, "\n\n")
	}

	stopTyping := c.keepTyping(ctx)
	finalText := composeText()
	reply, _ := c.drainTurnProgress(ctx, sess.Send(turnCtx, finalText))
	stopTyping()
	c.finishPostedTurn(ctx, sess, last.m.ID, reply, cancel)
}

// finishPostedTurn delivers a POSTED turn's reply and runs the housekeeping
// both posted paths share, a user turn and a steer catch-up.
//
// A posted turn always answers ("" renders as "(no output)"). A corrupt reply
// containing raw tool-call markup becomes a short notice rather than protocol
// garbage.
//
// The slot is held through delivery, so nothing else can start a turn.
func (c *conversation) finishPostedTurn(ctx context.Context, sess *shell3.Session, anchor, reply string, cancel context.CancelFunc) {
	if containsToolMarkup(reply) {
		reply = malformedReplyNotice
	}
	c.postReply(ctx, sess, anchor, reply)
	c.markCurrent(sess)
	c.releaseSlot(cancel)
	c.b.startNextWorkAll(ctx, c)
}

// mainSession is the room's session, created on first use: the persisted
// marker resumes the previous run's, and with no marker — or one the janitor
// swept — a fresh session starts and is recorded as current.
func (c *conversation) mainSession() (*shell3.Session, error) {
	c.mu.Lock()
	if c.main != nil {
		s := c.main
		c.mu.Unlock()
		return s, nil
	}
	c.mu.Unlock()

	var sess *shell3.Session
	if id, ok := c.currentSession(); ok {
		if s, err := c.b.rt.Session(shell3.SessionOpts{
			ResumeID: id, PromptSuffix: c.brief,
		}); err == nil {
			sess = s
		}
	}
	if sess == nil {
		s, err := c.b.rt.Session(shell3.SessionOpts{PromptSuffix: c.brief})
		if err != nil {
			return nil, err
		}
		sess = s
	}

	c.mu.Lock()
	if c.main != nil { // lost a create race — keep the winner
		winner := c.main
		c.mu.Unlock()
		_ = sess.Close()
		return winner, nil
	}
	c.main = sess
	c.mu.Unlock()
	if err := c.setCurrentSession(sess.ID()); err != nil {
		// The in-memory marker would still agree with sess, but a restart
		// re-reads the STORE: a lost write here resumes a stale conversation
		// on the next boot, so it must at least be visible.
		c.b.log.Warn("current-session marker not persisted", "session", sess.ID(), "err", err)
	}
	return sess, nil
}

// setAnchor advances the conversation's latest chat message id.
func (c *conversation) setAnchor(msgID string) {
	if msgID == "" {
		return
	}
	c.mu.Lock()
	c.mainAnchor = msgID
	c.mu.Unlock()
}

// Session-index access shares c.mu with migration's index swap. Holding it
// through the store call prevents a writer that captured the old surface from
// resurrecting its marker after migration cleared it.
func (c *conversation) currentSession() (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.index.Current()
}

func (c *conversation) setCurrentSession(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.index.SetCurrent(id)
}

// startNextWork starts this room's next queued unit once the slot frees: user
// messages first, since the user outranks background work, then a steer catch-up
// for one that missed the last round boundary.
func (c *conversation) startNextWork(ctx context.Context) {
	if c.drainNextMessages(ctx) {
		return
	}
	if c.startSteerCatchup(ctx) {
		return
	}
}

// startSteerCatchup runs a posted turn over steering that landed after the
// previous turn's final round boundary. Its reply is delivered because the
// queued text is the user talking.
func (c *conversation) startSteerCatchup(ctx context.Context) bool {
	c.mu.Lock()
	if c.turnActive || c.main == nil || !c.main.HasQueuedSteer() {
		c.mu.Unlock()
		return false
	}
	sess := c.main
	turnCtx, cancel, ok := c.takeSlotLocked(ctx)
	if !ok {
		c.mu.Unlock()
		return false
	}
	anchor := c.takeSteerAnchorLocked()
	c.mu.Unlock()
	go c.runPostedQueuedTurn(ctx, turnCtx, cancel, sess, anchor)
	return true
}

// takeSteerAnchorLocked consumes the pending steer anchor, falling back to
// the conversation anchor. Caller holds c.mu.
func (c *conversation) takeSteerAnchorLocked() string {
	anchor := c.steerAnchor
	c.steerAnchor = ""
	if anchor == "" {
		anchor = c.mainAnchor
	}
	return anchor
}

// drainNextMessages runs the WHOLE backlog as one batch turn — it is all the
// same conversation. False when the queue is empty or a turn is running.
func (c *conversation) drainNextMessages(ctx context.Context) bool {
	c.mu.Lock()
	if c.turnActive || len(c.pendingMessages) == 0 {
		c.mu.Unlock()
		return false
	}
	turnCtx, cancel, ok := c.takeSlotLocked(ctx)
	if !ok {
		c.mu.Unlock()
		return false // capped: another room's turn end will come back for this
	}
	batch := c.pendingMessages
	c.pendingMessages = nil
	c.mu.Unlock()
	go c.runUserTurn(ctx, turnCtx, cancel, batch)
	return true
}

// releaseSlot clears the turn slot and cancels its ctx, which is harmless
// once the model call has finished.
func (c *conversation) releaseSlot(cancel context.CancelFunc) {
	c.mu.Lock()
	c.cancelTurn = nil
	c.turnActive = false
	c.mu.Unlock()
	c.b.freeTurn()
	cancel()
}

// markCurrent re-persists sess's marker at the end of every turn. It covers
// the divergence compaction causes: compactInto rolls the live session onto a
// NEW store row mid-conversation, and nothing on that side of the layering can
// update telegram's marker (chat and shell3 must never import telegram).
// Re-marking is idempotent and catches the id roll, resume, and any future
// id-rolling change with one mechanism rather than a hook per cause.
//
// Must run while the slot is still held, BEFORE releaseSlot: /new refuses
// while a turn is active, so this write can never resurrect a marker /new
// just cleared.
func (c *conversation) markCurrent(sess *shell3.Session) {
	if sess == nil {
		return
	}
	id := sess.ID()
	if id == "" {
		return
	}
	if err := c.setCurrentSession(id); err != nil {
		c.b.log.Warn("current-session marker not persisted", "session", id, "err", err)
	}
}

// runPostedQueuedTurn drains the inbox and DELIVERS the reply — the queued
// input includes the user speaking.
func (c *conversation) runPostedQueuedTurn(ctx, turnCtx context.Context, cancel context.CancelFunc, sess *shell3.Session, anchor string) {
	stopTyping := c.keepTyping(ctx)
	reply, _ := c.drainTurnProgress(ctx, sess.RunQueued(turnCtx))
	stopTyping()
	c.finishPostedTurn(ctx, sess, anchor, reply, cancel)
}

// takeSlotLocked marks a turn active here and returns its ctx and cancel.
// Caller holds c.mu.
//
// ok is false when the global cap is full, and the caller must queue instead.
// A room queued by the cap has no trigger of
// its own — startNextWorkAll, run when any room frees a slot, drains it.
func (c *conversation) takeSlotLocked(ctx context.Context) (context.Context, context.CancelFunc, bool) {
	if !c.b.claimTurn() {
		return nil, nil, false
	}
	turnCtx, cancel := context.WithCancel(ctx)
	c.cancelTurn = cancel
	c.turnActive = true
	return turnCtx, cancel, true
}

// keepTyping refreshes the "typing…" action every 4s — it expires after
// ~5 — until stop is called.
func (c *conversation) keepTyping(ctx context.Context) (stop func()) {
	tctx, cancel := context.WithCancel(ctx)
	chatID := c.chatIDValue()
	go func() {
		_ = c.b.client.Typing(tctx, chatID)
		t := time.NewTicker(4 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-tctx.Done():
				return
			case <-t.C:
				_ = c.b.client.Typing(tctx, chatID)
			}
		}
	}()
	return cancel
}

// conv returns the conversation for chatID, creating it on first sight.
// handleMsg has already decided the sender may drive the agent, so creation
// carries no authorization meaning of its own.
func (b *Bot) conv(chatID int64) *conversation {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.convs == nil {
		b.convs = make(map[int64]*conversation)
	}
	if c, ok := b.convs[chatID]; ok {
		return c
	}
	c := &conversation{
		b:      b,
		chatID: chatID,
		index:  b.sessions.forSurface(roomSurface(b.sessions.hostSurface(), chatID)),
	}
	b.convs[chatID] = c
	return c
}

// peekConv returns an existing conversation without creating one: routing
// peeks before it decides, and a message not for the bot must leave no trace.
func (b *Bot) peekConv(chatID int64) *conversation {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.convs[chatID]
}

// allConvs snapshots the room registry.
func (b *Bot) allConvs() []*conversation {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]*conversation, 0, len(b.convs))
	for _, c := range b.convs {
		out = append(out, c)
	}
	return out
}

// convFor finds the live room whose conversation is that store session, for
// routing host-tool output back to where it was requested.
func (b *Bot) convFor(sessionID string) *conversation {
	if sessionID == "" {
		return nil
	}
	for _, c := range b.allConvs() {
		if s := c.session(); s != nil && s.ID() == sessionID {
			return c
		}
	}
	return nil
}

// homeConv is the fallback room for host alerts and ownerless file sends.
func (b *Bot) homeConv() *conversation {
	b.mu.Lock()
	id := b.homeChat
	b.mu.Unlock()
	return b.conv(id)
}

// session returns this room's live session handle without creating one.
func (c *conversation) session() *shell3.Session {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.main
}

func (c *conversation) chatIDValue() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.chatID
}

// claimTurn takes one of the global turn slots, or reports false when the cap
// is full. Without the cap, N rooms speaking at once
// fan out N concurrent agents against one provider account.
func (b *Bot) claimTurn() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.activeTurns >= b.maxTurns {
		return false
	}
	b.activeTurns++
	return true
}

// freeTurn returns a global turn slot.
func (b *Bot) freeTurn() {
	b.mu.Lock()
	if b.activeTurns > 0 {
		b.activeTurns--
	}
	b.mu.Unlock()
}

// startNextWorkAll runs end-of-turn housekeeping across EVERY room, starting
// with the room that just finished.
//
// The sweep is not tidiness: with a global cap, a message can be queued
// because the CAP was full rather than because its own room was busy, and
// that room has no event of its own coming to resume it. Freeing a slot is the
// only signal it will ever get.
func (b *Bot) startNextWorkAll(ctx context.Context, first *conversation) {
	if first != nil {
		first.startNextWork(ctx)
	}
	for _, c := range b.allConvs() {
		if c == first {
			continue
		}
		c.startNextWork(ctx)
	}
}

// migrateRoom carries a conversation across a chat-id change — what Telegram
// does when a group is converted to a supergroup.
//
// The room's session, anchors and queues are keyed by chat id, and so is its
// current-session marker; without this the old room would sit idle under an id nobody
// can post to again, and the new id would start an empty conversation. The
// user experiences a rename, so the conversation should survive one.
//
// Idempotent and safe if the new room was already touched: an existing
// conversation at the destination wins, since it may already hold a turn.
func (b *Bot) migrateRoom(from, to int64) {
	if from == to || to == 0 {
		return
	}
	b.mu.Lock()
	old, ok := b.convs[from]
	b.mu.Unlock()
	if !ok {
		return // nothing to carry over
	}
	// Conversation state precedes the registry in the global lock order.
	// Re-check both map entries after acquiring them together: another update
	// may have enrolled or migrated the destination in between.
	old.mu.Lock()
	b.mu.Lock()
	if b.convs[from] != old {
		b.mu.Unlock()
		old.mu.Unlock()
		return
	}
	if _, taken := b.convs[to]; taken {
		b.mu.Unlock()
		old.mu.Unlock()
		b.log.Warn("chat migrated onto a room that already exists; keeping the newer one",
			"from", from, "to", to)
		return
	}
	delete(b.convs, from)
	old.chatID = to
	old.isGroup = true // a supergroup is a group whatever the old chat type said
	old.index = b.sessions.forSurface(roomSurface(b.sessions.hostSurface(), to))
	newIndex := old.index
	b.convs[to] = old
	b.mu.Unlock()

	// Re-persist under the new surface so a restart resumes the conversation,
	// and clear the old marker so the janitor is not left pointing at a room
	// that no longer exists. Keep c.mu through both store writes: /new and the
	// end-of-turn marker update use the same lock, so neither can be reordered
	// behind migration and resurrect a stale surface.
	if sess := old.main; sess != nil {
		if err := newIndex.SetCurrent(sess.ID()); err != nil {
			b.log.Warn("migrated room marker not persisted", "chat", to, "err", err)
		}
	}
	if st := b.sessions.currentStore(); st != nil {
		if err := st.SetCurrentSession(roomSurface(b.sessions.hostSurface(), from), ""); err != nil {
			b.log.Warn("old room marker not cleared", "chat", from, "err", err)
		}
	}
	old.mu.Unlock()
	// The new chat has its own title and description; the cached metadata
	// belongs to the id that no longer exists.
	b.metaMu.Lock()
	delete(b.chatMetaCache, from)
	b.metaMu.Unlock()
	b.log.Info("chat migrated to a supergroup; conversation carried over", "from", from, "to", to)
}
