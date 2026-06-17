//go:build unix

package telegram

import (
	"context"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
	"github.com/weatherjean/shell3/pkg/shell3"
	"github.com/weatherjean/shell3/pkg/shell3/shell3test"
)

// paramClient is a fakellm with a tunable parameter surface (ParamDescriber +
// ParamSetter), so the /set success path — which calls sess.SetParam — can be
// exercised. The plain fakellm exposes no params, leaving only the error paths
// reachable.
type paramClient struct {
	*fakellm.Client
	specs []llm.ParamSpec
}

func (p *paramClient) ParamSpecs() []llm.ParamSpec { return p.specs }
func (p *paramClient) SetParams(llm.RequestParams) {}

func newParamBot(t *testing.T) (*fakeClient, *Bot) {
	t.Helper()
	client := &paramClient{
		Client: fakellm.New(),
		specs:  []llm.ParamSpec{{Name: "reasoning_effort", Enum: []string{"low", "high"}, Default: "low"}},
	}
	rt := shell3test.NewRuntimeForTestClient(t, client)
	sess, err := rt.Session(shell3.SessionOpts{Name: "telegram", Agent: "code"})
	if err != nil {
		t.Fatal(err)
	}
	fc := newFakeClient()
	return fc, NewBot(fc, rt, sess, 42)
}

// TestCommand_SetMutatesParam pins the success branch: "/set <name> <value>"
// pushes the value through sess.SetParam and echoes it back. The success echo
// (not "set failed") proves SetParam accepted the value; the snapshot confirms
// the mutation took.
func TestCommand_SetMutatesParam(t *testing.T) {
	fc, b := newParamBot(t)
	b.handleCommand(context.Background(), Msg{ChatID: 42, Text: "/set reasoning_effort high"})
	if reply := strings.Join(fc.sentTexts(), "\n"); !strings.Contains(reply, "reasoning_effort = high") {
		t.Fatalf("expected success echo of the new value, got %v", fc.sentTexts())
	}
	if got := paramValue(b, "reasoning_effort"); got != "high" {
		t.Fatalf("snapshot param = %q, want high", got)
	}
}

// TestCommand_SetUsageOnMissingValue pins the arg-parse branch: a name with no
// value is a usage error, not a (mis)mutation.
func TestCommand_SetUsageOnMissingValue(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := NewBot(fc, rt, sess, 42)
	b.handleCommand(context.Background(), Msg{ChatID: 42, Text: "/set onlyname"})
	if !strings.Contains(strings.Join(fc.sentTexts(), "\n"), "usage: /set") {
		t.Fatalf("expected usage hint for value-less /set, got %v", fc.sentTexts())
	}
}

// TestCommand_SetFailsOnUnknownParam pins the error branch: SetParam's rejection
// surfaces as a "set failed" reply rather than a silent success.
func TestCommand_SetFailsOnUnknownParam(t *testing.T) {
	fc, b := newParamBot(t)
	b.handleCommand(context.Background(), Msg{ChatID: 42, Text: "/set nope 1"})
	if !strings.Contains(strings.Join(fc.sentTexts(), "\n"), "set failed") {
		t.Fatalf("expected set-failed reply for unknown param, got %v", fc.sentTexts())
	}
}

func paramValue(b *Bot, name string) string {
	for _, p := range b.sess.Snapshot().Params {
		if p.Name == name {
			return p.Value
		}
	}
	return ""
}
