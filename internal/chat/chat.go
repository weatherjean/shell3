package chat

import (
	"context"
	"fmt"
	"os"
	"strings"

	"io"

	tea "charm.land/bubbletea/v2"
	"github.com/weatherjean/shell3/internal/hooks"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/persona"
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
	Personality   persona.Persona
	WorkDir       string
	StatusLine    string
	ModeLabel     string       // "c", "a", or "cst" — displayed as mode badge
	Models        []string     // available models (from credentials)
	ModelSwitcher func(string) // called on /model switch
	Truncate      bool         // when true, show full bash output; default false = truncated
	Docs          string       // embedded shell3 documentation, served by shell3_docs tool
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

	switchModel := func(chosen string) tea.Cmd {
		if cfg.ModelSwitcher != nil {
			cfg.ModelSwitcher(chosen)
		}
		parts := strings.SplitN(cfg.StatusLine, " │ ", 2)
		provider := ""
		if len(parts) > 0 {
			provider = parts[0]
		}
		cfg.StatusLine = provider + " │ " + chosen
		newStatus := cfg.StatusLine
		return tea.Batch(
			func() tea.Msg { return tui.AppendMsg(ansiDim + fmt.Sprintf("[model: %s]", chosen) + ansiReset + "\n") },
			func() tea.Msg { return tui.StatusMsg(newStatus) },
		)
	}

	dim := func(s string) tui.AppendMsg {
		return tui.AppendMsg(ansiDim + s + ansiReset + "\n")
	}

	handleSlash := func(input string) tea.Cmd {
		cmd := strings.TrimSpace(strings.ToLower(input))
		// /model must be handled outside the func() tea.Msg wrapper so we can
		// return tea.Batch (multiple messages) or OpenDialogMsg directly.
		if strings.HasPrefix(cmd, "/model") {
			name := strings.TrimSpace(input[6:])
			if name == "" {
				sel := newModelSelect(cfg.Models)
				return tea.Exec(sel, func(err error) tea.Msg {
					if err != nil || sel.chosen == "" {
						return nil
					}
					return tui.RunCmd{Cmd: switchModel(sel.chosen)}
				})
			}
			return switchModel(name)
		}
		return func() tea.Msg {
			switch cmd {
			case "/clear":
				sess.messages = nil
				return dim("[context cleared]")
			case "/prune":
				pruned := pruneLastTurn(sess.messages)
				if len(pruned) == len(sess.messages) {
					return dim("[nothing to prune]")
				}
				sess.messages = pruned
				return dim("[last turn removed from context]")
			case "/usage":
				if lastUsage.TotalTokens == 0 {
					return dim("[no usage data yet]")
				}
				return tui.AppendMsg(fmt.Sprintf(
					"prompt:     %d\ncompletion: %d\ntotal:      %d\n",
					lastUsage.PromptTokens, lastUsage.CompletionTokens, lastUsage.TotalTokens,
				))
			case "/prompt":
				var sb strings.Builder
				fmt.Fprintf(&sb, ansiBold+"system prompt:"+ansiReset+"\n%s\n\n", cfg.Personality.SystemPrompt)
				fmt.Fprintf(&sb, ansiBold+"active tools:"+ansiReset+"\n")
				for _, t := range cfg.Personality.Tools {
					fmt.Fprintf(&sb, "  %-16s %s\n", t.Name, t.Description)
				}
				return tui.AppendMsg(sb.String())
			case "/truncate":
				cfg.Truncate = !cfg.Truncate
				state := "off"
				if cfg.Truncate {
					state = "on"
				}
				return dim(fmt.Sprintf("[full output: %s]", state))
			case "/exit", "/quit":
				return tea.Quit()
			case "/help", "/list", "/", "/h":
				return tui.AppendMsg(slashHelp())
			default:
				return dim(fmt.Sprintf("[unknown command: %s  (type /help to list commands)]", input))
			}
		}
	}

	submitFn := func(input string) tea.Cmd {
		if strings.HasPrefix(input, "/") {
			return handleSlash(input)
		}
		ch := make(chan tea.Msg, 256)
		out := make(chan tea.Msg, 256)
		prevLen := len(sess.messages)
		turnCtx, cancel := context.WithCancel(ctx)
		go func() {
			defer cancel()
			runTurn(turnCtx, cfg, sess, input, ch)
			saveHistory(cfg, sess, sessionID, prevLen)
		}()
		go func() {
			for msg := range ch {
				if done, ok := msg.(tui.TurnDoneMsg); ok {
					lastUsage = done.Usage
				}
				out <- msg
			}
			close(out)
		}()
		return tea.Batch(
			func() tea.Msg { return tui.SetCancelMsg{Cancel: cancel} },
			tui.ReadCh(out),
		)
	}

	model := tui.New("shell3", cfg.StatusLine, cfg.ModeLabel, submitFn)

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

// modelSelect is a minimal tea.ExecCommand that runs an inline arrow-key model picker.
type modelSelect struct {
	models []string
	cursor int
	chosen string
	stdin  io.Reader
	stdout io.Writer
}

func newModelSelect(models []string) *modelSelect { return &modelSelect{models: models} }

func (s *modelSelect) SetStdin(r io.Reader)  { s.stdin = r }
func (s *modelSelect) SetStdout(w io.Writer) { s.stdout = w }
func (s *modelSelect) SetStderr(_ io.Writer) {}

func (s *modelSelect) Run() error {
	p := tea.NewProgram(s, tea.WithInput(s.stdin), tea.WithOutput(s.stdout))
	_, err := p.Run()
	return err
}

func (s *modelSelect) Init() tea.Cmd { return nil }

func (s *modelSelect) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.Code {
		case tea.KeyUp, 'k':
			if s.cursor > 0 {
				s.cursor--
			}
		case tea.KeyDown, 'j':
			if s.cursor < len(s.models)-1 {
				s.cursor++
			}
		case tea.KeyEnter:
			if len(s.models) > 0 {
				s.chosen = s.models[s.cursor]
			}
			return s, tea.Quit
		case tea.KeyEsc, 'q':
			return s, tea.Quit
		}
	}
	return s, nil
}

func (s *modelSelect) View() tea.View {
	var sb strings.Builder
	sb.WriteString("select model  ↑/↓ k/j  enter  esc to cancel\n\n")
	for i, m := range s.models {
		if i == s.cursor {
			sb.WriteString(" > " + m + "\n")
		} else {
			sb.WriteString("   " + m + "\n")
		}
	}
	return tea.View{Content: sb.String()}
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
