package chat

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/paths"
)

type diagnosticLLM struct{}

func (diagnosticLLM) Stream(context.Context, []llm.Message, []llm.ToolDefinition, func(llm.StreamEvent)) error {
	return nil
}

func (diagnosticLLM) LastTraffic() ([]byte, []byte) {
	return []byte("request bytes"), []byte("partial response bytes")
}

type diagnosticLogger struct {
	message string
	err     error
	fields  []any
}

func (*diagnosticLogger) Debug(string, ...any) {}
func (*diagnosticLogger) Info(string, ...any)  {}
func (*diagnosticLogger) Warn(string, ...any)  {}
func (l *diagnosticLogger) Error(message string, err error, fields ...any) {
	l.message, l.err, l.fields = message, err, fields
}

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

func TestStreamErrorWritesSessionTraceAndDiagnostic(t *testing.T) {
	dir := t.TempDir()
	logger := &diagnosticLogger{}
	streamErr := errors.New("stream interrupted")
	logStreamError(TurnConfig{
		ToolConfig: ToolConfig{WorkDir: dir, Log: logger},
		LLM:        diagnosticLLM{},
	}, "session-1", []llm.Message{{Role: llm.RoleUser, Content: "hello"}}, streamErr)

	path := paths.LastErrorPath(dir, "session-1")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "partial response bytes") || !strings.Contains(string(data), "stream interrupted") {
		t.Fatalf("trace = %s", data)
	}
	if logger.message != "stream error" || !errors.Is(logger.err, streamErr) {
		t.Fatalf("diagnostic = %q / %v", logger.message, logger.err)
	}
	fields := map[string]any{}
	for i := 0; i+1 < len(logger.fields); i += 2 {
		fields[logger.fields[i].(string)] = logger.fields[i+1]
	}
	if fields["session"] != "session-1" || fields["dump"] != path {
		t.Fatalf("fields = %#v", fields)
	}
}
