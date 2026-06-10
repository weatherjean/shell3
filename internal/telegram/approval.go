//go:build unix

package telegram

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/weatherjean/shell3/pkg/shell3"
)

type approvalRegistry struct {
	mu      sync.Mutex
	pending map[string]chan bool
	seq     int
}

func newApprovalRegistry() *approvalRegistry {
	return &approvalRegistry{pending: map[string]chan bool{}}
}

func (r *approvalRegistry) add() (id string, ch chan bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	id = fmt.Sprintf("%d", r.seq)
	ch = make(chan bool, 1)
	r.pending[id] = ch
	return id, ch
}

func (r *approvalRegistry) resolve(id string, ok bool) {
	r.mu.Lock()
	ch := r.pending[id]
	delete(r.pending, id)
	r.mu.Unlock()
	if ch != nil {
		ch <- ok
	}
}

// approve is the SetApprover hook. It blocks until a button press or timeout.
func (b *Bot) approve(ctx context.Context, req shell3.ApprovalRequest) bool {
	id, ch := b.approvals.add()
	text := fmt.Sprintf("🔐 Approve tool *%s*?\n`%s`", req.Tool, truncate(req.RawArgs, 300))
	if req.Reason != "" {
		text += "\n_" + req.Reason + "_"
	}
	buttons := []Button{
		{Text: "✅ Approve", Data: "ap:" + id + ":y"},
		{Text: "🚫 Deny", Data: "ap:" + id + ":n"},
	}
	msgID, _ := b.client.Send(ctx, b.chatID, text, buttons)

	timeout := b.approvalTimeout
	if timeout == 0 {
		timeout = 5 * time.Minute
	}
	select {
	case ok := <-ch:
		_ = b.client.EditText(ctx, b.chatID, msgID, decisionText(req.Tool, ok))
		return ok
	case <-time.After(timeout):
		b.approvals.resolve(id, false) // clean up the map entry
		_ = b.client.EditText(ctx, b.chatID, msgID, "⏱ denied (timeout): "+req.Tool)
		return false
	case <-ctx.Done():
		b.approvals.resolve(id, false)
		return false
	}
}

// handleCallback resolves a pending approval from a button press.
func (b *Bot) handleCallback(ctx context.Context, cb *Callback) {
	_ = b.client.AnswerCallback(ctx, cb.ID)
	parts := strings.Split(cb.Data, ":")
	if len(parts) != 3 || parts[0] != "ap" {
		return
	}
	b.approvals.resolve(parts[1], parts[2] == "y")
}

func decisionText(tool string, ok bool) string {
	if ok {
		return "✅ approved: " + tool
	}
	return "🚫 denied: " + tool
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
