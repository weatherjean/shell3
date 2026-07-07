// Package shell3 embeds the shell3 coding agent as a library — from a one-shot
// prompt to an always-on personal agent hosting many concurrent chats. It loads
// the same shell3.lua config, store, and persona as the CLI by building on
// internal/agentsetup; internal/chat, internal/persona, and internal/llm are
// implementation details, not part of this package's public API.
//
// # Three entry points
//
// Run executes a single prompt and streams the turn's [Event]s until the
// channel closes (the session is built and torn down for you). Start gives a
// persistent multi-turn [Session] — the embedding equivalent of an open TUI —
// with agent switching, history, pruning, and parameter control. NewRuntime is
// the host shape: one [Runtime] owning the shared build (config, store,
// proxy spawner, log) and hosting N named [Session]s via [Runtime.Session].
// Start and Run are thin single-session wrappers over a Runtime.
//
// # Sessions and the single-turn contract
//
// A Session runs one turn at a time. [Session.Send] streams a turn's events and
// returns [ErrBusy] (as an Error event) if a turn is already in flight; drain
// the channel to completion before the next Send, Clear, SwitchAgent, or Prune.
// Name sessions on the runtime (e.g. "sess:1234") via [SessionOpts]; requesting an
// existing live name returns that session. Each session has its own agent,
// workdir, headless flag, and audit log.
//
// # Steering: inbox and Interject
//
// [Session.Interject] queues a message (and optional media [Part]s) from any
// goroutine. It never fails: while a turn runs the text is injected at the next
// round boundary as a system-reminder that the user sent input; while idle it is queued and
// the session Wakes (see below). [Session.Send] is the strict path that honors
// ErrBusy; [Session.SendParts] is Send with media attachments.
// [Session.RunQueued] runs one turn seeded from the queued inbox items — the
// host's response to a Wake — and no-ops on an empty inbox or a busy session.
//
// # Out-of-turn bus: Wake
//
// A long-lived host does not block on a single Send channel. [Runtime.Events]
// returns a shared <-chan [HostEvent]; an inbox gaining an item while the
// session is idle emits a [HostEvent] of kind [Wake] naming the session. The
// host runs one select loop: receive a HostEvent, match HostEvent.Session
// against each Session's Name, and call RunQueued to react. A single-session
// host created via Start can use [Session.WakeEvents] instead of holding a
// *Runtime. The bus is buffered and drops on a full buffer (Wake is a hint, not
// a queue — the next turn drains the inbox anyway).
//
//	rt, _ := shell3.NewRuntime(ctx, shell3.RuntimeSpec{WorkDir: home})
//	defer rt.Close()
//	sessions := map[string]*shell3.Session{ /* name → session */ }
//	for ev := range rt.Events() {
//		if ev.Kind != shell3.Wake {
//			continue
//		}
//		if s := sessions[ev.Session]; s != nil {
//			for e := range s.RunQueued(context.Background()) {
//				_ = e // stream tokens/tool calls to the chat surface
//			}
//		}
//	}
//
// # Inbound media
//
// SendParts and Interject accept []Part / ...Part attachments. A [Part] sets
// exactly one of Path (extension-routed) or Data (MIME-routed, MIME required) —
// so media attachments from any front-end never touch disk — with Kind [PartImage]
// or [PartAudio]. Size caps match read_media (10 MB images, 25 MB audio).
// SendParts is all-or-nothing (one invalid part rejects the turn with an Error);
// Interject drops invalid parts with a bracketed note and still delivers.
//
// # Subagents
//
// Subagents are an explicit registry of delegatable specialists. Declare one
// with shell3.subagent{name, description, ...} (the description is the
// model-facing "when to use"); it is not part of the Tab/agent rotation. An
// agent opts in by listing subagent handles: tools = { subagents = { explorer,
// researcher } } and sets delegation=true to receive the task/task_list/
// task_status/task_cancel tools.
//
// A subagent is an in-process background job. The agent spawns one with the
// task tool ({subagent_type, prompt, description}); the call returns immediately.
// The jobManager (this package, jobs.go) runs the child as a goroutine under a
// concurrency cap (shell3.background{max_concurrent=N}, default 8). On
// completion the jobManager wakes the parent session with a capped result summary
// injected into context — there is no subprocess, no .shell3_project/inbox.jsonl,
// and no fsnotify watch. Delegation is single-level: a subagent is not given the
// task tool. Max nesting depth is shell3.subagents{max_depth=N} (default 3).
// Cancellation is via task_cancel <id> through the jobManager.
package shell3
