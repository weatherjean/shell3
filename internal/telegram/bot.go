//go:build unix

package telegram

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/shell3"
	"github.com/weatherjean/shell3/internal/strutil"
)

// Bot routes several Telegram chats to the shell3 runtime, one long-lived
// conversation PER CHAT: every message in a room continues that room's own
// session (a Telegram reply adds quoted context, it never forks), /new resets
// only the room it was typed in, and each room's session id persists in the
// runs store under its own surface key so a restart resumes all of them.
//
// Authorization is per SENDER, never per room: an allowlisted user drives the
// agent wherever they speak, and nobody else drives it anywhere. In a group a
// message must also be ADDRESSED to the bot (an @mention or a reply to one of
// its own messages) — see trigger.go. Everything else is dropped on the update
// loop, before attachments are saved and before any session exists.
//
// Rooms run turns concurrently, one slot each, under a global cap; sending
// always succeeds (mid-turn mail queues and drains as one batch turn).
// Background-job completions come back to the room that spawned them; cron
// results and orphans land in the home chat.
type Bot struct {
	client tgClient
	rt     *shell3.Runtime
	// homeChat is where cron results and ownerless completions land. It is
	// NOT an access rule: rooms are authorized by who speaks in them.
	homeChat int64
	// allow decides WHO may drive the agent, independent of which chat this
	// is. Never nil after NewBot: a nil allowlist denies everyone.
	allow *senderAllowlist

	workDir string // resolves relative paths for send_media_telegram
	// configDir is the loaded config directory. send_media_telegram refuses to
	// send anything inside it (media dir excepted) — by path AND by inode, so
	// a symlink or hardlink cannot launder a credentials file out. "" disables
	// only those two checks; the rest of safeOpen still applies.
	configDir string

	// threads is the surface-agnostic thread index handle: every room derives
	// its own index from it (surface "telegram:<chatid>"). Kept for the whole
	// process, across /reload, so a generation swap never strands a marker.
	threads *ThreadIndex

	mu sync.Mutex // guards the room registry and the mutable wiring below
	// convs is the room registry: one conversation per chat id, created the
	// first time an allowlisted sender addresses the bot there.
	convs map[int64]*conversation
	// activeTurns counts rooms currently holding a turn slot, bounded by
	// maxTurns — without a global cap, N rooms fan out N concurrent agents.
	activeTurns int
	maxTurns    int
	// reloading blocks new turns while a config swap is in flight: rooms are
	// independent, so without it one room could start a turn against the old
	// Parts while another room's /reload is replacing them.
	reloading bool
	debounce  time.Duration
	// cronID is the adopted cron dispatch parent's session id — the jobs/runs
	// source that never runs turns and is never a room's conversation.
	cronID string
	// cron is the adopted parent handle (the jobs/runs source the dash reads).
	cron *shell3.Session

	quietMode *QuietStore // the /quiet toggle's store; nil = never quiet

	runJob func(name string) error             // fires a cron job by name; nil if no scheduler
	reload func() (shell3.ReloadResult, error) // performs a full config reload; nil if unset

	// kitCommands are the kit's declared host commands and the runner that
	// answers one. Guarded by mu because /reload swaps them mid-life.
	kitCommands   []KitCommand
	kitCommandRun func(ctx context.Context, name, arg string) (string, error)
	pendingReload bool // set by the reload tool mid-turn; applied at end-of-turn

	// chatSettings is the operator's per-room configuration from the wiring's
	// chats: block (guarded by b.mu, replaced by /reload).
	chatSettings map[int64]roomSettings
	// readContext reads a room's declared context files through the config
	// package's reader (cap, elision, warnings). nil = layer 3 is off.
	readContext func(paths []string) string

	// metaMu guards the chat metadata cache. It is separate from b.mu
	// because a cache miss makes a NETWORK call (getChat), and holding the
	// registry lock across that would stall every room's routing.
	metaMu        sync.Mutex
	chatMetaCache map[int64]chatMeta
	// metaInflight guards against one background refresh per turn in a busy
	// room: at most one getChat per room is ever in flight.
	metaInflight map[int64]bool

	// dashURL mints a freshly tokened dashboard URL for /dash (guarded by
	// b.mu). nil = the dash is disabled or failed to start.
	dashURL func() (string, error)

	// botUser is the bot's own @username, resolved once from the transport
	// and used to spot an @mention in a group. botUserKnown separates "not
	// looked up yet" from "looked up and the transport could not say".
	botUser      string
	botUserKnown bool
	// botUserWarned throttles the "could not resolve the bot username"
	// warning to once per outage — username() runs on every inbound message.
	botUserWarned bool

	// log records host-side faults that never reach the user (a failed
	// current-session marker write, etc). Defaults to Noop so every existing
	// caller of NewBot keeps working unchanged; SetLogger opts in.
	log applog.Logger
}

