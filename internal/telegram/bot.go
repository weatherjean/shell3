//go:build unix

package telegram

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/shell3"
	"github.com/weatherjean/shell3/internal/strutil"
)

// Bot maintains one persistent conversation per Telegram chat. Authorization
// is per sender; rooms run concurrently under a global turn cap.
type Bot struct {
	client tgClient
	rt     *shell3.Runtime
	// homeChat takes cron results and ownerless completions. NOT an access
	// rule — rooms are authorized by who speaks in them.
	homeChat int64
	// allow decides WHO may drive the agent, whatever the chat. Never nil
	// after NewBot — a nil allowlist denies everyone.
	allow *senderAllowlist

	workDir string // resolves relative paths for send_media_telegram
	// configDir is what send_media_telegram refuses to send from, media dir
	// excepted — by path AND inode, so no symlink or hardlink launders a
	// credential out. "" disables only those two checks.
	configDir string
	// threads is the thread index every room derives its own surface from.
	threads *ThreadIndex

	mu sync.Mutex // guards the room registry and the mutable wiring below
	// convs holds one conversation per chat id, created the first time an
	// allowlisted sender addresses the bot there.
	convs map[int64]*conversation
	// activeTurns counts rooms holding a slot, bounded by maxTurns — without
	// a global cap N rooms fan out N concurrent agents.
	activeTurns int
	maxTurns    int
	debounce    time.Duration

	// answerAllGroups disables the mention/reply trigger for allowlisted
	// senders. Authorization still runs first.
	answerAllGroups bool
	reload          func() error
	// metaMu guards the chat metadata cache, separate from b.mu because a miss
	// makes a network call and holding the registry lock would stall routing.
	metaMu        sync.Mutex
	chatMetaCache map[int64]chatMeta
	// metaInflight keeps at most one getChat per room in flight.
	metaInflight map[int64]bool

	// botUser is the bot's own @username, for @mention matching.
	// botUserKnown separates "not looked up" from "looked up, no answer".
	botUser      string
	botUserKnown bool
	// botUserWarned throttles that warning to once per outage — username()
	// runs on every inbound message.
	botUserWarned bool

	// log records host-side faults that never reach the user, such as a failed
	// marker write. Defaults to Noop; SetLogger opts in.
	log applog.Logger
}

// defaultMaxTurns bounds concurrent turns across all rooms.
const defaultMaxTurns = 4

// NewBot wires a Bot over the runtime. homeChat receives host-owned inbox
// alerts; threads is the process-wide index each room derives its own surface
// from, kept across /reload.
func NewBot(client tgClient, rt *shell3.Runtime, homeChat int64, threads *ThreadIndex) *Bot {
	// Default allowlist: the home chat's owner.
	allow, _ := newSenderAllowlist(homeChat, nil)
	return &Bot{
		client:   client,
		rt:       rt,
		homeChat: homeChat,
		threads:  threads,
		convs:    make(map[int64]*conversation),
		maxTurns: defaultMaxTurns,
		allow:    allow,
		log:      applog.Noop{},
	}
}

// SetMaxConcurrentTurns bounds rooms holding a slot; non-positive keeps the
// default.
// SetConvoLog records every message in and out to w, as JSONL. Call it before
// Run: it swaps the transport for a logging wrapper, so anything sent through
// the client this Bot was built with is covered — host command replies and
// inbox alerts included, which is traffic no other record holds.
//
// The "/" command registration is sent through the concrete API client and is
// not in the log. Lifecycle and inbox notices use Bot methods and are logged
// when this wrapper is installed.
func (b *Bot) SetConvoLog(w io.Writer) {
	if w == nil {
		return
	}
	b.mu.Lock()
	b.client = newConvoLogClient(b.client, w)
	b.mu.Unlock()
}

func (b *Bot) SetMaxConcurrentTurns(n int) {
	if n <= 0 {
		return
	}
	b.mu.Lock()
	b.maxTurns = n
	b.mu.Unlock()
}

// SetLogger wires the app log for faults that never reach the user.
func (b *Bot) SetLogger(log applog.Logger) {
	if log != nil {
		b.log = log
	}
}

// SetAllowFrom replaces the sender allowlist. An empty list keeps the
// default: the chat owner alone.
func (b *Bot) SetAllowFrom(ids []string) error {
	allow, err := newSenderAllowlist(b.homeChat, ids)
	if err != nil {
		return err
	}
	b.mu.Lock()
	b.allow = allow
	b.mu.Unlock()
	return nil
}

