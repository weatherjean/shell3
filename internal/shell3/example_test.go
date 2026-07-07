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

// ExampleSession_Snapshot shows introspection: the active agent, model, skills,
// and tunable parameters, as the TUI's status line and /info render them.
func ExampleSession_Snapshot() {
	sess, err := shell3.Start(context.Background(), shell3.Spec{})
	if err != nil {
		log.Fatal(err)
	}
	defer sess.Close()

	snap := sess.Snapshot()
	fmt.Println(snap.Agent, snap.Model, snap.ContextWindow)
	for _, p := range snap.Params {
		fmt.Println(p.Name, "=", p.Value)
	}
}

// ExampleNewRuntime shows the always-on host shape: one Runtime rooted at an
// agent home, multiple named sessions (e.g. one per client connection), each with
// its own history, agent, and optional workdir.
func ExampleNewRuntime() {
	rt, err := shell3.NewRuntime(context.Background(), shell3.RuntimeSpec{WorkDir: "/home/me/assistant"})
	if err != nil {
		panic(err)
	}
	defer rt.Close()

	chat1, err := rt.Session(shell3.SessionOpts{Name: "tg:1234", Headless: true})
	if err != nil {
		panic(err) // panic (not log.Fatal) so the deferred rt.Close still runs
	}
	for ev := range chat1.Send(context.Background(), "good morning") {
		if ev.Kind == shell3.Token {
			fmt.Print(ev.Text)
		}
	}

	// A second session rooted in a repo behaves like a normal coding session.
	coder, err := rt.Session(shell3.SessionOpts{Name: "job:fix-ci", WorkDir: "/home/me/src/myrepo", Headless: true})
	if err != nil {
		panic(err)
	}

	// Mid-turn steering: Interject can be called from any goroutine while
	// Send is running. The text is queued and injected at the next round
	// boundary as a system reminder ("user interjected …"). While idle it
	// queues and is delivered at the start of the next turn.
	go func() {
		coder.Interject("stop after 3 steps and report status")
	}()
	for range coder.Send(context.Background(), "make the tests pass") {
	}
}

// ExampleRuntime_Events shows the flagship always-on loop: an idle session
// whose inbox gains an item (a subagent finishing, an Interject) emits a Wake
// on the runtime bus; the host reacts with RunQueued, which drains the queued
// inbox as a fresh turn (and no-ops when there is nothing to do).
func ExampleRuntime_Events() {
	rt, err := shell3.NewRuntime(context.Background(), shell3.RuntimeSpec{})
	if err != nil {
		panic(err)
	}
	defer rt.Close()

	sessions := map[string]*shell3.Session{}
	s, err := rt.Session(shell3.SessionOpts{Name: "main", Headless: true})
	if err != nil {
		panic(err)
	}
	sessions[s.Name()] = s

	for ev := range rt.Events() {
		if ev.Kind != shell3.Wake {
			continue
		}
		if s := sessions[ev.Session]; s != nil {
			for e := range s.RunQueued(context.Background()) {
				if e.Kind == shell3.Token {
					fmt.Print(e.Text)
				}
			}
		}
	}
}

// ExampleSession_RegisterHostTool shows the host-extensibility hook: a
// Go-implemented tool registered before the first turn, dispatched by name
// alongside the built-ins and Lua custom tools.
func ExampleSession_RegisterHostTool() {
	sess, err := shell3.Start(context.Background(), shell3.Spec{})
	if err != nil {
		panic(err)
	}
	defer sess.Close()

	err = sess.RegisterHostTool(shell3.HostTool{
		Name:        "get_weather",
		Description: "Current weather for a city.",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{"city": map[string]any{"type": "string"}},
			"required":   []string{"city"},
		},
		Handler: func(ctx context.Context, argsJSON string) (string, error) {
			return `{"temp_c": 21, "sky": "clear"}`, nil
		},
	})
	if err != nil {
		panic(err)
	}
	for range sess.Send(context.Background(), "what's the weather in Oslo?") {
	}
}

// ExampleSession_SetSafetyOff shows the host-side switch behind a front-end's
// disable_safety command: while on, on_tool_call ask verdicts run without
// prompting a human (block verdicts still block).
func ExampleSession_SetSafetyOff() {
	sess, err := shell3.Start(context.Background(), shell3.Spec{})
	if err != nil {
		panic(err)
	}
	defer sess.Close()

	sess.SetSafetyOff(true) // e.g. the user toggled :disable_safety
	for range sess.Send(context.Background(), "clean the build directory") {
	}
	sess.SetSafetyOff(false)
}

// ExampleSession_SendParts shows inbound media: a turn carrying an image the
// model can see. A Part sets exactly one of Path (extension-routed) or Data
// (MIME-routed); invalid parts reject the whole turn with an Error event.
func ExampleSession_SendParts() {
	sess, err := shell3.Start(context.Background(), shell3.Spec{})
	if err != nil {
		panic(err)
	}
	defer sess.Close()

	parts := []shell3.Part{{Kind: shell3.PartImage, Path: "screenshot.png"}}
	for ev := range sess.SendParts(context.Background(), "what's wrong in this screenshot?", parts) {
		if ev.Kind == shell3.Token {
			fmt.Print(ev.Text)
		}
	}
}