// defaultMaxTurns bounds concurrent turns across all rooms.
const defaultMaxTurns = 4

// NewBot wires a Bot over the runtime. homeChat is where cron results and
// ownerless completions land; threads is the process-wide thread index handle
// each room derives its own surface from (constructed once by the host and
// kept across /reload).
func NewBot(client tgClient, rt *shell3.Runtime, homeChat int64, threads *ThreadIndex) *Bot {
	// Default allowlist: the home chat's owner. SetAllowFrom narrows or
	// widens it.
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

// SetMaxConcurrentTurns bounds how many rooms may hold a turn slot at once.
// A non-positive value keeps the default.
func (b *Bot) SetMaxConcurrentTurns(n int) {
	if n <= 0 {
		return
	}
	b.mu.Lock()
	b.maxTurns = n
	b.mu.Unlock()
}

// SetHomeChat sets where cron results and ownerless completions land.
func (b *Bot) SetHomeChat(id int64) {
	b.mu.Lock()
	b.homeChat = id
	b.mu.Unlock()
}

// SetLogger wires the app log for host-side faults that never reach the
// user. Optional: a Bot with none logs nothing (NewBot defaults to Noop).
func (b *Bot) SetLogger(log applog.Logger) {
	if log != nil {
		b.log = log
	}
}

// SetAllowFrom replaces the sender allowlist from configured user ids. An
// empty list keeps the default (the chat owner alone), so a config that never
// mentions allow_from behaves exactly as it always did.
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

// allowlist returns the current sender allowlist. Read under b.mu because
// /reload can replace it from a turn goroutine while the update loop is
// authorizing an inbound message.
func (b *Bot) allowlist() *senderAllowlist {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.allow
}

// SetJobRunner wires /run <job> to the scheduler's manual fire. Written from
// the reload path (which can run on a turn goroutine) and read by /run on the
// update loop, so it goes through b.mu like the rest of the mutable wiring.
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

// LiveSession returns the current main conversation's session, or any live
// adopted session, or nil — the dash index's status source. Callers must
// tolerate nil (render.DashIndexHTML does).
func (b *Bot) LiveSession() *shell3.Session { return b.anyLiveSession() }

// SetDash wires /dash to the host's URL minter: each call returns the dash
// base URL with a fresh ~1h token appended. nil (never set) means the dash
// is disabled and /dash says so.
func (b *Bot) SetDash(mint func() (string, error)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.dashURL = mint
}

// SetQuiet installs the /quiet toggle's store. Nil (or an unset path) means
// quiet can never turn on — every post rings.
func (b *Bot) SetQuiet(s *QuietStore) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.quietMode = s
}

// isQuiet reports whether the /quiet toggle is on. Agent-initiated posts
// (⏰/🔔/✉️) send silently under it; replies to the user's own messages and
// ⚠️ failures always ring.
func (b *Bot) isQuiet() bool {
	b.mu.Lock()
	s := b.quietMode
	b.mu.Unlock()
	return s.Get() // nil-safe: a nil store reads as off
}

