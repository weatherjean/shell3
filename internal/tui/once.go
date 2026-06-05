package tui

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/weatherjean/shell3/internal/chat"
)

// RunOnce executes a single turn and prints output to stdout. No TUI.
//
// The chat.Session runs in synchronous-sink mode: events are delivered inline
// by render as the turn streams, so the whole function is linear — no events
// goroutine and no channel teardown. status is written by render (on this
// goroutine, during Run) and read after Run returns, so there is no race.
func RunOnce(ctx context.Context, cfg chat.Config, input string) error {
	sink, sinkCleanup, err := chat.OpenSink(cfg.OutPath)
	if err != nil {
		return err
	}
	defer sinkCleanup()
	if sink != nil {
		_, model := chat.SplitStatus(cfg.StatusLine)
		sink.WriteStart(input, cfg.ModeLabel, model, cfg.OutPath, cfg.Headless)
	}

	status := "ok"
	render := func(ev chat.Event) {
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

	sess := chat.NewSession(chat.SessionOpts{Sink: render})
	tc := chat.NewTurnConfig(cfg, chat.NewHandlers(cfg), func(ctx context.Context, cmd, workdir string) string {
		return "error: interactive TTY not available in headless mode"
	})
	sess.Run(ctx, tc, input)

	if sink != nil {
		sink.WriteEnd(status)
	}
	if status == "error" {
		return fmt.Errorf("turn ended with error")
	}
	return nil
}
