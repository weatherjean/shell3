package chat

import (
	"context"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
)

const probeReport = "TASK REPORT — cron nightly (clean)\nstatus: clean\noutput tail: 3 files synced"

func newReportSession(t *testing.T, scripts ...fakellm.Script) (*Session, *fakellm.Client, TurnConfig) {
	t.Helper()
	fake := fakellm.New(append([]fakellm.Script{
		{Events: []llm.StreamEvent{{TextDelta: "Done — moved."}}},
	}, scripts...)...)
	sess, _ := newCollectorSession(SessionOpts{})
	cfg := TurnConfig{LLM: fake, Profile: AgentProfile{SystemPrompt: "sys"}, ToolConfig: ToolConfig{Log: LogOrNoop(nil)}}
	RunTurn(context.Background(), cfg, sess, llm.Message{Role: llm.RoleUser, Content: "move the files"}, nil)
	return sess, fake, cfg
}

func TestReportLandsAtEndOfWakeContext(t *testing.T) {
	sess, fake, cfg := newReportSession(t,
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "NO_REPLY"}}})

	sess.InterjectNotice(probeReport)
	RunTurn(context.Background(), cfg, sess, llm.Message{Role: llm.RoleUser}, nil)

	msgs := fake.CallsSnapshot()[1].Msgs
	last := msgs[len(msgs)-1]
	if !strings.Contains(last.Content, "cron nightly") {
		t.Fatalf("report is not the last thing in context; tail was %s: %q", last.Role, last.Content)
	}
	if last.Role != llm.RoleUser {
		t.Fatalf("report carrier role = %v, want user", last.Role)
	}
	for _, m := range msgs[:len(msgs)-1] {
		if strings.Contains(m.Content, "cron nightly") {
			t.Fatalf("report also grafted onto an earlier message: %s %q", m.Role, m.Content)
		}
	}
}

func TestReportLeavesPersistedTrace(t *testing.T) {
	sess, _, cfg := newReportSession(t,
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "Got it — standing by."}}})

	sess.InterjectNotice(probeReport)
	RunTurn(context.Background(), cfg, sess, llm.Message{Role: llm.RoleUser}, nil)

	var trace string
	for _, m := range sess.messages {
		if strings.Contains(m.Content, "task report") && m.Role == llm.RoleUser {
			trace = m.Content
		}
	}
	if trace == "" {
		t.Fatalf("no persisted trace; history=%+v", sess.messages)
	}
	if !strings.Contains(trace, "cron nightly") {
		t.Fatalf("trace does not identify the report: %q", trace)
	}
	if strings.Contains(trace, "output tail") {
		t.Fatalf("trace persisted the full report body: %q", trace)
	}
}

func TestReportTraceKeepsTranscriptAlternating(t *testing.T) {
	sess, _, cfg := newReportSession(t,
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "Got it — standing by."}}})

	sess.InterjectNotice(probeReport)
	RunTurn(context.Background(), cfg, sess, llm.Message{Role: llm.RoleUser}, nil)

	for i := 1; i < len(sess.messages); i++ {
		if sess.messages[i].Role == llm.RoleAssistant && sess.messages[i-1].Role == llm.RoleAssistant {
			t.Fatalf("two consecutive assistant messages at %d: %+v", i, sess.messages)
		}
	}
}

func TestFollowUpTurnCanSeeWhyItSpoke(t *testing.T) {
	sess, fake, cfg := newReportSession(t,
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "Got it — standing by."}}},
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "a cron report came in."}}})

	sess.InterjectNotice(probeReport)
	RunTurn(context.Background(), cfg, sess, llm.Message{Role: llm.RoleUser}, nil)
	RunTurn(context.Background(), cfg, sess, llm.Message{Role: llm.RoleUser, Content: "why did you send that?"}, nil)

	calls := fake.CallsSnapshot()
	for _, m := range calls[len(calls)-1].Msgs {
		if strings.Contains(m.Content, "cron nightly") {
			return
		}
	}
	t.Fatalf("follow-up turn cannot see the report that caused the reply: %+v", calls[len(calls)-1].Msgs)
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
