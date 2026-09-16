//go:build unix

package telegram

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
	"github.com/weatherjean/shell3/internal/shell3"
	"github.com/weatherjean/shell3/internal/shell3/shell3test"
)

func TestControlOutcomeReachesNextModelRound(t *testing.T) {
	for _, mode := range []string{"success", "rejected", "error", "persistence-error"} {
		t.Run(mode, func(t *testing.T) {
			fake := fakellm.New(
				fakellm.Script{Events: []llm.StreamEvent{{ToolCall: &llm.ToolCall{ID: "a", Name: "shell3", RawArgs: `{"action":"reload"}`}}}},
				fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "done"}}},
			)
			rt := shell3test.NewRuntimeForTestConfig(t, func(shell3.SessionOpts) (chat.Config, error) {
				return chat.Config{LLM: fake, WorkDir: t.TempDir()}, nil
			})
			b := newBot(t, newFakeClient(), rt)
			state := "before action"
			b.SetHostControl(HostControl{
				Context: func() string { return state },
				Reload: func(context.Context) (ControlResult, error) {
					if mode == "error" {
						return nil, errors.New("invalid config")
					}
					return ControlResult{"ok": mode != "rejected"}, nil
				},
				Record: func(action, out string, err error) error {
					if action != "reload" {
						t.Errorf("action=%s", action)
					}
					if mode == "error" && err == nil {
						t.Error("lost error")
					}
					if mode == "rejected" && !strings.Contains(out, `"ok":false`) {
						t.Error("lost rejection")
					}
					state = "receipt " + mode
					if mode == "persistence-error" {
						return errors.New("disk full")
					}
					return nil
				},
			})
			sess := decoratedSession(t, b, rt)
			for range sess.Send(t.Context(), "reload") {
			}
			calls := fake.CallsSnapshot()
			if len(calls) != 2 || !strings.Contains(calls[1].Msgs[0].Content, "receipt "+mode) {
				t.Fatalf("missing authoritative result: %+v", calls)
			}
			if mode == "persistence-error" {
				found := false
				for _, m := range calls[1].Msgs {
					found = found || strings.Contains(m.Content, "feedback persistence failed")
				}
				if !found {
					t.Fatal("persistence failure hidden")
				}
			}
		})
	}
}

func TestRestartDeliveryFailureRecordsCancellation(t *testing.T) {
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, newFakeClient(), rt)
	recorded := ""
	b.SetHostControl(HostControl{Record: func(action, out string, err error) error { recorded = out; return nil }})
	b.armRestart()
	b.finishRestartDrain(errors.New("delivery failed"))
	if b.RestartPending() || !strings.Contains(recorded, "cancelled_reply_delivery_failed") {
		t.Fatalf("receipt=%s", recorded)
	}
}