// SetAnswerAllGroupMessages controls whether an allowlisted sender must
// address the bot in a group. It is replaced on /reload with the current
// telegram.group_messages setting.
func (b *Bot) SetAnswerAllGroupMessages(on bool) {
	b.mu.Lock()
	b.answerAllGroups = on
	b.mu.Unlock()
}

// SetReload installs the host-owned, between-turn configuration reload.
func (b *Bot) SetReload(fn func() error) {
	b.mu.Lock()
	b.reload = fn
	b.mu.Unlock()
}

func (b *Bot) answersAllGroupMessages() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.answerAllGroups
}

// allowlist reads the sender allowlist under b.mu: /reload can replace it
// from a turn goroutine while the update loop authorizes a message.
func (b *Bot) allowlist() *senderAllowlist {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.allow
}

// SetWorkDir sets the directory used to resolve relative paths passed to
// send_media_telegram (the agent's session workdir).
func (b *Bot) SetWorkDir(dir string) { b.workDir = dir }

// SetConfigDir sets the directory send_media_telegram refuses to send from.
// Unset skips the path and inode checks, so every front-end must supply it.
func (b *Bot) SetConfigDir(dir string) { b.configDir = dir }

// Run consumes inbound messages and the wake bus until ctx is cancelled.
func (b *Bot) Run(ctx context.Context) {
	go b.consumeWakes(ctx)
	// Subscribe ONCE. Calling Updates per iteration only worked because the
	// clients happened to hand back a stored field; a client that builds its
	// stream on demand (the --convo-log wrapper did) then gets a fresh
	// subscriber every loop, each competing for the same upstream, and the
	// abandoned ones silently eat messages.
	updates := b.client.Updates(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case m, ok := <-updates:
			if !ok {
				return
			}
			b.handleMsg(ctx, m)
		}
	}
}

// inMail is one received user message, attachments already saved, waiting for
// (or entering) its turn.
type inMail struct {
	m     Msg
	text  string
	saved []savedFile
}

// handleMsg routes one inbound message on the single update loop, so handling
// is serialized and only the turn runs on its own goroutine. Two gates, in
// this order:
//
//  1. The SENDER must be allowlisted — checked before the room is resolved, so
//     a stranger never creates a conversation, saves an attachment or costs a
//     token, and before the command branch, since Telegram delivers /commands
//     from every group member and a turn-path gate would still let a stranger
//     /stop a turn or /new the conversation away.
//  2. In a GROUP the message must be ADDRESSED to the bot unless
//     telegram.group_messages is all. A no-op in a private chat.
func (b *Bot) handleMsg(ctx context.Context, m Msg) {
	// A group becoming a supergroup changes its chat id, announced once as a
	// service message with no sender — so this runs BEFORE the sender gate,
	// there being nobody to authorize. Missing it strands the room's
	// conversation under an id that never speaks again.
	if m.MigratedTo != 0 {
		b.migrateRoom(m.ChatID, m.MigratedTo)
		return
	}
	if !b.allowlist().allows(m.SenderID) {
		return // unauthorized sender: drop silently
	}
	// Peek, don't create: chatter that never reaches a turn must not leave a
	// phantom room in the registry, the inbox and every listing.
	c := b.peekConv(m.ChatID)
	if c != nil {
		c.setGroup(m.ChatType)
	}
	isGroup := m.ChatType != "" && m.ChatType != "private"

	if strings.HasPrefix(m.Text, "/") {
		verb, suffix, hasSuffix := strings.Cut(strings.Fields(m.Text)[0], "@")
		// "/stop@otherbot" belongs to a DIFFERENT bot; privacy mode off
		// delivers it anyway, and routing on the bare verb would let another
		// bot's users stop our turns.
		if botUser := b.username(ctx); hasSuffix && botUser != "" && !strings.EqualFold(suffix, botUser) {
			// No resolvable username: answer anyway. The sender is
			// allowlisted either way, and dropping would silently no-op
			// every "/stop@mybot" through a getMe outage.
			return
		}
		// An unknown verb in a group is almost always someone talking to
		// another bot; answering "unknown command" would spam the room. In a
		// DM it is a typo worth reporting.
		if isGroup && !b.answersAllGroupMessages() && !b.knowsCommand(verb) {
			return
		}
		cmdRoom := b.conv(m.ChatID)
		cmdRoom.setGroup(m.ChatType)  // the room may be brand new: this is its first sighting
		cmdRoom.handleCommand(ctx, m) // defined in commands.go
		return
	}
	if isGroup && !b.answersAllGroupMessages() && !roomAddressed(c, m, b.username(ctx)) {
		return // group chatter not aimed at the bot: drop before anything is saved
	}
	c = b.conv(m.ChatID)
	c.setGroup(m.ChatType)
	// Both gates have passed, so this message is for us: NOW fetch its
	// attachments. Downloading before this point would mean pulling every
	// stranger's photo out of every group the bot can see (privacy mode off
	// delivers them all) just to drop it here.
	text := strings.TrimSpace(m.Text)
	if m.FetchMedia != nil && len(m.Media) == 0 {
		m.Media = m.FetchMedia(ctx)
	}
	// Save attachments to the durable media dir — fast, local, no network.
	// attachmentNote's path-injection runs inside the turn goroutine, never
	// on this loop.
	saved := saveAttachments(m.Media)
	if len(saved) == 0 {
		if (len(m.Media) > 0 || m.HasMedia) && text == "" {
			c.sendReply(ctx, "⚠️ couldn't save that attachment.")
			return
		}
		if text == "" {
			return // nothing actionable
		}
	}

	mail := inMail{m: m, text: text, saved: saved}

	// Text rides a short debounce: Telegram splits a long message into
	// back-to-back updates, and the burst merges them into ONE turn. Media
	// flushes the burst and dispatches immediately — its attachment note
	// needs the session resolved on a turn goroutine.
	if len(saved) == 0 {
		c.mu.Lock()
		c.burst = append(c.burst, mail)
		if c.burstTimer == nil {
			window := b.debounce
			if window <= 0 {
				window = burstWindow
			}
			c.burstTimer = time.AfterFunc(window, func() { c.flushBurst(ctx) })
		}
		c.mu.Unlock()
		return
	}
	c.flushBurst(ctx)
	c.dispatchMail(ctx, []inMail{mail})
}

