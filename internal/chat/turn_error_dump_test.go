package chat

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/llm"
)

func TestErrorDumpIsBoundedAndPrivate(t *testing.T) {
	msgs := make([]llm.Message, 20)
	for i := range msgs {
		msgs[i] = llm.Message{
			Role:             llm.RoleAssistant,
			Content:          strings.Repeat("message", errorDumpMessageBytes),
			ReasoningContent: strings.Repeat("reason", errorDumpMessageBytes),
			ToolCalls:        []llm.ToolCall{{Name: "bash", RawArgs: strings.Repeat("arg", errorDumpToolArgsBytes)}},
		}
	}
	data, err := buildErrorDump(msgs, errors.New(strings.Repeat("failure", errorDumpErrorBytes)),
		[]byte(strings.Repeat("request", errorDumpTrafficBytes)),
		[]byte(strings.Repeat("response", errorDumpTrafficBytes)), time.Unix(1, 0))
	if err != nil {
		t.Fatalf("buildErrorDump: %v", err)
	}
	if len(data) > errorDumpMaxBytes {
		t.Fatalf("dump is %d bytes, cap is %d", len(data), errorDumpMaxBytes)
	}
	if !strings.Contains(string(data), `"messages_omitted": 8`) {
		t.Fatalf("dump does not report omitted messages: %s", data)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "state", "last_error.json")
	if err := writePrivateAtomic(path, data); err != nil {
		t.Fatalf("writePrivateAtomic: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode = %o, want 600", got)
	}
	if temps, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".last_error-*.tmp")); err != nil || len(temps) != 0 {
		t.Fatalf("temporary files remain: %v (glob error %v)", temps, err)
	}
}
