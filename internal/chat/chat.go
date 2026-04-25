package chat

import (
	"context"
	"fmt"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/weatherjean/shell3/internal/hooks"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/personality"
	"github.com/weatherjean/shell3/internal/store"
	"github.com/weatherjean/shell3/internal/tui"
)

// LLMClient is the streaming LLM interface.
type LLMClient interface {
	Stream(ctx context.Context, msgs []llm.Message, tools []llm.ToolDefinition, onEvent func(llm.StreamEvent)) error
}

// Config holds all dependencies for a chat session.
type Config struct {
	LLM           LLMClient
	Hooks         *hooks.Runner
	Store         *store.Store
	Personality   personality.Personality
	WorkDir       string
	StatusLine    string
	Models        []string     // available models (from credentials)
	ModelSwitcher func(string) // called on /model switch
	Truncate      bool         // when true, show full bash output; default false = truncated
}

// programReleaser implements hooks.TTYReleaser backed by a tea.Program.
type programReleaser struct{ p *tea.Program }

func (r *programReleaser) Release() error { return r.p.ReleaseTerminal() }
func (r *programReleaser) Restore() error { return r.p.RestoreTerminal() }

// RunInteractive starts the BubbleTea TUI and blocks until the user quits.
func RunInteractive(ctx context.Context, cfg Config) error {
	sess := &session{}

	var sessionID int64
	if cfg.Store != nil {
		var err error
		sessionID, err = cfg.Store.StartSession()
		if err != nil {
			return fmt.Errorf("chat: start session: %w", err)
		}
		defer cfg.Store.EndSession(sessionID)
	}

	var lastUsage llm.Usage

	handleSlash := func(input string) tea.Cmd {
		cmd := strings.TrimSpace(strings.ToLower(input))
		return func() tea.Msg {
			switch cmd {
			case "/clear":
				sess.messages = nil
				return tui.AppendMsg(ansiDim + "\n[context cleared]\n" + ansiReset)
			case "/prune":
				pruned := pruneLastTurn(sess.messages)
				if len(pruned) == len(sess.messages) {
					return tui.AppendMsg(ansiDim + "\n[nothing to prune]\n" + ansiReset)
				}
				sess.messages = pruned
				return tui.AppendMsg(ansiDim + "\n[last turn removed from context]\n" + ansiReset)
			case "/usage":
				if lastUsage.TotalTokens == 0 {
					return tui.AppendMsg(ansiDim + "\n[no usage data yet]\n" + ansiReset)
				}
				return tui.AppendMsg(fmt.Sprintf(
					"\n"+ansiBold+"token usage"+ansiReset+" (last turn)\n"+
						ansiDim+"  prompt:     %d\n  completion: %d\n  total:      %d\n"+ansiReset,
					lastUsage.PromptTokens, lastUsage.CompletionTokens, lastUsage.TotalTokens,
				))
			case "/prompt":
				var sb strings.Builder
				fmt.Fprintf(&sb, "\n"+ansiBold+"system prompt:"+ansiReset+"\n")
				fmt.Fprintf(&sb, ansiDim+"%s"+ansiReset+"\n", cfg.Personality.SystemPrompt)
				fmt.Fprintf(&sb, ansiBold+"active tools:"+ansiReset+"\n")
				for _, t := range cfg.Personality.Tools {
					fmt.Fprintf(&sb, "  %s  %s\n", t.Name, t.Description)
				}
				return tui.AppendMsg(sb.String())
			case "/truncate":
				cfg.Truncate = !cfg.Truncate
				state := "off"
				if cfg.Truncate {
					state = "on"
				}
				return tui.AppendMsg(fmt.Sprintf(ansiDim+"\n[full output: %s]\n"+ansiReset, state))
			case "/exit", "/quit":
				return func() tea.Msg { return tea.Quit() }
			case "/help", "/list", "/":
				return tui.AppendMsg(slashHelp())
			default:
				// /model <name>
				if strings.HasPrefix(cmd, "/model") {
					name := strings.TrimSpace(input[6:])
					if name == "" {
						var sb strings.Builder
						fmt.Fprintf(&sb, "\n"+ansiBold+"available models:"+ansiReset+"\n")
						for _, m := range cfg.Models {
							fmt.Fprintf(&sb, "  %s\n", m)
						}
						return tui.AppendMsg(sb.String())
					}
					if cfg.ModelSwitcher != nil {
						cfg.ModelSwitcher(name)
					}
					return tui.AppendMsg(fmt.Sprintf(ansiDim+"\n[model: %s]\n"+ansiReset, name))
				}
				return tui.AppendMsg(fmt.Sprintf(
					ansiDim+"\nunknown command: %s  (type /help to list commands)\n"+ansiReset, input,
				))
			}
		}
	}

	submitFn := func(input string) tea.Cmd {
		if strings.HasPrefix(input, "/") {
			return handleSlash(input)
		}
		ch := make(chan tea.Msg, 256)
		prevLen := len(sess.messages)
		turnCtx, cancel := context.WithCancel(ctx)
		go func() {
			defer cancel()
			runTurn(turnCtx, cfg, sess, input, ch)
			// Capture usage from the turn for /usage command.
			// (Usage is also delivered via TurnDoneMsg to the model.)
			saveHistory(cfg, sess, sessionID, prevLen)
		}()
		return tea.Batch(
			func() tea.Msg { return tui.SetCancelMsg{Cancel: cancel} },
			tui.ReadCh(ch),
		)
	}

	model := tui.New("shell3", cfg.StatusLine, submitFn)

	rel := &programReleaser{}
	prog := tea.NewProgram(model)
	rel.p = prog
	cfg.Hooks.SetReleaser(rel)

	cfg.Hooks.OnSessionStart(ctx)
	defer cfg.Hooks.OnSessionEnd(ctx)

	_, err := prog.Run()
	return err
}