// roomAddressed answers the trigger question for a room that may not exist
// yet: with no conversation nothing has been posted to reply to, so only an
// @mention can open one.
func roomAddressed(c *conversation, m Msg, botUser string) bool {
	if c == nil {
		return mentions(m.Text, botUser)
	}
	return c.addressed(m, botUser)
}

// knowsCommand reports whether verb is a built-in or kit-declared command.
func (b *Bot) knowsCommand(verb string) bool {
	name := strings.TrimPrefix(verb, "/")
	for _, cmd := range b.BotCommands() {
		if cmd.Command == name {
			return true
		}
	}
	return false
}

// username is the bot's own @name, resolved once and cached. A transport that
// cannot answer yields "", making @mentions unmatchable — replies still work,
// so a room degrades to reply-only rather than going deaf.
func (b *Bot) username(ctx context.Context) string {
	b.mu.Lock()
	name, known := b.botUser, b.botUserKnown
	b.mu.Unlock()
	if known {
		return name
	}
	name, err := b.client.Username(ctx)
	if err != nil {
		// Once per outage, not per message: this runs on every inbound one.
		b.mu.Lock()
		warned := b.botUserWarned
		b.botUserWarned = true
		b.mu.Unlock()
		if !warned {
			b.log.Warn("could not resolve the bot username; group @mentions will not match until it resolves", "err", err)
		}
		return ""
	}
	b.mu.Lock()
	b.botUser, b.botUserKnown, b.botUserWarned = name, true, false
	b.mu.Unlock()
	return name
}

// burstWindow is the default text-message debounce.
const burstWindow = 400 * time.Millisecond

// runningJobs counts sess's live background jobs. Session.Jobs() lists ALL
// runtime jobs, so filter by JobInfo.ParentID.
func runningJobs(sess *shell3.Session) int {
	n := 0
	for _, j := range sess.Jobs() {
		if j.ParentID == sess.Name() && !j.Done {
			n++
		}
	}
	return n
}

// sessionHasRunningJob reports any live background job under sess.
func (b *Bot) sessionHasRunningJob(sess *shell3.Session) bool {
	return runningJobs(sess) > 0
}

