//go:build unix

package telegram

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/weatherjean/shell3/pkg/shell3"
)

func TestApprover_ApproveResolvesTrue(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := NewBot(fc, rt, sess, 42, "")
	b.approvalTimeout = time.Minute

	done := make(chan bool, 1)
	go func() {
		done <- b.approve(context.Background(), shell3.ApprovalRequest{Tool: "bash", RawArgs: `{"cmd":"rm x"}`})
	}()

	// the approver should have sent a prompt with two buttons
	waitFor(t, func() bool { return len(fc.lastButtons()) == 2 })
	id := buttonID(fc.lastButtons()[0].Data) // "ap:<id>:y" → "<id>"
	b.handleCallback(context.Background(), &Callback{ID: "cq1", Data: "ap:" + id + ":y", MsgID: 1})

	select {
	case got := <-done:
		if !got {
			t.Fatal("expected approve=true")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("approver did not resolve")
	}
}

func TestApprover_TimeoutDenies(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := NewBot(fc, rt, sess, 42, "")
	b.approvalTimeout = 50 * time.Millisecond

	got := b.approve(context.Background(), shell3.ApprovalRequest{Tool: "bash"})
	if got {
		t.Fatal("expected timeout → deny")
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("waitFor: condition not met within 1s")
}

func buttonID(data string) string {
	parts := strings.Split(data, ":")
	if len(parts) >= 2 {
		return parts[1]
	}
	return ""
}
