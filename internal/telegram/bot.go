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

// Bot routes several Telegram chats to the runtime, one long-lived
// conversation PER CHAT: every message continues that room's session (a reply
// adds quoted context, it never forks), /new resets only the room it was typed
// in, and each room's id persists under its own surface key so a restart
// resumes all of them.
//
// Authorization is per SENDER, never per room: an allowlisted user drives the
// agent wherever they speak, nobody else anywhere. In a group the message must
// also be ADDRESSED to the bot (trigger.go); everything else is dropped on the
// update loop, before attachments are saved and before any session exists.
//
// Rooms run turns concurrently, one slot each, under a global cap; sending
// always succeeds, since mid-turn mail queues and drains as one batch turn.
// Completions return to the room that spawned them; cron results and orphans
// land in the home chat.
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
	// Kept across /reload, so a generation swap never strands a marker.
	threads *ThreadIndex

	mu sync.Mutex // guards the room registry and the mutable wiring below
	// convs holds one conversation per chat id, created the first time an
	// allowlisted sender addresses the bot there.
	convs map[int64]*conversation
	// activeTurns counts rooms holding a slot, bounded by maxTurns — without
	// a global cap N rooms fan out N concurrent agents.
	activeTurns int
	maxTurns    int
	// reloading blocks new turns during a config swap: rooms are independent,
	// so one could otherwise start a turn against Parts being replaced.
	reloading bool
	debounce  time.Duration
	// cronID is the adopted cron dispatch parent: a jobs/runs source that runs
	// no turns and is never a room's conversation.
	cronID string
	// cron is the adopted parent handle (the jobs/runs source the dash reads).
	cron *shell3.Session

	quietMode *QuietStore // the /quiet toggle's store; nil = never quiet

	runJob func(name string) error             // fires a cron job by name; nil if no scheduler
	reload func() (shell3.ReloadResult, error) // performs a full config reload; nil if unset

	// kitCommands are the kit's declared commands and their runner. Guarded
	// by mu — /reload swaps them mid-life.
	kitCommands   []KitCommand
	kitCommandRun func(ctx context.Context, name, arg string) (string, error)
	pendingReload bool // set by the reload tool mid-turn; applied at end-of-turn

	// chatSettings is the wiring's per-room chats: block. Replaced by /reload.
	chatSettings map[int64]roomSettings
	// readContext reads a room's context: files through config's reader (cap,
	// elision, warnings). nil = that brief layer is off.
	readContext func(paths []string) string

	// metaMu guards the chat metadata cache, separate from b.mu because a miss
	// makes a network call and holding the registry lock would stall routing.
	metaMu        sync.Mutex
	chatMetaCache map[int64]chatMeta
	// metaInflight keeps at most one getChat per room in flight.
	metaInflight map[int64]bool

	// dashURL mints a freshly tokened /dash URL. nil = the dash is off.
	dashURL func() (string, error)

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

// NewBot wires a Bot over the runtime. homeChat takes cron results and
// ownerless completions; threads is the process-wide index each room derives
// its own surface from, kept across /reload.
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
// completion posts included, which is the traffic no other record holds.
//
// The startup banner and the "/" command registration are sent through the
// concrete API client by the caller, before the Bot exists, and so are NOT in
// the log. They are constant text sent once per boot; the log's own first line
// records the start instead.
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

// allowlist reads the sender allowlist under b.mu: /reload can replace it
// from a turn goroutine while the update loop authorizes a message.
func (b *Bot) allowlist() *senderAllowlist {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.allow
}

// SetJobRunner wires /run <job> to the scheduler's manual fire. Written from
// the reload path and read on the update loop, so it goes through b.mu.
func (b *Bot) SetJobRunner(fn func(name string) error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.runJob = fn
}

// jobRunner returns the current /run handler (nil when no scheduler is armed).
func (b *Bot) jobRunner() func(name string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.runJob
}

// SetReloader wires /reload (and the reload tool) to the host's reload coordinator.
func (b *Bot) SetReloader(fn func() (shell3.ReloadResult, error)) { b.reload = fn }

// SetDash wires /dash to the host's URL minter, which appends a fresh ~1h
// token. nil means the dash is disabled and /dash says so.
func (b *Bot) SetDash(mint func() (string, error)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.dashURL = mint
}

// SetQuiet installs the /quiet store. Nil means quiet never turns on.
func (b *Bot) SetQuiet(s *QuietStore) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.quietMode = s
}

