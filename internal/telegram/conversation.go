//go:build unix

package telegram

// conversation is ONE chat's conversation: the long-lived main session every
// message in that chat continues, plus the turn slot, anchors and queues that
// belong to it. shell3 holds one per Telegram chat, so two rooms never share a
// context window, an anchor, or a turn — the state that used to live on Bot as
// single fields lives here as one struct per room.
//
// Locking: c.mu guards this conversation's own state (slot, session, queues);
// b.mu guards the Bot-wide registry and wiring. Take b.mu inside c.mu, never
// the other way round.

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/weatherjean/shell3/internal/shell3"
	"github.com/weatherjean/shell3/internal/strutil"
)

type conversation struct {
	b       *Bot
	chatID  int64
	isGroup bool
	// index persists WHICH store session is this room's conversation, under
	// the room's own surface key ("telegram:<chatid>"), so a restart resumes
	// every room and not just one.
	index *ThreadIndex

	mu sync.Mutex // guards this room's turn slot, session, anchors and queues
	// main is THIS ROOM's conversation: one long-lived session every message
	// in this chat continues, replaced only by /new here.
	main *shell3.Session
	// mainAnchor is this room's latest chat message id — replies and agent
	// mail thread onto it.
	mainAnchor string
	// steerAnchor is the newest STEERED user message id, held separately so
	// the running turn's own reply can't clobber it.
	steerAnchor string
	// lastAgentMail is the previous ✉️ wake-turn post in this room, so an
	// identical repeat is dropped host-side. Cleared by /new.
	lastAgentMail string

	cancelTurn  context.CancelFunc // non-nil while a turn runs in this room
	turnActive  bool
	turnQuiet   bool
	wakePending bool
	mailQueue   []inMail
	burst       []inMail
	burstTimer  *time.Timer
	// sentIDs is a bounded ring of message ids the bot posted in this room.
	// A group message replying to one of them is addressed to the bot (see
	// trigger.go) — the only way, short of an @mention, to tell "answer me"
	// from ordinary room chatter.
	sentIDs []string
}

// sentIDsCap bounds the recorded-sent ring. Big enough that a reply to
// anything still on screen resolves, small enough to stay free.
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
		c.dispatchMail(ctx, batch)
	}
}

