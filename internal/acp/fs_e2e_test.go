package acp

import (
	"context"
	"fmt"
	"strings"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"
)

// initializeWithFS performs the ACP initialize handshake advertising the fs
// capability, so subsequent sessions get the editor-buffer backend.
func initializeWithFS(t *testing.T, e *env) {
	t.Helper()
	if _, err := e.conn.Initialize(context.Background(), acpsdk.InitializeRequest{
		ProtocolVersion: acpsdk.ProtocolVersionNumber,
		ClientCapabilities: acpsdk.ClientCapabilities{
			Fs: acpsdk.FileSystemCapabilities{ReadTextFile: true, WriteTextFile: true},
		},
	}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
}

// fsEnv builds an env whose agent has the read + edit_file tools and whose
// recorder acts as a fake editor buffer holding files.
func fsEnv(t *testing.T, files map[string]string, scripts ...string) *env {
	t.Helper()
	llm := newFakeLLM(t, nil, scripts...)
	lua := fmt.Sprintf(`
shell3.model("test", { base_url = %q, api_key = "test", model = "gpt-4o", context_window = 128000 })
shell3.agent({ name = "code", model = "test", prompt = "You are a coding assistant.",
  tools = { bash = true, edit = true, read = true } })
`, llm.URL)
	e := buildPumpEnv(t, lua)
	e.rec.mu.Lock()
	e.rec.fsFiles = files
	e.rec.mu.Unlock()
	initializeWithFS(t, e)
	return e
}

// TestACPFS_ReadDelegatesToEditorBuffer drives the read tool end-to-end
// through the editor-buffer backend: the client advertised fs, so the read
// goes to the client (unsaved buffer content), not the disk.
func TestACPFS_ReadDelegatesToEditorBuffer(t *testing.T) {
	e := fsEnv(t,
		map[string]string{"/tmp/buffer.txt": "unsaved buffer content"},
		`tool:read:{"path":"/tmp/buffer.txt"}`,
		"read the buffer",
	)
	sessID := newSession(t, e.conn)

	if _, err := e.conn.Prompt(context.Background(), promptRequest(sessID, "read it")); err != nil {
		t.Fatalf("Prompt: %v", err)
	}

	e.rec.mu.Lock()
	reads := append([]string(nil), e.rec.fsReads...)
	e.rec.mu.Unlock()
	if len(reads) == 0 || reads[0] != "/tmp/buffer.txt" {
		t.Fatalf("expected a client ReadTextFile for /tmp/buffer.txt, got %v", reads)
	}
	// The buffer content must have reached the model as the tool result.
	var sawContent bool
	for _, n := range e.rec.snapshotUpdates() {
		u := n.Update.ToolCallUpdate
		if u == nil {
			continue
		}
		for _, c := range u.Content {
			if c.Content != nil && c.Content.Content.Text != nil &&
				strings.Contains(c.Content.Content.Text.Text, "unsaved buffer content") {
				sawContent = true
			}
		}
	}
	if !sawContent {
		t.Fatal("tool result never carried the editor-buffer content")
	}
}

// TestACPFS_WriteDelegatesToEditorBuffer drives edit_file (create) end-to-end:
// the write must flow to the client, not the disk.
func TestACPFS_WriteDelegatesToEditorBuffer(t *testing.T) {
	e := fsEnv(t,
		map[string]string{},
		`tool:edit_file:{"file_path":"/tmp/new.txt","old_string":"","new_string":"hello from the agent"}`,
		"wrote the file",
	)
	sessID := newSession(t, e.conn)

	if _, err := e.conn.Prompt(context.Background(), promptRequest(sessID, "write it")); err != nil {
		t.Fatalf("Prompt: %v", err)
	}

	e.rec.mu.Lock()
	writes := append([]string(nil), e.rec.fsWrites...)
	content := e.rec.fsFiles["/tmp/new.txt"]
	e.rec.mu.Unlock()
	if len(writes) == 0 || writes[0] != "/tmp/new.txt" {
		t.Fatalf("expected a client WriteTextFile for /tmp/new.txt, got %v", writes)
	}
	if content != "hello from the agent" {
		t.Fatalf("editor buffer content = %q, want the written text", content)
	}
}

// TestACPFS_MissingFileMapsToCreate pins the not-found mapping through the
// real acpFS methods: edit_file with empty old_string on a file the client
// doesn't have must be treated as a create (os.ErrNotExist), not a hard error.
func TestACPFS_MissingFileMapsToCreate(t *testing.T) {
	e := fsEnv(t,
		map[string]string{},
		`tool:edit_file:{"file_path":"/tmp/absent.txt","old_string":"","new_string":"created"}`,
		"created it",
	)
	sessID := newSession(t, e.conn)

	if _, err := e.conn.Prompt(context.Background(), promptRequest(sessID, "create it")); err != nil {
		t.Fatalf("Prompt: %v", err)
	}

	e.rec.mu.Lock()
	content, ok := e.rec.fsFiles["/tmp/absent.txt"]
	e.rec.mu.Unlock()
	if !ok || content != "created" {
		t.Fatalf("expected the missing file to be created via the client, got (%q, %v)", content, ok)
	}
}
