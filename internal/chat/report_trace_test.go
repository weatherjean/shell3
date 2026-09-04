package chat

import (
	"context"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
)

const probeHostNotice = "[superstop] stopped background commands\ndetail: 3 commands stopped"

func newHostNoticeSession(t *testing.T, scripts ...fakellm.Script) (*Session, *fakellm.Client, TurnConfig) {
	t.Helper()
	fake := fakellm.New(append([]fakellm.Script{
		{Events: []llm.StreamEvent{{TextDelta: "Done — moved."}}},
	}, scripts...)...)
	sess, _ := newCollectorSession(SessionOpts{})
	cfg := TurnConfig{LLM: fake, Profile: AgentProfile{SystemPrompt: "sys"}, ToolConfig: ToolConfig{Log: LogOrNoop(nil)}}
	RunTurn(context.Background(), cfg, sess, llm.Message{Role: llm.RoleUser, Content: "move the files"}, nil)
	return sess, fake, cfg
}

func TestHostNoticeLandsAtEndOfWakeContext(t *testing.T) {
	sess, fake, cfg := newHostNoticeSession(t,
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "NO_REPLY"}}})

	sess.InterjectHostNotice(probeHostNotice)
	RunTurn(context.Background(), cfg, sess, llm.Message{Role: llm.RoleUser}, nil)

	msgs := fake.CallsSnapshot()[1].Msgs
	last := msgs[len(msgs)-1]
	if !strings.Contains(last.Content, "superstop") {
		t.Fatalf("host notice is not the last thing in context; tail was %s: %q", last.Role, last.Content)
	}
	if last.Role != llm.RoleUser {
		t.Fatalf("host-notice carrier role = %v, want user", last.Role)
	}
	for _, m := range msgs[:len(msgs)-1] {
		if strings.Contains(m.Content, "superstop") {
			t.Fatalf("host notice also grafted onto an earlier message: %s %q", m.Role, m.Content)
		}
	}
}

func TestHostNoticeLeavesPersistedTrace(t *testing.T) {
	sess, _, cfg := newHostNoticeSession(t,
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "Got it — standing by."}}})

	sess.InterjectHostNotice(probeHostNotice)
	RunTurn(context.Background(), cfg, sess, llm.Message{Role: llm.RoleUser}, nil)

	var trace string
	for _, m := range sess.messages {
		if strings.Contains(m.Content, "host notice") && m.Role == llm.RoleUser {
			trace = m.Content
		}
	}
	if trace == "" {
		t.Fatalf("no persisted trace; history=%+v", sess.messages)
	}
	if !strings.Contains(trace, "superstop") {
		t.Fatalf("trace does not identify the host notice: %q", trace)
	}
	if strings.Contains(trace, "detail:") {
		t.Fatalf("trace persisted the full host notice body: %q", trace)
	}
}

func TestHostNoticeTraceKeepsTranscriptAlternating(t *testing.T) {
	sess, _, cfg := newHostNoticeSession(t,
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "Got it — standing by."}}})

	sess.InterjectHostNotice(probeHostNotice)
	RunTurn(context.Background(), cfg, sess, llm.Message{Role: llm.RoleUser}, nil)

	for i := 1; i < len(sess.messages); i++ {
		if sess.messages[i].Role == llm.RoleAssistant && sess.messages[i-1].Role == llm.RoleAssistant {
			t.Fatalf("two consecutive assistant messages at %d: %+v", i, sess.messages)
		}
	}
}

func TestFollowUpTurnCanSeeWhyItSpoke(t *testing.T) {
	sess, fake, cfg := newHostNoticeSession(t,
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "Got it — standing by."}}},
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "a host notice came in."}}})

	sess.InterjectHostNotice(probeHostNotice)
	RunTurn(context.Background(), cfg, sess, llm.Message{Role: llm.RoleUser}, nil)
	RunTurn(context.Background(), cfg, sess, llm.Message{Role: llm.RoleUser, Content: "why did you send that?"}, nil)

	calls := fake.CallsSnapshot()
	for _, m := range calls[len(calls)-1].Msgs {
		if strings.Contains(m.Content, "superstop") {
			return
		}
	}
	t.Fatalf("follow-up turn cannot see the host notice that caused the reply: %+v", calls[len(calls)-1].Msgs)
}

func TestReminderStillRidesCurrentUserMessage(t *testing.T) {
	fake := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "ok"}}})
	sess, _ := newCollectorSession(SessionOpts{})
	cfg := TurnConfig{LLM: fake, Profile: AgentProfile{SystemPrompt: "sys"}, ToolConfig: ToolConfig{Log: LogOrNoop(nil)}}
	sess.Interject("also check the logs")
	RunTurn(context.Background(), cfg, sess, llm.Message{Role: llm.RoleUser, Content: "deploy it"}, nil)

	msgs := fake.CallsSnapshot()[0].Msgs
	last := msgs[len(msgs)-1]
	if last.Role != llm.RoleUser || !strings.Contains(last.Content, "deploy it") {
		t.Fatalf("tail message = %s %q", last.Role, last.Content)
	}
	if !strings.Contains(last.Content, "also check the logs") {
		t.Fatalf("steer did not ride the user's current message: %q", last.Content)
	}
}
