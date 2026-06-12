package shell3

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/notify"
	"github.com/weatherjean/shell3/internal/store"
)

func TestRevivePrompt_SummarizesDrainedInbox(t *testing.T) {
	st, _ := store.Open(":memory:")
	defer st.Close()
	parent, _ := st.StartSession()
	_ = st.AppendInbox(parent, mustJSON(notify.Notification{Kind: notify.KindAgentDone, ID: "explore1", Preview: "found 3 files"}))
	_ = st.AppendInbox(parent, mustJSON(notify.Notification{Kind: notify.KindAgentDone, ID: "explore2", Preview: "all green"}))

	prompt, err := revivePrompt(st, parent)
	if err != nil {
		t.Fatalf("revivePrompt: %v", err)
	}
	if !strings.Contains(prompt, "explore1") || !strings.Contains(prompt, "found 3 files") ||
		!strings.Contains(prompt, "explore2") || !strings.Contains(prompt, "all green") {
		t.Fatalf("prompt missing drained notifications:\n%s", prompt)
	}
}

func mustJSON(n notify.Notification) []byte {
	b, _ := json.Marshal(n)
	return b
}