// dispatchMail routes a message batch: mid-turn TEXT steers the running turn
// (injected at the next round boundary — "stop, wrong file" redirects work in
// flight instead of waiting behind it; a steer landing after the final
// boundary is answered by startNextWork's catch-up turn), media queues, and
// an idle bot runs the batch as one user turn.
func (c *conversation) dispatchMail(ctx context.Context, batch []inMail) {
	if len(batch) == 0 {
		return
	}
	hasMedia := false
	for _, mail := range batch {
		hasMedia = hasMedia || len(mail.saved) > 0
	}
	c.mu.Lock()
	if c.turnActive {
		if !hasMedia && !c.turnQuiet && c.main != nil {
			sess := c.main
			c.mainAnchor = batch[len(batch)-1].m.ID
			c.steerAnchor = c.mainAnchor
			c.mu.Unlock()
			parts := make([]string, 0, len(batch))
			for _, mail := range batch {
				parts = append(parts, withReplyContext(mail.text, mail.m.ReplyTo))
			}
			sess.Interject(strings.Join(parts, "\n\n"))
			return
		}
		c.mailQueue = append(c.mailQueue, batch...)
		c.mu.Unlock()
		return
	}
	// Take the turn slot before resolving the session so a wake landing during
	// session creation queues instead of racing onto the slot.
	turnCtx, cancel, ok := c.takeSlotLocked(ctx)
	if !ok {
		// The global cap is full: queue rather than refuse. Sending always
		// succeeds — the backlog drains as one batch turn when a slot frees.
		c.mailQueue = append(c.mailQueue, batch...)
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()

	go c.runUserTurn(ctx, turnCtx, cancel, batch)
}

// runUserTurn runs one user-initiated turn over batch (one message, or the
// queued backlog). The turn slot is held on entry; this goroutine owns
// delivery, slot release, and starting the next queued work.
func (c *conversation) runUserTurn(ctx, turnCtx context.Context, cancel context.CancelFunc, batch []inMail) {
	last := batch[len(batch)-1]
	sess, err := c.mainSession()
	if err != nil {
		c.releaseSlot(cancel)
		c.sendReply(ctx, "⚠️ could not start a session: "+err.Error())
		// Work queued against the held slot during the failed creation would
		// otherwise wait for the next event — drain it now.
		c.startNextWork(ctx)
		return
	}
	c.setAnchor(last.m.ID) // the reply anchors at the newest message

	composeText := func() string {
		parts := make([]string, 0, len(batch))
		for _, mail := range batch {
			out := mail.text
			if injected := attachmentNote(mail.saved, c.b.hasTool(sess, "read_media")); injected != "" {
				if out != "" {
					out += "\n\n" + injected
				} else {
					out = injected
				}
			}
			parts = append(parts, withReplyContext(out, mail.m.ReplyTo))
		}
		return strings.Join(parts, "\n\n")
	}

	stopTyping := c.keepTyping(ctx)
	finalText := composeText()
	reply, _ := c.drainTurnProgress(ctx, sess.Send(turnCtx, finalText))
	stopTyping()
	// The NO_REPLY sentinel belongs to wake turns; a model over-generalizing
	// it into a user turn must not post the literal string as its answer.
	if strutil.IsNoReply(reply) {
		reply = ""
	}
	// A corrupt reply (raw tool-call markup — the provider failed to parse
	// its own template) is replaced by a short notice: the user learns the
	// turn misfired instead of staring at protocol garbage.
	if containsToolMarkup(reply) {
		reply = malformedReplyNotice
	}
	// A user-initiated turn always answers ("" renders as "(no output)").
	// The turn slot is held through delivery: while it is held no other
	// goroutine can start a turn (a mid-delivery user message queues, a Wake
	// queues, startNextWork waits).
	c.postReply(ctx, sess, last.m.ID, reply)
	c.markCurrent(sess)
	c.releaseSlot(cancel)
	c.applyPendingReload(ctx) // self-evolution: agent edited config + called reload this turn (needs a free slot)
	c.b.startNextWorkAll(ctx, c)
}

// mainSession returns THE conversation's session, creating it on first use:
// the persisted current-marker resumes the previous run's session (so a
// restart continues the same conversation); with no marker — or a marker the
// janitor swept — a fresh session starts and is recorded as current.
func (c *conversation) mainSession() (*shell3.Session, error) {
	c.mu.Lock()
	if c.main != nil {
		s := c.main
		c.mu.Unlock()
		return s, nil
	}
	c.mu.Unlock()

	var sess *shell3.Session
	if id, ok := c.index.Current(); ok {
		if s, err := c.b.rt.Session(shell3.SessionOpts{
			ResumeID: id, WorkDir: c.b.workDir, PromptSuffix: c.brief,
		}); err == nil {
			sess = s
		}
	}
	if sess == nil {
		s, err := c.b.rt.Session(shell3.SessionOpts{WorkDir: c.b.workDir, PromptSuffix: c.brief})
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
	if err := c.index.SetCurrent(sess.ID()); err != nil {
		// A failed write here used to vanish silently: mainSession's own
		// in-memory marker (c.index.m) still agreed with sess, but a restart
		// re-reads the marker from the STORE, not from memory — so a lost
		// write here would resume a stale conversation on the next boot,
		// with no trace of why. Now it is at least visible.
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

// afterTurn runs the end-of-turn housekeeping for a wake turn: re-mark the
// current-session marker (see markCurrent), release the slot, then start the
// next queued work (user mail first, then wakes). The main session never
// retires — it IS the conversation.
func (c *conversation) afterTurn(ctx context.Context, sess *shell3.Session, cancel context.CancelFunc) {
	c.markCurrent(sess)
	c.releaseSlot(cancel)
	c.b.startNextWorkAll(ctx, c)
}

// startNextWork starts the next queued unit in THIS room once its slot is
// free: queued user mail first (the user outranks background mail), then a
// steer catch-up (a steer that missed its turn's last round boundary still
// gets an answered turn), then pending wakes.
func (c *conversation) startNextWork(ctx context.Context) {
	if c.drainNextMail(ctx) {
		return
	}
	if c.startSteerCatchup(ctx) {
		return
	}
	c.startNextWake(ctx)
}

// startSteerCatchup runs a POSTED turn over user steering that landed in the
// session inbox after the previous turn's final round boundary. Unlike a wake
// (quiet) turn, its reply is delivered — the queued text is the user talking.
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

// takeSteerAnchorLocked consumes the pending steer anchor — the newest
// steered user message id — falling back to the conversation anchor when no
// steer recorded one. Caller must hold c.mu.
func (c *conversation) takeSteerAnchorLocked() string {
	anchor := c.steerAnchor
	c.steerAnchor = ""
	if anchor == "" {
		anchor = c.mainAnchor
	}
	return anchor
}

// drainNextMail drains the WHOLE queued-mail backlog as one batch turn — it
// is all the same conversation. Returns false when the queue is empty or a
// turn is already running.
func (c *conversation) drainNextMail(ctx context.Context) bool {
	c.mu.Lock()
	if c.turnActive || len(c.mailQueue) == 0 {
		c.mu.Unlock()
		return false
	}
	turnCtx, cancel, ok := c.takeSlotLocked(ctx)
	if !ok {
		c.mu.Unlock()
		return false // capped: another room's turn end will come back for this
	}
	batch := c.mailQueue
	c.mailQueue = nil
	c.mu.Unlock()
	go c.runUserTurn(ctx, turnCtx, cancel, batch)
	return true
}

// releaseSlot clears the turn slot (turnActive/cancelTurn) and cancels the
// turn ctx. Cancelling a turn whose model call already finished is harmless.
func (c *conversation) releaseSlot(cancel context.CancelFunc) {
	c.mu.Lock()
	c.cancelTurn = nil
	c.turnActive = false
	c.mu.Unlock()
	c.b.freeTurn()
	cancel()
}

// markCurrent re-persists the current-session marker for sess. Called at the
// end of every turn, just before releaseSlot — covers the marker/session
// divergence host-managed compaction causes: internal/chat's compactInto
// rolls the live session onto a NEW runs-store row mid-conversation whenever
// auto-compaction fires, and nothing on that side of the layering can update
// telegram's marker directly (internal/chat and internal/shell3 must never
// import internal/telegram). Re-marking here after every turn is idempotent
// (SetCurrent is last-write-wins) and catches compaction's id roll — plus
// resume, plus any future id-rolling change — with one mechanism instead of
// a bespoke hook per cause.
//
// Must run while the turn slot is still held (turnActive true), i.e. BEFORE
// releaseSlot: /new (commands.go's handleNewCommand) refuses outright while a
// turn is active, so calling this first guarantees /new cannot clear the
// marker until after this write has already landed — a write here can never
// resurrect a marker /new just cleared.
func (c *conversation) markCurrent(sess *shell3.Session) {
	if sess == nil {
		return
	}
	id := sess.ID()
	if id == "" {
		return
	}
	if err := c.index.SetCurrent(id); err != nil {
		c.b.log.Warn("current-session marker not persisted", "session", id, "err", err)
	}
}

// runPostedQueuedTurn is startSteerCatchup's turn body: drain the session
// inbox and DELIVER the reply (the queued input includes the user speaking).
func (c *conversation) runPostedQueuedTurn(ctx, turnCtx context.Context, cancel context.CancelFunc, sess *shell3.Session, anchor string) {
	stopTyping := c.keepTyping(ctx)
	reply, _ := c.drainTurnProgress(ctx, sess.RunQueued(turnCtx))
	stopTyping()
	if strutil.IsNoReply(reply) {
		reply = ""
	}
	if containsToolMarkup(reply) {
		reply = malformedReplyNotice
	}
	c.postReply(ctx, sess, anchor, reply)
	c.markCurrent(sess)
	c.releaseSlot(cancel)
	c.applyPendingReload(ctx)
	c.b.startNextWorkAll(ctx, c)
}

// startNextWake runs a pending wake turn on the main session if the slot is
// free. Called after every turn ends. Queued user steering upgrades it to a
// posted turn (see dispatchWake).
func (c *conversation) startNextWake(ctx context.Context) bool {
	c.mu.Lock()
	if c.turnActive || !c.wakePending || c.main == nil {
		c.mu.Unlock()
		return false
	}
	c.wakePending = false
	sess := c.main
	if !sess.HasQueuedInput() {
		c.mu.Unlock()
		return false // already drained by the turn that just ran
	}
	if sess.HasQueuedSteer() {
		turnCtx, cancel, ok := c.takeSlotLocked(ctx)
		if !ok {
			c.wakePending = true // capped: try again when a slot frees
			c.mu.Unlock()
			return false
		}
		anchor := c.takeSteerAnchorLocked()
		c.mu.Unlock()
		go c.runPostedQueuedTurn(ctx, turnCtx, cancel, sess, anchor)
		return true
	}
	turnCtx, cancel, ok := c.takeSlotLocked(ctx)
	if !ok {
		c.wakePending = true
		c.mu.Unlock()
		return false
	}
	c.turnQuiet = true
	c.mu.Unlock()
	go func() {
		c.runWakeTurn(ctx, turnCtx, sess)
		c.afterTurn(ctx, sess, cancel)
	}()
	return true
}

// takeSlotLocked marks a turn active in THIS room and returns its ctx +
// cancel. Caller holds c.mu.
//
// ok is false when the global cap is full or a reload is swapping the config
// underneath: the caller must queue its work instead of running it. A room
// whose message queues because the CAP was hit has no waker of its own —
// startNextWorkAll, run whenever any room frees a slot, is what eventually
// drains it.
func (c *conversation) takeSlotLocked(ctx context.Context) (context.Context, context.CancelFunc, bool) {
	if !c.b.claimTurn() {
		return nil, nil, false
	}
	turnCtx, cancel := context.WithCancel(ctx)
	c.cancelTurn = cancel
	c.turnActive = true
	c.turnQuiet = false
	return turnCtx, cancel, true
}

// runWakeTurn runs one queued mail turn on sess. Its reply is the agent
// speaking to the user and posts ✉️-prefixed — ONE channel, so the agent can
// never send the same answer twice through two exits. NO_REPLY (or an empty
// final segment — no narration fallback here: a wake turn that ends on a tool
// call said nothing) keeps the turn silent. Under /quiet the post arrives
// without a ping. The turn slot is held on entry and stays held on return —
// the caller's afterTurn retires the session and releases the slot, in that
// order, so the slot spans the turn + retirement (see retireAndRelease).
// /stop can cancel it via the shared turn slot. Only ordinary threaded
// sessions (a subagent/bash_bg completion) and fresh StartFreshTurn sessions
// run wake turns; the pinned cron session is never woken.
func (c *conversation) runWakeTurn(ctx, turnCtx context.Context, sess *shell3.Session) {
	reply, errText := c.drainTurn(sess.RunQueued(turnCtx), false)
	// A wake turn with nothing to say stays silent even when its provider
	// hiccuped — the turn error is in the transcript (the dash), and posting it
	// would make every flaky tick ring the chat. Errors ride along only when
	// the agent was going to speak anyway.
	if strutil.IsNoReply(reply) {
		return
	}
	// Corrupt output (raw tool-call markup) never posts as an update — the
	// transcript keeps it for diagnosis; the chat is spared the garbage.
	if containsToolMarkup(reply) {
		return
	}
	if errText != "" {
		reply += "\n" + errText
	}
	// A mail identical to the previous one is dropped: a repeat carries no
	// information (a changed situation produces changed text), and a model
	// stuck in a narration loop must not fill the chat with copies.
	c.mu.Lock()
	if reply == c.lastAgentMail {
		c.mu.Unlock()
		return
	}
	c.lastAgentMail = reply
	c.mu.Unlock()
	// Agent mail is ALWAYS silent and never a Telegram reply: it is mail, not
	// a page — a background thought must not ring a sleeping phone (⚠️
	// failure posts are the ones that ring), and a quote header on every ✉️
	// reads as noise in the one conversation.
	c.postReply(ctx, sess, "", "✉️ "+reply, SendOpt{Silent: true})
}

// keepTyping shows the "typing…" chat action and refreshes it every 4s (the
// action expires after ~5s) until the returned stop is called.
func (c *conversation) keepTyping(ctx context.Context) (stop func()) {
	tctx, cancel := context.WithCancel(ctx)
	go func() {
		_ = c.b.client.Typing(tctx, c.chatID)
		t := time.NewTicker(4 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-tctx.Done():
				return
			case <-t.C:
				_ = c.b.client.Typing(tctx, c.chatID)
			}
		}
	}()
	return cancel
}

// conv returns the conversation for chatID, creating it on first sight. A
// room exists because an allowlisted sender addressed the bot there — the
// caller has already made that decision (handleMsg), so creation here carries
// no authorization meaning of its own.
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
		index:  b.threads.forSurface(roomSurface(b.threads.hostSurface(), chatID)),
	}
	b.convs[chatID] = c
	return c
}

// peekConv returns the conversation for chatID if the room already exists,
// without creating one. Routing peeks before it decides: a message that turns
// out not to be for the bot must leave no trace.
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

// convFor finds the LIVE room whose conversation is the given store session,
// or nil. Used to route a completion back to the room that spawned it.
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

// homeConv is the room cron results and ownerless completions land in.
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

// busy reports whether this room is mid-turn.
func (c *conversation) busy() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.turnActive
}

