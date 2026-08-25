//go:build unix

package telegram

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/weatherjean/shell3/internal/shell3"
)

// registerStatusTool gives the agent a `status` tool reporting where its config
// lives and what is currently active, the orientation a self-editing agent needs
// before changing the config and calling reload. Telegram-only host tool.
func (b *Bot) registerStatusTool(s *shell3.Session) {
	_ = s.RegisterHostTool(shell3.HostTool{
		Name: "status",
		Description: "Report your runtime status: the absolute path of the config directory " +
			"config file you can edit, your active agent and the agents available, the " +
			"model, the working directory, any scheduled cron jobs, and the OTHER Telegram " +
			"rooms you are live in (each with its own conversation, sharing this working " +
			"directory). Call this to find your config file before editing it (see the " +
			"self-evolve skill), and before touching shared files — another room may be " +
			"mid-turn in the same directory.",
		Parameters: map[string]any{"type": "object", "properties": map[string]any{}},
		Handler: func(ctx context.Context, argsJSON string) (string, error) {
			return b.statusToolHandler(ctx, s, argsJSON)
		},
	})
}

func (b *Bot) statusToolHandler(_ context.Context, s *shell3.Session, _ string) (string, error) {
	var sb strings.Builder

	cfgPath, err := b.rt.ConfigDir()
	if err != nil {
		cfgPath = "(could not resolve — run 'shell3 boot'?)"
	}
	fmt.Fprintf(&sb, "config: %s\n", cfgPath)

	snap := s.Snapshot()
	fmt.Fprintf(&sb, "agent: %s (available: %s)\n",
		s.ActiveAgent(), strings.Join(snap.Subagents, ", "))
	if snap.Model != "" {
		fmt.Fprintf(&sb, "model: %s\n", snap.Model)
	}

	wd := b.workDir
	if wd == "" {
		wd = "(default)"
	}
	fmt.Fprintf(&sb, "workdir: %s\n", wd)

	jobs := b.rt.Cron()
	if rooms := b.roomsStatus(); rooms != "" {
		sb.WriteString(rooms)
	}
	if len(jobs) == 0 {
		sb.WriteString("cron: none")
	} else {
		names := make([]string, len(jobs))
		for i, j := range jobs {
			names[i] = j.Name
		}
		fmt.Fprintf(&sb, "cron: %d job(s) — %s", len(jobs), strings.Join(names, ", "))
	}
	return sb.String(), nil
}

// roomsStatus lists the live rooms: which chats have a conversation, which
// are mid-turn, and how many background jobs each is running.
//
// It exists because every room shares ONE working directory, so two rooms can
// run bash in the same tree at the same time — room A checking out a branch
// while room B edits files. This is the agent's only way to see that coming.
// It is ADVISORY, not a lock: the answer can go stale a second later, and
// nothing here prevents the collision it warns about.
func (b *Bot) roomsStatus() string {
	rooms := b.allConvs()
	if len(rooms) == 0 {
		return ""
	}
	type row struct {
		chatID int64
		title  string
		brief  string
		busy   bool
		jobs   int
		sessID string
	}
	var rows []row
	for _, c := range rooms {
		sess := c.session()
		if sess == nil {
			continue // enrolled but no live conversation: nothing to collide with
		}
		r := row{chatID: c.chatID, busy: c.busy(), sessID: sess.ID()}
		meta := b.chatMetaFor(c.chatID)
		r.title = meta.title
		r.brief = briefState(meta, c.isGroupRoom(), b.settingsFor(c.chatID).useDescription())
		r.jobs = runningJobs(sess)
		rows = append(rows, r)
	}
	if len(rows) == 0 {
		return ""
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].chatID < rows[j].chatID })

	var sb strings.Builder
	fmt.Fprintf(&sb, "rooms: %d live (each has its OWN conversation; all share the working directory above —\n", len(rows))
	sb.WriteString("  check here before touching shared files, and note this is advice, not a lock)\n")
	for _, r := range rows {
		name := r.title
		if name == "" {
			name = "(untitled)"
		}
		state := "idle"
		if r.busy {
			state = "BUSY (mid-turn)"
		}
		fmt.Fprintf(&sb, "  · %s [%d] — %s", name, r.chatID, state)
		if r.jobs > 0 {
			fmt.Fprintf(&sb, ", %d background job(s)", r.jobs)
		}
		fmt.Fprintf(&sb, ", session %s\n", r.sessID)
		if r.brief != "" {
			fmt.Fprintf(&sb, "      description: %s\n", r.brief)
		}
	}
	return sb.String()
}

// briefState says, in one line, what this room's description contributes to
// the prompt and why — the three cases look identical from inside the prompt
// and have completely different fixes.
//
// "not visible" is the one worth naming: Telegram serves a group's
// description only to a bot that can see group info, so a restricted bot gets
// an empty string with no error, indistinguishable from a chat that never set
// one. Without this line the only way to tell them apart is reading the app
// log, which is not where anyone looks.
func briefState(meta chatMeta, group, useDescription bool) string {
	switch {
	case !group:
		return "" // a direct chat has no group description; say nothing
	case !useDescription:
		return "off for this room (use_description: false)"
	case !meta.known:
		return "not looked up yet"
	case meta.description != "":
		return fmt.Sprintf("in your prompt (%d bytes)", len(meta.description))
	default:
		return "not visible — either this chat has none, or Telegram is withholding it " +
			"because I cannot see group info here (promoting me to admin fixes that)"
	}
}
