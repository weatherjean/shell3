//go:build unix

package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestShell3ControlToolRegisteredAndRoutesActions(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	called := ""
	operation := func(name string) func(context.Context) (ControlResult, error) {
		return func(context.Context) (ControlResult, error) {
			called = name
			return ControlResult{"detail": name}, nil
		}
	}
	b.SetHostControl(HostControl{
		Status: operation("status"), Validate: operation("validate"),
		Reload: operation("reload"), PrepareRestart: operation("restart"),
	})
	sess := decoratedSession(t, b, rt)
	if !hasTool(sess, "shell3") {
		t.Fatal("shell3 should be registered in the schema")
	}

	for _, action := range []string{"status", "validate", "reload"} {
		out, err := b.controlToolHandler(context.Background(), `{"action":"`+action+`"}`)
		if err != nil || called != action {
			t.Fatalf("%s = (%q, %v), called %q", action, out, err, called)
		}
		var got map[string]any
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("%s result JSON: %v", action, err)
		}
		if got["ok"] != true || got["action"] != action || got["detail"] != action ||
			got["tool"] != "shell3" || got["interface_version"] != float64(1) {
			t.Fatalf("%s result = %#v", action, got)
		}
	}

	out, err := b.controlToolHandler(context.Background(), `{"action":"nope"}`)
	if err == nil || !strings.HasPrefix(err.Error(), "action must be") {
		t.Fatalf("invalid action = (%q, %v)", out, err)
	}
}

func TestDeferredRestartSignalsOnlyAfterReplyAndDrain(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	c := tconv(b)

	b.mu.Lock()
	b.activeTurns = 2
	b.mu.Unlock()
	if already := b.armRestart(); already {
		t.Fatal("first restart request reported already pending")
	}

	// One other room is still active, so delivering this reply cannot yet let
	// the host exit.
	c.mu.Lock()
	c.turnActive = true
	c.mu.Unlock()
	c.finishPostedTurn(context.Background(), nil, "7", "restart queued", func() {})
	select {
	case <-b.RestartReady():
		t.Fatal("restart became ready while another turn was active")
	default:
	}
	if reply, ok := fc.lastReply(); !ok || !strings.Contains(reply.text, "restart queued") {
		t.Fatalf("final reply was not delivered before drain: %+v, %v", reply, ok)
	}

	// The other room's successful delivery releases the final active slot.
	b.freeTurn()
	b.finishRestartDrain(nil)
	select {
	case <-b.RestartReady():
	case <-time.After(time.Second):
		t.Fatal("restart never became ready")
	}
}

func TestDeferredRestartCancelsOnReplyDeliveryFailure(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	b.armRestart()
	b.finishRestartDrain(errors.New("transport down"))
	if b.RestartPending() {
		t.Fatal("failed delivery left the restart gate armed")
	}
	select {
	case <-b.RestartReady():
		t.Fatal("failed delivery allowed restart")
	default:
	}
}

func TestRestartActionArmsOnlyAfterSuccessfulPreparation(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	b.SetHostControl(HostControl{PrepareRestart: func(context.Context) (ControlResult, error) {
		return ControlResult{"ok": false, "reason": "invalid config"}, nil
	}})
	if _, err := b.controlToolHandler(context.Background(), `{"action":"restart"}`); err != nil {
		t.Fatal(err)
	}
	if b.RestartPending() {
		t.Fatal("rejected restart preparation armed the gate")
	}

	b.SetHostControl(HostControl{PrepareRestart: func(context.Context) (ControlResult, error) {
		return ControlResult{"ok": true}, nil
	}})
	out, err := b.controlToolHandler(context.Background(), `{"action":"restart"}`)
	if err != nil || !b.RestartPending() || !strings.Contains(out, "queued_after_active_replies") {
		t.Fatalf("restart = (%q, %v), pending=%v", out, err, b.RestartPending())
	}
}

func TestRestartGateRejectsNewTurnsWithoutLosingThemSilently(t *testing.T) {
	fc := newFakeClient()
	rt := storeRuntime(t, "must not run")
	b := newBot(t, fc, rt)
	b.armRestart()

	b.handleMsg(context.Background(), Msg{ChatID: "42", SenderID: 42, ID: "9", Text: "new work"})
	fc.mu.Lock()
	gotNotice := len(fc.html) > 0 && strings.Contains(fc.html[len(fc.html)-1], "please resend")
	fc.mu.Unlock()
	if !gotNotice {
		t.Fatal("restart gate did not tell the user to resend")
	}
	c := tconv(b)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.main != nil || c.turnActive || len(c.pendingMessages) != 0 {
		t.Fatalf("restart gate started or queued a turn: %+v", c)
	}
}

func TestRestartPreservesAcceptedDebounceBurstForDrain(t *testing.T) {
	fc := newFakeClient()
	rt := storeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	c := tconv(b)
	c.mu.Lock()
	c.burst = []inboundMessage{{m: Msg{ID: "10"}, text: "accepted before restart"}}
	c.burstTimer = time.AfterFunc(time.Hour, func() {})
	c.mu.Unlock()

	b.armRestart()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.burst) != 0 || c.burstTimer != nil || len(c.pendingMessages) != 1 || c.pendingMessages[0].text != "accepted before restart" {
		t.Fatalf("debounce burst was not preserved: burst=%d pending=%+v", len(c.burst), c.pendingMessages)
	}
}
