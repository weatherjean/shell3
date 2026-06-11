package chat

import (
	"os"
	"path/filepath"
	"testing"
)

// TestReadTranscript_FinalAssistantText asserts ReadTranscript returns the LAST
// assistant_message text (the run's concluding reply) and not an earlier one,
// and reports no error for an ok run. This is the bridge cron Dispatch and the
// child agent_done self-report use to summarize a headless run.
func TestReadTranscript_FinalAssistantText(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "t.jsonl")
	content := `{"kind":"start"}
{"kind":"assistant_message","text":"first pass"}
{"kind":"tool_call","tool":"bash"}
{"kind":"assistant_message","text":"final answer"}
{"kind":"end","status":"ok"}
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	res := ReadTranscript(path)
	if res.FinalText != "final answer" {
		t.Errorf("FinalText = %q, want %q", res.FinalText, "final answer")
	}
	if res.Errored {
		t.Error("Errored = true, want false for an ok run")
	}
}

// TestReadTranscript_ErrorMarkers asserts a transcript with an error event OR a
// non-ok end status is reported as errored.
func TestReadTranscript_ErrorMarkers(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"error-event": `{"kind":"assistant_message","text":"partial"}` + "\n" + `{"kind":"error","text":"boom"}` + "\n",
		"end-status":  `{"kind":"assistant_message","text":"partial"}` + "\n" + `{"kind":"end","status":"error"}` + "\n",
	} {
		path := filepath.Join(dir, name+".jsonl")
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if res := ReadTranscript(path); !res.Errored {
			t.Errorf("%s: Errored = false, want true", name)
		}
	}
}

// TestReadTranscript_MissingFile asserts a missing/unreadable transcript yields
// a zero result rather than panicking — the transcript pointer in the
// notification remains the durable guarantee.
func TestReadTranscript_MissingFile(t *testing.T) {
	res := ReadTranscript(filepath.Join(t.TempDir(), "nope.jsonl"))
	if res.FinalText != "" || res.Errored {
		t.Errorf("missing file = %+v, want zero result", res)
	}
}
