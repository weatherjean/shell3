//go:build unix

package telegram

import (
	"context"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/runs"
)

func TestSendRecordConversation(t *testing.T) {
	fc := newFakeClient()
	rt := storeRuntime(t, "unused")
	b := newBot(t, fc, rt)
	sess := decoratedSession(t, b, rt)
	root := t.TempDir()
	st, err := runs.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	b.SetRunsRoot(root)
	id, err := st.NewSession(runs.Meta{Agent: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AppendMessage(id, llm.Message{Role: llm.RoleUser, Content: "hello <world>"}); err != nil {
		t.Fatal(err)
	}
	out, err := b.sendRecordHandler(context.Background(), sess,
		`{"kind":"conversation","session":"`+id+`"}`)
	if err != nil || !strings.Contains(out, "sent shell3-conversation-") {
		t.Fatalf("send = %q, %v", out, err)
	}
	fc.mu.Lock()
	defer fc.mu.Unlock()
	if len(fc.docs) != 1 || !strings.Contains(string(fc.docs[0].data), "hello &lt;world&gt;") {
		t.Fatalf("documents = %+v", fc.docs)
	}
}

func TestSendRecordRejectsMissingJob(t *testing.T) {
	fc := newFakeClient()
	rt := storeRuntime(t, "unused")
	b := newBot(t, fc, rt)
	out, err := b.sendRecordHandler(context.Background(), decoratedSession(t, b, rt),
		`{"kind":"job_log","session":"run1"}`)
	if err != nil || !strings.Contains(out, "job is required") {
		t.Fatalf("send = %q, %v", out, err)
	}
}