// isQuiet reports the /quiet toggle. Agent-initiated posts send silently
// under it; replies to the user and ⚠️ failures always ring.
func (b *Bot) isQuiet() bool {
	b.mu.Lock()
	s := b.quietMode
	b.mu.Unlock()
	return s.Get() // nil-safe: a nil store reads as off
}

// SetWorkDir sets the directory used to resolve relative paths passed to
// send_media_telegram (the agent's session workdir).
func (b *Bot) SetWorkDir(dir string) { b.workDir = dir }

// SetConfigDir sets the directory send_media_telegram refuses to send from.
// Unset skips the path and inode checks, so every front-end must supply it.
func (b *Bot) SetConfigDir(dir string) { b.configDir = dir }

// DecorateChatSession registers the bot's host tools on a main chat session.
// The host wires it into Runtime.SetSessionDecorator, so every session and
// every one a reload rebuilds gets them; headless children are skipped there.
func (b *Bot) DecorateChatSession(s *shell3.Session) {
	b.registerSendTool(s)
	b.registerReloadTool(s)
	b.registerStatusTool(s)
}

// AdoptSession registers the cron dispatch parent: a jobs/runs source that
// runs no turns, is never woken, and is never a conversation.
func (b *Bot) AdoptSession(s *shell3.Session) {
	b.mu.Lock()
	b.cronID = s.ID()
	b.cron = s
	b.mu.Unlock()
}