// consumeWakes routes queued session input to its conversation.
func (b *Bot) consumeWakes(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-b.rt.Events():
			if !ok {
				return
			}
			if ev.Kind != shell3.Wake {
				continue
			}
			b.dispatchWake(ctx, ev.Session)
		}
	}
}

// dispatchWake runs queued input in the room that owns the session. Input for
// a detached session is left in its persisted transcript.
func (b *Bot) dispatchWake(ctx context.Context, id string) {
	c := b.convFor(id)
	if c == nil {
		return
	}
	c.wake(ctx)
}

// containsToolMarkup spots the raw <tool_call> wrapper several open-model
// chat templates use. No legitimate reply contains it: its presence means the
// provider failed to parse its own template and a tool call arrived as text.
func containsToolMarkup(text string) bool {
	return strings.Contains(text, "<tool_call")
}

// malformedReplyNotice is what a user turn posts in place of a corrupt reply.
const malformedReplyNotice = "⚠️ the model produced malformed output (raw tool-call markup) — reply suppressed; ask me to send the transcript if needed"

// withReplyContext prepends the replied-to message as a capped blockquote so
// the model sees what the user is responding to, unchanged when there is no
// reply. Used for both known and unknown reply ids.
func withReplyContext(text, replyTo string) string {
	replyTo = strings.TrimSpace(replyTo)
	if replyTo == "" {
		return text
	}
	lines := strings.Split(strutil.Truncate(replyTo, 1500), "\n")
	for i, ln := range lines {
		lines[i] = "> " + ln
	}
	return strings.Join(lines, "\n") + "\n\n" + text
}

// roomForOwner resolves a live session's room from memory or its durable
// surface marker.
func (b *Bot) roomForOwner(ownerID string) *conversation {
	if ownerID == "" {
		return nil
	}
	if c := b.convFor(ownerID); c != nil {
		return c
	}
	st := b.threads.currentStore()
	if st == nil {
		return nil
	}
	surface, ok := st.SurfaceForSession(ownerID)
	if !ok {
		return nil
	}
	chatID, ok := chatIDFromSurface(b.threads.hostSurface(), surface)
	if !ok {
		return nil
	}
	return b.conv(chatID)
}

// roomOrHome is roomForOwner with the home chat as the fallback.
func (b *Bot) roomOrHome(ownerID string) *conversation {
	if c := b.roomForOwner(ownerID); c != nil {
		return c
	}
	return b.homeConv()
}

// Inbox is every room's pending user mail and undrained session input:
// zero-token, deterministic, and included in /status.
func (b *Bot) Inbox() string { return b.renderInbox() }

func (b *Bot) renderInbox() string {
	type roomInbox struct {
		chatID      int64
		mail        []inMail
		queuedInput bool
		active      bool
	}
	var rooms []roomInbox
	anyQueued, anyActive := false, false
	for _, c := range b.allConvs() {
		c.mu.Lock()
		r := roomInbox{
			chatID:      c.chatID,
			mail:        append([]inMail{}, c.mailQueue...),
			queuedInput: c.main != nil && c.main.HasQueuedInput(),
			active:      c.turnActive,
		}
		c.mu.Unlock()
		anyQueued = anyQueued || len(r.mail) > 0 || r.queuedInput
		anyActive = anyActive || r.active
		rooms = append(rooms, r)
	}
	sort.Slice(rooms, func(i, j int) bool { return rooms[i].chatID < rooms[j].chatID })

	if !anyQueued {
		if anyActive {
			return "📥 inbox empty — a turn is running, nothing queued behind it"
		}
		return "📥 inbox empty"
	}
	var sb strings.Builder
	sb.WriteString("📥 inbox\n")
	for _, r := range rooms {
		if len(r.mail) == 0 && !r.queuedInput {
			continue
		}
		fmt.Fprintf(&sb, "· chat %d\n", r.chatID)
		for _, m := range r.mail {
			text := strings.TrimSpace(m.text)
			if text == "" {
				text = "(attachment)"
			}
			fmt.Fprintf(&sb, "  · from you: %s\n", strutil.Truncate(text, 80))
		}
		if r.queuedInput {
			sb.WriteString("  · queued session input waiting for its turn\n")
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

// LiveSession is any room's live conversation, if one exists.
func (b *Bot) LiveSession() *shell3.Session {
	for _, c := range b.allConvs() {
		if s := c.session(); s != nil {
			return s
		}
	}
	return nil
}
