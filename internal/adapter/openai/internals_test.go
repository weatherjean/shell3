package openai

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
)

type mockTransport struct {
	resp *http.Response
	err  error
}

func (m *mockTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return m.resp, m.err
}

func sseResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

func TestBodyTapCapturesRequestBody(t *testing.T) {
	responseBody := "data: [DONE]\n\n"
	tap := &bodyTap{rt: &mockTransport{resp: sseResponse(responseBody)}}
	body := []byte(`{"test":true}`)
	req, _ := http.NewRequest("POST", "http://x", bytes.NewReader(body))
	resp, err := tap.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	req2, res2 := tap.snapshot()
	if !bytes.Equal(req2, body) {
		t.Fatalf("request body: got %q want %q", req2, body)
	}
	if string(res2) != responseBody {
		t.Fatalf("response body: got %q want %q", res2, responseBody)
	}
}

func TestBodyTapCapturesErrorResponseBody(t *testing.T) {
	errBody := `{"error":"not authorized"}`
	tap := &bodyTap{
		rt: &mockTransport{resp: &http.Response{
			StatusCode: 401,
			Body:       io.NopCloser(strings.NewReader(errBody)),
			Header:     make(http.Header),
		}},
	}
	req, _ := http.NewRequest("POST", "http://x", strings.NewReader("body"))
	resp, err := tap.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		t.Fatal(err)
	}

	_, res := tap.snapshot()
	if !strings.Contains(string(res), "not authorized") {
		t.Fatalf("error body not captured: %q", res)
	}
}

func TestBodyTapBoundsTrafficCapture(t *testing.T) {
	body := strings.Repeat("r", trafficCaptureBytes+1)
	tap := &bodyTap{rt: &mockTransport{resp: sseResponse(body)}}
	req, _ := http.NewRequest("POST", "http://x", strings.NewReader(strings.Repeat("q", trafficCaptureBytes+1)))
	resp, err := tap.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		t.Fatal(err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatal(err)
	}
	reqBody, resBody := tap.snapshot()
	if len(reqBody) != trafficCaptureBytes || len(resBody) != trafficCaptureBytes {
		t.Fatalf("capture sizes = %d/%d, want %d/%d", len(reqBody), len(resBody), trafficCaptureBytes, trafficCaptureBytes)
	}
}

func TestBodyTapNilBody(t *testing.T) {
	tap := &bodyTap{rt: &mockTransport{resp: sseResponse("data: [DONE]\n\n")}}
	req, _ := http.NewRequest("GET", "http://x", nil)
	resp, err := tap.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
}

func TestToMessagesBasic(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "hello"},
		{Role: llm.RoleAssistant, Content: "hi"},
	}
	out := toMessages(msgs)
	if len(out) != 2 {
		t.Fatalf("expected 2, got %d", len(out))
	}
	if out[0].OfUser == nil {
		t.Fatalf("first must be user, got %+v", out[0])
	}
	if out[1].OfAssistant == nil {
		t.Fatalf("second must be assistant, got %+v", out[1])
	}
}

func TestToMessagesToolCall(t *testing.T) {
	msgs := []llm.Message{
		{
			Role: llm.RoleAssistant,
			ToolCalls: []llm.ToolCall{
				{ID: "tc1", Name: "bash", RawArgs: `{"cmd":"ls"}`},
			},
		},
	}
	out := toMessages(msgs)
	if len(out) != 1 || out[0].OfAssistant == nil {
		t.Fatalf("expected 1 assistant, got %+v", out)
	}
	asst := out[0].OfAssistant
	if len(asst.ToolCalls) != 1 || asst.ToolCalls[0].OfFunction == nil ||
		asst.ToolCalls[0].OfFunction.ID != "tc1" || asst.ToolCalls[0].OfFunction.Function.Name != "bash" {
		t.Fatalf("tool call: %+v", asst.ToolCalls)
	}
}

func TestToMessagesAssistantReasoningContentEchoed(t *testing.T) {
	msgs := []llm.Message{
		{
			Role:             llm.RoleAssistant,
			Content:          "thinking complete",
			ReasoningContent: "step 1 step 2",
			ToolCalls: []llm.ToolCall{
				{ID: "tc1", Name: "bash", RawArgs: `{"cmd":"ls"}`},
			},
		},
	}
	out := toMessages(msgs)
	raw, err := out[0].MarshalJSON()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"reasoning_content":"step 1 step 2"`) {
		t.Fatalf("reasoning_content not in serialized assistant message: %s", raw)
	}
}

func TestToMessagesThinkingModeEchoesEmptyReasoning(t *testing.T) {
	msgs := []llm.Message{
		{
			Role:             llm.RoleAssistant,
			ReasoningContent: "thought about it",
			ToolCalls:        []llm.ToolCall{{ID: "1", Name: "bash", RawArgs: `{}`}},
		},
		{Role: llm.RoleTool, Content: "ok", ToolCallID: "1"},
		{
			Role:      llm.RoleAssistant,
			ToolCalls: []llm.ToolCall{{ID: "2", Name: "edit_file", RawArgs: `{}`}},
		},
	}
	out := toMessages(msgs)
	raw, err := out[2].MarshalJSON()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"reasoning_content":""`) {
		t.Fatalf("empty reasoning_content not echoed in thinking mode: %s", raw)
	}
}

func TestToMessagesNoReasoningNoField(t *testing.T) {
	msgs := []llm.Message{
		{
			Role:      llm.RoleAssistant,
			Content:   "plain",
			ToolCalls: []llm.ToolCall{{ID: "1", Name: "bash", RawArgs: `{}`}},
		},
	}
	out := toMessages(msgs)
	raw, err := out[0].MarshalJSON()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "reasoning_content") {
		t.Fatalf("reasoning_content leaked into non-thinking conversation: %s", raw)
	}
}

func TestToMessagesToolResult(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleTool, Content: "output", ToolCallID: "tc1"},
	}
	out := toMessages(msgs)
	if len(out) != 1 || out[0].OfTool == nil {
		t.Fatalf("expected tool message, got %+v", out)
	}
	if out[0].OfTool.ToolCallID != "tc1" {
		t.Fatalf("ToolCallID: %q", out[0].OfTool.ToolCallID)
	}
}

func TestToTools(t *testing.T) {
	tools := []llm.ToolDefinition{
		{
			Name:        "bash",
			Description: "run shell commands",
			Parameters:  map[string]any{"type": "object"},
		},
	}
	out := toTools(tools)
	if len(out) != 1 {
		t.Fatalf("expected 1, got %d", len(out))
	}
	if out[0].OfFunction == nil || out[0].OfFunction.Function.Name != "bash" {
		t.Fatalf("tool: %+v", out[0])
	}
}
