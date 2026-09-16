//go:build unix

package telegram

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
	"github.com/weatherjean/shell3/internal/shell3"
	"github.com/weatherjean/shell3/internal/shell3/shell3test"
)

func polledConversation(t *testing.T) (*Bot, *conversation, *fakellm.Client, *fakeClient) {
	return polledCommandConversation(t, "sleep 60")
}

func polledCommandConversation(t *testing.T, command string) (*Bot, *conversation, *fakellm.Client, *fakeClient) {
	t.Helper()
	fake := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{{ToolCall: &llm.ToolCall{ID: "launch", Name: "bash_bg", RawArgs: `{"command":"` + command + `","poll_in":"2m"}`}}}},
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "Checking in two minutes."}}},
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "Still working."}}},
	)
	rt := shell3test.NewRuntimeForTestConfig(t, func(shell3.SessionOpts) (chat.Config, error) {
		return chat.Config{LLM: fake, WorkDir: t.TempDir(), Profile: chat.AgentProfile{Tools: []llm.ToolDefinition{{Name: "bash_bg"}}}}, nil
	})
	fc := newFakeClient()
	b := newBot(t, fc, rt)
	rt.SetSessionDecorator(b.DecorateOrchestratorSession)
	c := tconv(b)
	c.setAnchor("request-1")
	sess, err := c.mainSession()
	if err != nil {
		t.Fatal(err)
	}
	for ev := range sess.Send(t.Context(), "start the task") {
		if ev.Kind == shell3.Error || ev.ToolError {
			t.Fatalf("launch failed: %+v", ev)
		}
	}
	if command == "sleep 60" && sess.RunningJobs() != 1 {
		t.Fatal("no background command")
	}
	return b, c, fake, fc
}

func TestJobPollAfterCompletionWhileRoomBusy(t *testing.T) {
	_, c, fake, fc := polledCommandConversation(t, "exit 0")
	waitFor(t, func() bool { return c.main.RunningJobs() == 0 })
	c.mu.Lock()
	c.turnActive = true
	c.mu.Unlock()
	due := time.Now().Add(3 * time.Minute)
	if c.startJobPoll(t.Context(), due) {
		t.Fatal("overlapped user turn")
	}
	c.mu.Lock()
	c.turnActive = false
	c.mu.Unlock()
	if c.startJobPoll(t.Context(), time.Now()) {
		t.Fatal("completion accelerated deadline")
	}
	if !c.startJobPoll(t.Context(), due) {
		t.Fatal("completion lost scheduled follow-up")
	}
	waitPollTurn(t, c)
	if c.startJobPoll(t.Context(), due) {
		t.Fatal("follow-up repeated")
	}
	if fake.CallCount() != 3 {
		t.Fatalf("model calls = %d", fake.CallCount())
	}
	if reply, ok := fc.lastReply(); !ok || reply.chatID != "42" {
		t.Fatalf("missing follow-up: %+v", reply)
	}
}

func waitPollTurn(t *testing.T, c *conversation) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		active := c.turnActive
		c.mu.Unlock()
		if !active {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("poll turn did not finish")
}

func TestJobPollQueuesBehindBusyTurnAndCap(t *testing.T) {
	b, c, fake, fc := polledConversation(t)
	due := time.Now().Add(3 * time.Minute)
	if c.startJobPoll(t.Context(), time.Now()) {
		t.Fatal("started early")
	}
	c.mu.Lock()
	c.turnActive = true
	c.mu.Unlock()
	if c.startJobPoll(t.Context(), due) {
		t.Fatal("overlapped active turn")
	}
	c.mu.Lock()
	c.turnActive = false
	c.mu.Unlock()
	b.mu.Lock()
	b.activeTurns = b.maxTurns
	b.mu.Unlock()
	if c.startJobPoll(t.Context(), due) {
		t.Fatal("exceeded global cap")
	}
	b.mu.Lock()
	b.activeTurns = 0
	b.mu.Unlock()
	if !c.startJobPoll(t.Context(), due) {
		t.Fatal("lost due check while busy")
	}
	waitPollTurn(t, c)
	if c.startJobPoll(t.Context(), due) {
		t.Fatal("one-shot check repeated")
	}
	calls := fake.CallsSnapshot()
	if len(calls) != 3 || !strings.Contains(calls[2].Msgs[len(calls[2].Msgs)-1].Content, "scheduled progress check") {
		t.Fatalf("calls = %+v", calls)
	}
	if err := llm.ValidateToolOrder(calls[2].Msgs); err != nil {
		t.Fatal(err)
	}
	reply, ok := fc.lastReply()
	if !ok || reply.chatID != "42" || !strings.Contains(reply.text, "Still working") {
		t.Fatalf("reply = %+v", reply)
	}
}

func TestJobPollRespectsStopNewRestartAndUserPriority(t *testing.T) {
	for _, mode := range []string{"/stop", "/superstop", "/new", "restart", "burst", "queue", "cancelled-context"} {
		t.Run(mode, func(t *testing.T) {
			b, c, fake, _ := polledConversation(t)
			ctx := t.Context()
			switch mode {
			case "restart":
				b.armRestart()
			case "burst":
				c.burst = []inboundMessage{{text: "user first"}}
			case "queue":
				c.pendingMessages = []inboundMessage{{text: "user first"}}
			case "cancelled-context":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			default:
				c.handleCommand(ctx, Msg{Text: mode})
			}
			if c.startJobPoll(ctx, time.Now().Add(time.Hour)) {
				t.Fatalf("started after %s", mode)
			}
			if fake.CallCount() != 2 {
				t.Fatal("unexpected model call")
			}
		})
	}
}