// RunOnce executes a single turn and prints output to stdout. No TUI.
func RunOnce(ctx context.Context, cfg Config, input string) error {
	sess := &session{}
	ch := make(chan tea.Msg, 256)
	go runTurn(ctx, cfg, sess, input, ch)

	for msg := range ch {
		switch v := msg.(type) {
		case tui.ChunkMsg:
			fmt.Print(string(v))
		case tui.AppendMsg:
			fmt.Print(string(v))
		case tui.TurnErrMsg:
			fmt.Fprintln(os.Stderr, "error:", v.Err)
		case tui.TurnDoneMsg:
			fmt.Println()
		}
	}
	return nil
}

func slashHelp() string {
	return "\n" + ansiBold + "slash commands:" + ansiReset + "\n" +
		"  /clear     reset conversation context\n" +
		"  /prune     remove last turn from context\n" +
		"  /model     list models or /model <name> to switch\n" +
		"  /usage     show token usage from last turn\n" +
		"  /prompt    dump system prompt and active tools\n" +
		"  /truncate  toggle truncated bash output\n" +
		"  /exit      quit shell3\n" +
		"  /help      show this help\n" +
		"\n" + ansiBold + "keyboard shortcuts:" + ansiReset + "\n" +
		"  enter          send message\n" +
		"  shift+enter    new line in message\n" +
		"  alt+enter      new line in message\n" +
		"  esc esc        clear input\n" +
		"  ctrl+c         cancel active response (when busy)\n" +
		"  ctrl+c ctrl+c  quit shell3 (when idle)\n" +
		"\n" + ansiBold + "shell passthrough:" + ansiReset + "\n" +
		"  !<cmd>     run shell command with full terminal\n"
}

// pruneLastTurn removes the last user message and everything that follows it.
func pruneLastTurn(messages []llm.Message) []llm.Message {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == llm.RoleUser {
			return messages[:i]
		}
	}
	return messages
}