// wake runs this room's queued mail turn (a subagent or bash_bg finished, or
// a completion was mailed here). A wake that lands while the room is mid-turn
// — or while the global cap is full — is marked pending and drained by the
// next turn end.
func (c *conversation) wake(ctx context.Context) {
	c.mu.Lock()
	sess := c.main
	if sess == nil {
		c.mu.Unlock()
		return
	}
	if c.turnActive {
		c.wakePending = true
		c.mu.Unlock()
		return
	}
	// User steering in the inbox upgrades the wake to a POSTED turn: the user
	// spoke (perhaps a steer that raced the previous turn's end), so the reply
	// must be delivered — RunQueued drains notices alongside it.
	if sess.HasQueuedSteer() {
		turnCtx, cancel, ok := c.takeSlotLocked(ctx)
		if !ok {
			c.wakePending = true
			c.mu.Unlock()
			return
		}
		anchor := c.takeSteerAnchorLocked()
		c.mu.Unlock()
		go c.runPostedQueuedTurn(ctx, turnCtx, cancel, sess, anchor)
		return
	}
	turnCtx, cancel, ok := c.takeSlotLocked(ctx)
	if !ok {
		c.wakePending = true
		c.mu.Unlock()
		return
	}
	c.turnQuiet = true
	c.mu.Unlock()
	go func() {
		c.runWakeTurn(ctx, turnCtx, sess)
		c.afterTurn(ctx, sess, cancel)
	}()
}

