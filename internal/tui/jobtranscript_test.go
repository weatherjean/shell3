package tui

import (
	"strings"
	"testing"
)

func TestRenderJobTranscript(t *testing.T) {
	// Backtick raw string: the \n stay as the two-char JSON escape, so the line is
	// valid JSON (the sink's json.Marshal escapes newlines the same way).
	out := `line01\nline02\nline03\nline04\nline05\nline06\nline07\nline08\nline09\nline10\nline11\nline12\nline13\nline14`
	raw := strings.Join([]string{
		`{"kind":"start"}`, // skipped
		`{"kind":"user_message","role":"user","text":"find the main entrypoint"}`,
		`{"kind":"assistant_reasoning","text":"Let me search for the main package."}`,
		`{"kind":"assistant_token","text":"ZZTOKEN"}`, // streaming delta — must be skipped
		`{"kind":"tool_call","tool":"bash","input":"{\"command\":\"rg main\"}"}`,
		`{"kind":"tool_result","tool":"bash","output":"` + out + `"}`,
		`{"kind":"assistant_message","role":"assistant","text":"Found it in **cmd/main.go**"}`,
		`this line is not json and must be skipped`,
		`{"kind":"usage"}`, // skipped
	}, "\n")

	plain := stripANSI(strings.Join(renderJobTranscript(raw, 60), "\n"))

	for _, want := range []string{
		"find the main entrypoint",   // user message
		"thinking",                   // reasoning header
		"Let me search for the main", // reasoning body
		"bash",                       // tool name
		"rg main",                    // tool call input
		"line01",                     // tool result body (head shown)
		"more lines",                 // tool result truncated (14 > 12)
		"cmd/main.go",                // assistant markdown (text survives)
	} {
		if !strings.Contains(plain, want) {
			t.Errorf("transcript render missing %q in:\n%s", want, plain)
		}
	}
	// Streaming token deltas are skipped (the final assistant_message is the form shown).
	if strings.Contains(plain, "ZZTOKEN") {
		t.Errorf("streaming token delta leaked into the render:\n%s", plain)
	}
	// Tool result truncated at toolResultMaxLines (12): later lines are dropped.
	if strings.Contains(plain, "line14") {
		t.Errorf("tool result not truncated — line14 should be hidden:\n%s", plain)
	}
}