// Run consumes inbound messages and the wake bus until ctx is cancelled.
func (b *Bot) Run(ctx context.Context) {
	go b.consumeWakes(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case m, ok := <-b.client.Updates(ctx):
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
//  2. In a GROUP the message must be ADDRESSED to the bot. A room of people
//     talking to each other is not a prompt. A no-op in a private chat.
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
		if isGroup && !b.knowsCommand(verb) {
			return
		}
		cmdRoom := b.conv(m.ChatID)
		cmdRoom.setGroup(m.ChatType)  // the room may be brand new: this is its first sighting
		cmdRoom.handleCommand(ctx, m) // defined in commands.go
		return
	}
	if isGroup && !roomAddressed(c, m, b.username(ctx)) {
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

// consumeWakes routes out-of-turn runtime events to their session. A Wake
// fires when an idle session's inbox gains an item, and running the queued
// turn lets that agent narrate the result into its own thread.
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

// dispatchWake runs the queued turn in the ROOM whose conversation is that
// session. A wake for any other session — the cron parent, one /new left
// behind — is dropped; its completions re-route through the CompletionHost.
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
const malformedReplyNotice = "⚠️ the model produced malformed output (raw tool-call markup) — reply suppressed; the dash has the transcript"

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

// The Bot implements shell3.CompletionHost. Delivery is per ROOM: a result
// returns where the job was started, and only what has no room — a cron tick,
// an orphan — lands in the home chat. All three run on job-runtime
// goroutines, so their sends never stall a conversation.
var _ shell3.CompletionHost = (*Bot)(nil)

// roomForOwner resolves a completion's room: the live registry first, then
// the runs store. The second stage is not an optimization — after a restart,
// redelivery runs before any room has been used, so a memory-only lookup
// would report every recovered job to the home chat. The threads table
// already maps each surface to its session, so the reverse lookup restores
// the room without the user speaking first.
//
// nil when the owner names no room (cron, a session /new left behind); the
// caller falls back to the home chat.
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

// PostCompletion posts to the room that spawned the job, threaded onto its
// conversation: "⏰ <cronJob>: …" for a cron origin, "🔔 …" otherwise (⚠️
// failure floors carry their own marker). The send is SYNCHRONOUS and its
// error is returned, so the router keeps the outbox row and redelivers rather
// than losing the post to an outage. Blocking is fine on a job goroutine.
func (b *Bot) PostCompletion(p shell3.CompletionPost) error {
	text := p.Text
	if strings.TrimSpace(text) == "" {
		text = "(no output)"
	}
	failure := strings.HasPrefix(text, "⚠️")
	switch {
	case p.Aside:
		text = "💬 " + text
	case p.CronJob != "":
		text = fmt.Sprintf("⏰ %s: %s", p.CronJob, text)
	case failure:
		// The runtime's failure-floor text carries its own marker.
	default:
		text = "🔔 " + text
	}
	// Under /quiet these arrive without a ping; failures always ring.
	var opts []SendOpt
	if b.isQuiet() && !failure {
		opts = append(opts, SendOpt{Silent: true})
	}
	c := b.roomOrHome(p.OwnerID)
	// Plain messages, never replies — a quote header on every ⏰/🔔 is noise.
	// recordSent still advances the anchor so a steer-catchup can thread.
	sess := c.session()
	// Single chunks in practice, but chunk anyway; the first failed chunk
	// fails the whole post, so the router redelivers it complete.
	ctx := context.Background()
	var firstErr error
	for _, part := range chunk(text) {
		if err := c.postChunk(ctx, sess, "", part, opts...); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// WakeOwner delivers the mail into the owning session, in whatever room it
// belongs to — including one this process has not opened yet, which is why
// roomForOwner consults the store: after a restart this is the default route
// for every recovered completion.
//
// A Required mail also arms the room's fallback, so the turn that reads it
// answers the user or the job's own result posts in its place.
//
// False means there is no room: the cron parent, or a session /new left
// behind. StartFreshTurn then lands it in the home chat, so nothing is lost.
func (b *Bot) WakeOwner(m shell3.Mail) bool {
	c := b.roomForOwner(m.OwnerID)
	if c == nil {
		return false
	}
	sess, err := c.mainSession()
	if err != nil || sess == nil {
		return false
	}
	if sess.ID() != m.OwnerID {
		// A /new since this job started: the completion is an orphan.
		return false
	}
	// Arm before queueing: the wake can be answered on another goroutine the
	// moment NotifyText returns, and a bind armed after that turn settled
	// would sit unanswered until the next one.
	if m.Required {
		c.requireReport(m.Post)
	}
	sess.NotifyText(m.Note) // queue + Wake; consumeWakes runs the quiet turn
	return true
}

// StartFreshTurn queues the mail into the home chat, creating it on first use
// — the catch-all for completions with no live owning room.
func (b *Bot) StartFreshTurn(m shell3.Mail) {
	c := b.homeConv()
	sess, err := c.mainSession()
	if err != nil {
		// Degrade to a raw post rather than dropping the completion. That
		// post IS the delivery, so a Required mail needs no second one — and
		// it posts the FALLBACK, since m.Note is written for the agent and
		// carries instructions ("NO_REPLY is not available here") that mean
		// nothing to a reader.
		text := m.Note
		if m.Required && m.Fallback != "" {
			text = m.Fallback
		}
		var opts []SendOpt
		if b.isQuiet() {
			opts = append(opts, SendOpt{Silent: true})
		}
		go c.sendReply(context.Background(), "🔔 "+text, opts...)
		return
	}
	if m.Required {
		c.requireReport(m.Post)
	}
	sess.NotifyText(m.Note)
}

// Inbox is every room's pending user mail and undrained agent mail:
// zero-token, deterministic, the dash index's Inbox section.
func (b *Bot) Inbox() string { return b.renderInbox() }

func (b *Bot) renderInbox() string {
	type roomInbox struct {
		chatID    int64
		mail      []inMail
		agentMail bool
		active    bool
	}
	var rooms []roomInbox
	anyQueued, anyActive := false, false
	for _, c := range b.allConvs() {
		c.mu.Lock()
		r := roomInbox{
			chatID:    c.chatID,
			mail:      append([]inMail{}, c.mailQueue...),
			agentMail: c.main != nil && c.main.HasQueuedInput(),
			active:    c.turnActive,
		}
		c.mu.Unlock()
		anyQueued = anyQueued || len(r.mail) > 0 || r.agentMail
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
		if len(r.mail) == 0 && !r.agentMail {
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
		if r.agentMail {
			sb.WriteString("  · agent mail waiting for its turn\n")
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

// LiveSession is any room's conversation, else the adopted cron parent, for
// host code that wants a session and does not care which. May be nil.
func (b *Bot) LiveSession() *shell3.Session {
	for _, c := range b.allConvs() {
		if s := c.session(); s != nil {
			return s
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.cron
}

// hasTool reports whether sess's active agent has the named tool enabled.
func (b *Bot) hasTool(sess *shell3.Session, name string) bool {
	if sess == nil {
		return false
	}
	for _, t := range sess.Snapshot().Tools {
		if t.Name == name {
			return true
		}
	}
	return false
}
