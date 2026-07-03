package tui

import (
	"strings"
	"testing"
)

func TestRenderJobTranscript(t *testing.T) {
	// The tool result has 14 lines encoded as JSON \n escapes so json.Unmarshal
	// decodes them to actual newlines — matches how the runs store persists content.
	out := `line01\nline02\nline03\nline04\nline05\nline06\nline07\nline08\nline09\nline10\nline11\nline12\nline13\nline14`
	raw := strings.Join([]string{
		`{"role":"system","content":"system prompt — must be skipped"}`,
		`{"role":"user","content":"find the main entrypoint"}`,
		`{"role":"assistant","reasoning_content":"Let me search for the main package.","tool_calls":[{"ID":"c1","Name":"bash","RawArgs":"{\"command\":\"rg main\"}"}]}`,
		`{"role":"tool","content":"` + out + `","name":"bash","tool_call_id":"c1"}`,
		`{"role":"assistant","content":"Found it in **cmd/main.go**"}`,
		`this line is not json and must be skipped`,
		`{"role":"assistant","content":""}`, // empty content — must not add a blank block
	}, "\n")

	plain := stripANSI(strings.Join(renderJobTranscript(raw, 60), "\n"))

	for _, want := range []string{
		"find the main entrypoint",   // user message
		"thinking",                   // reasoning header
		"Let me search for the main", // reasoning body
		"bash",                       // tool name
		"rg main",                    // tool call args
		"line01",                     // tool result body (head shown)
		"more lines",                 // tool result truncated (14 > 12)
		"cmd/main.go",                // assistant markdown (text survives)
	} {
		if !strings.Contains(plain, want) {
			t.Errorf("transcript render missing %q in:\n%s", want, plain)
		}
	}
	// System messages are silently skipped.
	if strings.Contains(plain, "system prompt") {
		t.Errorf("system message leaked into the render:\n%s", plain)
	}
	// Tool result truncated at toolResultMaxLines (12): later lines are dropped.
	if strings.Contains(plain, "line14") {
		t.Errorf("tool result not truncated — line14 should be hidden:\n%s", plain)
	}
}
