package chat

import (
	"context"
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
)

func TestNewTurnConfigReviewTrustUsesSessionProvenance(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		want bool
	}{
		{name: "interactive root", cfg: Config{}, want: true},
		{name: "headless", cfg: Config{Headless: true}, want: false},
		{name: "subagent", cfg: Config{ParentID: "parent"}, want: false},
		{name: "cron", cfg: Config{CronJob: "nightly"}, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := NewTurnConfig(tc.cfg, nil).TrustedUserContext
			if got != tc.want {
				t.Fatalf("TrustedUserContext=%v, want %v", got, tc.want)
			}
		})
	}
}

func TestNewTurnConfigMarksOnlySubagentsWithAParent(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  Config
		want bool
	}{
		{name: "interactive root", cfg: Config{}, want: false},
		{name: "ownerless headless root", cfg: Config{Headless: true}, want: false},
		{name: "cron root", cfg: Config{Headless: true, CronJob: "nightly"}, want: false},
		{name: "subagent", cfg: Config{Headless: true, ParentID: "parent"}, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := NewTurnConfig(tc.cfg, nil).HasParentAgent; got != tc.want {
				t.Fatalf("HasParentAgent=%v, want %v", got, tc.want)
			}
		})
	}
}

func TestBashReviewReceivesTranscriptThroughCurrentToolCall(t *testing.T) {
	var reviewed ToolReviewRequest
	cfg := TurnConfig{
		ToolConfig: ToolConfig{
			WorkDir:            t.TempDir(),
			TrustedUserContext: true,
			RunToolCall:        reviewVerdict("publishing"),
			ReviewToolCall: func(_ context.Context, req ToolReviewRequest) (bool, string) {
				reviewed = req
				return true, ""
			},
			Log: LogOrNoop(nil),
		},
		Handlers: map[string]ToolHandler{"bash": BashHandler{}},
	}
	call := llm.ToolCall{ID: "1", Name: "bash", RawArgs: `{"command":"echo reviewed"}`}
	messages := []llm.Message{
		{Role: llm.RoleUser, Content: "run the reviewed command"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{call}},
	}
	sess := NewSession(SessionOpts{})
	if _, err := executeToolCalls(context.Background(), cfg, sess, []llm.ToolCall{call}, nil, messages); err != nil {
		t.Fatal(err)
	}
	if reviewed.Command != "echo reviewed" || !reviewed.TrustedUserContext {
		t.Fatalf("review request=%+v", reviewed)
	}
	if len(reviewed.Messages) != len(messages) {
		t.Fatalf("review received %d messages, want %d", len(reviewed.Messages), len(messages))
	}
	last := reviewed.Messages[len(reviewed.Messages)-1]
	if len(last.ToolCalls) != 1 || last.ToolCalls[0].Name != "bash" {
		t.Fatalf("current tool call missing from review transcript: %+v", last)
	}
}

func TestRunTurnMarksOnlyInteractiveRootInputAsOperatorContent(t *testing.T) {
	for _, tc := range []struct {
		name    string
		trusted bool
		want    string
	}{
		{name: "interactive root", trusted: true, want: "approve exact action"},
		{name: "generated or delegated", trusted: false, want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sess := NewSession(SessionOpts{})
			fake := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "done"}, {Done: true}}})
			RunTurn(context.Background(), TurnConfig{
				ToolConfig: ToolConfig{TrustedUserContext: tc.trusted, Log: LogOrNoop(nil)},
				LLM:        fake,
			}, sess, llm.Message{Role: llm.RoleUser, Content: "approve exact action"}, nil)
			messages := sess.Messages()
			if len(messages) < 1 || messages[0].OperatorContent != tc.want {
				t.Fatalf("messages=%+v, want OperatorContent=%q", messages, tc.want)
			}
		})
	}
}