// claimTurn takes one of the global turn slots, or reports false when the cap
// is full or a reload is in flight. Without the cap, N rooms speaking at once
// fan out N concurrent agents against one provider account.
func (b *Bot) claimTurn() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.reloading || b.activeTurns >= b.maxTurns {
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

// anyBusy reports whether any room is mid-turn — what /reload refuses on.
func (b *Bot) anyBusy() bool {
	for _, c := range b.allConvs() {
		if c.busy() {
			return true
		}
	}
	return false
}

// startNextWorkAll runs end-of-turn housekeeping across EVERY room, starting
// with the room that just finished.
//
// The sweep is not tidiness: with a global cap, a message can be queued
// because the CAP was full rather than because its own room was busy, and
// that room has no event of its own coming to wake it. Freeing a slot is the
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
// thread marker; without this the old room would sit idle under an id nobody
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
	if !ok {
		b.mu.Unlock()
		return // nothing to carry over
	}
	if _, taken := b.convs[to]; taken {
		b.mu.Unlock()
		b.log.Warn("chat migrated onto a room that already exists; keeping the newer one",
			"from", from, "to", to)
		return
	}
	delete(b.convs, from)
	old.chatID = to
	old.isGroup = true // a supergroup is a group whatever the old chat type said
	old.index = b.threads.forSurface(roomSurface(b.threads.hostSurface(), to))
	b.convs[to] = old
	b.mu.Unlock()

	// Re-persist under the new surface so a restart resumes the conversation,
	// and clear the old marker so the janitor is not left pointing at a room
	// that no longer exists.
	if sess := old.session(); sess != nil {
		if err := old.index.SetCurrent(sess.ID()); err != nil {
			b.log.Warn("migrated room marker not persisted", "chat", to, "err", err)
		}
	}
	if st := b.threads.currentStore(); st != nil {
		if err := st.SetCurrentSession(roomSurface(b.threads.hostSurface(), from), ""); err != nil {
			b.log.Warn("old room marker not cleared", "chat", from, "err", err)
		}
	}
	// The new chat has its own title and description; the cached metadata
	// belongs to the id that no longer exists.
	b.metaMu.Lock()
	delete(b.chatMetaCache, from)
	b.metaMu.Unlock()
	b.log.Info("chat migrated to a supergroup; conversation carried over", "from", from, "to", to)
}
