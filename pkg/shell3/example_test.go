package shell3_test

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/weatherjean/shell3/pkg/shell3"
)

// ExampleRun shows the one-shot form: load the config, run a single prompt,
// and stream the turn's events. The channel closes when the turn ends and the
// session is torn down automatically.
func ExampleRun() {
	events, err := shell3.Run(context.Background(), shell3.Spec{
		Prompt: "summarize the diff in this repo",
		// ConfigPath "" resolves ./shell3.lua, then ~/.shell3/shell3.lua.
	})
	if err != nil {
		log.Fatal(err) // startup failure: no config, bad config, unknown agent
	}
	for ev := range events {
		switch ev.Kind {
		case shell3.Token:
			fmt.Print(ev.Text)
		case shell3.ToolCall:
			fmt.Printf("\n[%s %s]\n", ev.ToolName, ev.ToolInput)
		case shell3.Error:
			log.Println("turn failed:", ev.Err)
		case shell3.Done:
			fmt.Printf("\n(%d tokens)\n", ev.TotalTokens)
		}
	}
}

// ExampleStart shows a persistent multi-turn session — the embedding
// equivalent of an open TUI. Drain each Send channel to completion before the
// next Send/Clear/SwitchAgent; an overlapping call reports shell3.ErrBusy.
func ExampleStart() {
	sess, err := shell3.Start(context.Background(), shell3.Spec{})
	if err != nil {
		log.Fatal(err)
	}
	defer sess.Close()

	for ev := range sess.Send(context.Background(), "what does this repo do?") {
		if ev.Kind == shell3.Token {
			fmt.Print(ev.Text)
		}
	}

	// Switch agents between turns; history is kept.
	if err := sess.SwitchAgent("plan"); err != nil && !errors.Is(err, shell3.ErrBusy) {
		log.Println(err)
	}
	for ev := range sess.Send(context.Background(), "propose a refactor plan") {
		if ev.Kind == shell3.Token {
			fmt.Print(ev.Text)
		}
	}
}

// ExampleSession_Snapshot shows introspection: the active agent, model, tools,
// and tunable parameters, as the TUI's status line and /info render them.
func ExampleSession_Snapshot() {
	sess, err := shell3.Start(context.Background(), shell3.Spec{})
	if err != nil {
		log.Fatal(err)
	}
	defer sess.Close()

	snap := sess.Snapshot()
	fmt.Println(snap.Agent, snap.Model, snap.ContextWindow)
	for _, tool := range snap.Tools {
		fmt.Println(tool.Name, "—", tool.Description)
	}
}
