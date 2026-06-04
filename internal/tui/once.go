package tui

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/weatherjean/shell3/internal/chat"
)

// RunOnce executes a single turn and prints output to stdout. No TUI.
func RunOnce(ctx context.Context, cfg chat.Config, input string) error {
	sess := chat.NewSession(chat.SessionOpts{BufSize: 256})

	sink, sinkCleanup, err := chat.OpenSink(cfg.OutPath)
	if err != nil {
		return err
	}
	defer sinkCleanup()
	if sink != nil {
		_, model := chat.SplitStatus(cfg.StatusLine)
		sink.WriteStart(input, cfg.ModeLabel, model, cfg.OutPath, cfg.Headless)
	}

	tc := chat.TurnConfig{
		LLM:             cfg.LLM,
		Personality:     cfg.Personality,
		StatusLine:      cfg.StatusLine,
		WorkDir:         cfg.WorkDir,
		Store:           cfg.Store,
		Handlers:        chat.NewHandlers(cfg),
		Log:             chat.LogOrNoop(cfg.Log),
		Headless:        cfg.Headless,
		CustomTool:      cfg.CustomTool,
		CustomToolNames: cfg.CustomToolNames,
		ToolGuard:       cfg.ToolGuard,
		ShellInteractive: func(ctx context.Context, cmd, workdir string) string {
			return "error: interactive TTY not available in headless mode"
		},
	}
	go func() {
		sess.Run(ctx, tc, input)
		sess.CloseEvents()
	}()

	status := "ok"
	for ev := range sess.Events() {
		if sink != nil {
			sink.WriteChatEvent(ev)
		}
		switch ev.Kind {
		case chat.EventAssistantToken:
			fmt.Print(ev.Text)
		case chat.EventToolResult:
			// Show tool body on stdout, trimmed and followed by a blank line
			// for separation. Headers are skipped here — RunOnce is for
			// pipeline use where minimal output is preferable.
			fmt.Println()
			fmt.Print(strings.TrimRight(ev.ToolOutput, "\n"))
			fmt.Println()
		case chat.EventRetry:
			fmt.Fprintln(os.Stderr, "retry:", ev.Text)
		case chat.EventError:
			fmt.Fprintln(os.Stderr, "error:", ev.Text)
			status = "error"
		case chat.EventTurnDone:
			fmt.Println()
		}
	}
	if sink != nil {
		sink.WriteEnd(status)
	}
	if status == "error" {
		return fmt.Errorf("turn ended with error")
	}
	return nil
}