// SetWorkDir sets the directory used to resolve relative paths passed to
// send_media_telegram (the agent's session workdir).
func (b *Bot) SetWorkDir(dir string) { b.workDir = dir }

// SetConfigDir sets the config directory send_media_telegram refuses to send
// from (see safeOpen). Unset, the path- and inode-containment checks are
// skipped — so every front-end that registers the tool must supply it.
func (b *Bot) SetConfigDir(dir string) { b.configDir = dir }

// DecorateChatSession registers the bot's host tools (send_media_telegram,
// reload, status) on a main chat session. The host wires it into
// Runtime.SetSessionDecorator so every chat session — and every one rebuilt by
// a reload — gets the tools; subagent children (Headless) are skipped there.
func (b *Bot) DecorateChatSession(s *shell3.Session) {
	b.registerSendTool(s)
	b.registerReloadTool(s)
	b.registerStatusTool(s)
}

// AdoptSession registers the externally-created cron dispatch parent: the
// jobs/runs source that runs no turns, is never woken, and is never the main
// conversation.
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

// handleMsg routes one inbound message. It runs on the single update loop, so
// message handling is serialized; only the turn itself runs on its own
// goroutine.
//
// Two gates gate everything, in this order:
//
//  1. The SENDER must be allowlisted. This is checked before the room is even
//     resolved, so a stranger's message never creates a conversation, never
//     saves an attachment, and never costs a token — whichever room it lands
//     in. It is also checked BEFORE the command branch: Telegram delivers
//     /commands from every group member, so a gate on the turn path alone
//     would still let a stranger /stop a turn or /new the conversation away.
//  2. In a GROUP the message must be ADDRESSED to the bot (an @mention or a
//     reply to one of its own messages). A room full of people talking to each
//     other is not a prompt; only what is aimed at the bot enters its context.
//     In a private chat every message is addressed to the bot, so the gate is
//     a no-op there.
func (b *Bot) handleMsg(ctx context.Context, m Msg) {
	// A group becoming a supergroup changes its chat id. Telegram announces
	// it once, as a service message with no sender, so this runs BEFORE the
	// sender gate — there is nobody to authorize, and the announcement is
	// Telegram's, not a user's. Missing it would strand the room's
	// conversation under an id that never speaks again.
	if m.MigratedTo != 0 {
		b.migrateRoom(m.ChatID, m.MigratedTo)
		return
	}
	if !b.allowlist().allows(m.SenderID) {
		return // unauthorized sender: drop silently
	}
	// Peek, don't create: a room must not spring into existence for chatter
	// that never reaches a turn. An allowlisted person saying "lunch?" in a
	// group would otherwise leave a phantom room in the registry, the inbox
	// and every room listing — one per chat the bot can see.
	c := b.peekConv(m.ChatID)
	if c != nil {
		c.setGroup(m.ChatType)
	}
	isGroup := m.ChatType != "" && m.ChatType != "private"

	if strings.HasPrefix(m.Text, "/") {
		verb, suffix, hasSuffix := strings.Cut(strings.Fields(m.Text)[0], "@")
		// "/stop@otherbot" is a command for a DIFFERENT bot in the same
		// group. With privacy mode off we are delivered it anyway, and
		// routing on the bare verb would let another bot's users stop our
		// turns and /new our conversations away.
		if botUser := b.username(ctx); hasSuffix && botUser != "" && !strings.EqualFold(suffix, botUser) {
			// With no resolvable username we cannot tell whose command this
			// is. Answering is the safer failure: the sender is allowlisted
			// either way, and dropping would make every "/stop@mybot" a
			// silent no-op during a getMe outage.
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

	// Text messages ride a short debounce window first: Telegram splits a
	// long message into several updates arriving back to back, and the burst
	// merges them into ONE turn. Media messages flush the pending burst and
	// dispatch immediately (their attachment note needs the resolved session
	// from a turn goroutine, never the update loop).
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
// yet. A room with no conversation has never posted anything, so no reply can
// point at it — only an @mention can open one.
func roomAddressed(c *conversation, m Msg, botUser string) bool {
	if c == nil {
		return mentions(m.Text, botUser)
	}
	return c.addressed(m, botUser)
}

// knowsCommand reports whether verb ("/stop") is a command this bot answers —
// a built-in or one the kit declares.
func (b *Bot) knowsCommand(verb string) bool {
	name := strings.TrimPrefix(verb, "/")
	for _, cmd := range b.BotCommands() {
		if cmd.Command == name {
			return true
		}
	}
	return false
}

// username returns the bot's own @name, resolved once from the transport and
// cached. A transport that cannot answer yields "", which makes @mentions
// unmatchable — replies to the bot still work, so a room degrades to
// reply-only rather than going deaf.
func (b *Bot) username(ctx context.Context) string {
	b.mu.Lock()
	name, known := b.botUser, b.botUserKnown
	b.mu.Unlock()
	if known {
		return name
	}
	name, err := b.client.Username(ctx)
	if err != nil {
		// Warn ONCE per outage, not once per group message: this runs on
		// every inbound message, and a transport that cannot answer would
		// otherwise fill the app log with the same line.
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

// sessionHasRunningJob reports whether sess has a background job (bash_bg or
// subagent) still running. Session.Jobs() lists ALL runtime jobs, so filter by
// the parent registry name (JobInfo.ParentID == Session.Name()).
func (b *Bot) sessionHasRunningJob(sess *shell3.Session) bool {
	for _, j := range sess.Jobs() {
		if j.ParentID == sess.Name() && !j.Done {
			return true
		}
	}
	return false
}

// consumeWakes routes out-of-turn runtime events to the session that owns them.
// A Wake fires when a session's inbox gains an item while idle — a subagent
// finished, or a bash_bg landed. Running the queued turn lets that session's
// agent narrate the result back into its thread.
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

// dispatchWake handles a Wake for store-session id: it finds the ROOM whose
// conversation is that session and runs the queued turn there. A wake for any
// other session (the cron parent, a session /new left behind draining old
// jobs) is dropped — its completions re-route through the CompletionHost.
func (b *Bot) dispatchWake(ctx context.Context, id string) {
	c := b.convFor(id)
	if c == nil {
		return
	}
	c.wake(ctx)
}

// containsToolMarkup reports whether text carries raw tool-call template
// markup — the <tool_call> wrapper several open-model chat templates use
// (MiniMax, Qwen, GLM). No legitimate reply contains it: its presence means
// the provider failed to parse its own template and the model's tool call
// arrived as text. Such a reply is corrupt output, not an answer.
func containsToolMarkup(text string) bool {
	return strings.Contains(text, "<tool_call")
}

// malformedReplyNotice is what a user turn posts in place of a corrupt reply.
const malformedReplyNotice = "⚠️ the model produced malformed output (raw tool-call markup) — reply suppressed; the dash has the transcript"

// withReplyContext prepends the replied-to message as a capped markdown
// blockquote so the model sees what the user is responding to. Returns text
// unchanged when there's no reply. Kept for both the fresh-session fallback
// (unknown reply id) and known-thread replies — the model benefits from seeing
// the quoted portion either way.
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

// The Bot implements shell3.CompletionHost. Delivery is per ROOM: a job's
// result comes back where the job was started, and only what has no room of
// its own (a cron tick, an orphan) lands in the home chat. All three methods
// are invoked on job-runtime goroutines, so network sends run on their own
// goroutines and never stall the job runtime.
var _ shell3.CompletionHost = (*Bot)(nil)

// roomForOwner resolves the room that owns a completion, in two stages.
//
// Stage 1 is the live registry. Stage 2 is the runs store: after a restart,
// outbox redelivery and RecoverCompletions run before any room has been used,
// so a memory-only lookup would report every recovered job to the home chat —
// the opposite of "a job reports where it was started". The threads table
// already maps each room's surface to its session id, so the reverse lookup
// restores the room without the user having to speak first.
//
// Returns nil when the owner names no room at all (cron, or a session /new
// left behind); the caller falls back to the home chat.
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

// PostCompletion posts a completion message to the room that spawned it (the
// home chat for cron and orphans), threaded onto that room's conversation.
// p.CronJob != "" posts "⏰ <cronJob>: <text>", otherwise "🔔 <text>" (⚠️
// failure floors carry their own marker). The send is SYNCHRONOUS and its
// failure is returned: the router keeps the completion's outbox row on a
// non-nil error so the post is redelivered (periodic RedeliverUndelivered
// tick, or the next boot) instead of vanishing into a transport outage.
// Blocking is fine here — this runs on a job-runtime goroutine, never a
// conversation turn.
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
	// Under /quiet, background posts arrive without a ping — except failures,
	// which always ring.
	var opts []SendOpt
	if b.isQuiet() && !failure {
		opts = append(opts, SendOpt{Silent: true})
	}
	c := b.roomOrHome(p.OwnerID)
	// Background posts are plain messages, never Telegram replies — a quote
	// header on every ⏰/🔔 reads as noise. recordSent still advances the
	// room's anchor so a steer-catchup can thread to something sensible.
	sess := c.session()
	// Background posts are single chunks in practice (tails are capped), but
	// chunk anyway; the first undelivered chunk fails the whole post so the
	// router redelivers it complete.
	ctx := context.Background()
	var firstErr error
	for _, part := range chunk(text) {
		if err := c.postChunk(ctx, sess, "", part, opts...); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// WakeOwner delivers note into the owning session, in whichever room that
// session belongs to — including a room this process has not opened yet.
//
// The store lookup is not an optimization: after a restart, the default mail
// route for every recovered completion is this method, and a live-registry-only
// check would answer false for every one of them, sending a job started in the
// work room to the home chat. That is precisely the routing this feature
// exists to avoid, so the room is resumed here rather than declared missing.
//
// False means the note has no room to go to: the cron parent, or a session
// /new left behind (the room's marker has moved on, so the resumed session is
// a different one). The router's StartFreshTurn fallback then lands it in the
// home chat. Nothing is ever lost.
func (b *Bot) WakeOwner(ownerID, note string) bool {
	c := b.roomForOwner(ownerID)
	if c == nil {
		return false
	}
	sess, err := c.mainSession()
	if err != nil || sess == nil {
		return false
	}
	if sess.ID() != ownerID {
		// The room moved on (a /new since this job started). Its completion
		// is an orphan, which the home chat handles.
		return false
	}
	sess.NotifyText(note) // queue + Wake; consumeWakes runs the quiet turn
	return true
}

// StartFreshTurn queues note into the home chat's conversation (creating it if
// this is the very first activity) and wakes it — the catch-all delivery for
// completions whose owner is no longer a live room: cron results, orphans,
// and jobs outliving a /new.
func (b *Bot) StartFreshTurn(note string) {
	c := b.homeConv()
	sess, err := c.mainSession()
	if err != nil {
		// Degrade to a raw post rather than dropping the completion.
		var opts []SendOpt
		if b.isQuiet() {
			opts = append(opts, SendOpt{Silent: true})
		}
		go c.sendReply(context.Background(), "🔔 "+note, opts...)
		return
	}
	sess.NotifyText(note)
}

// Inbox reports what is queued while (or since) turns run, across every
// room: each room's pending user mail and any undrained agent mail.
// Zero-token, deterministic — the dash index's Inbox section.
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

// anyLiveSession returns a session for host code that wants one but doesn't
// care which — any room's conversation, else the adopted cron parent.
// Callers must tolerate nil (render.DashIndexHTML does).
func (b *Bot) anyLiveSession() *shell3.Session {
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
