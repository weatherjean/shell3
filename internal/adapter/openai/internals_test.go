package openai

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/llm"
)

// mockTransport satisfies http.RoundTripper and returns a fixed response.
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

// ---- bodyTap ----

func TestBodyTapCapturesRequestBody(t *testing.T) {
	tap := &bodyTap{
		rt:   &mockTransport{resp: sseResponse("data: [DONE]\n\n")},
		done: make(chan struct{}),
	}
	body := []byte(`{"test":true}`)
	req, _ := http.NewRequest("POST", "http://x", bytes.NewReader(body))
	resp, err := tap.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	// Drain then close explicitly — Close shuts the pipe writer, which lets
	// scanReasoning see EOF and close tap.done. Must happen before <-tap.done.
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	<-tap.done

	req2, res2, _ := tap.snapshot()
	if !bytes.Equal(req2, body) {
		t.Fatalf("request body: got %q want %q", req2, body)
	}
	_ = res2 // 2xx bodies are not buffered
}

func TestBodyTapCapturesErrorResponseBody(t *testing.T) {
	errBody := `{"error":"not authorized"}`
	tap := &bodyTap{
		rt: &mockTransport{resp: &http.Response{
			StatusCode: 401,
			Body:       io.NopCloser(strings.NewReader(errBody)),
			Header:     make(http.Header),
		}},
		done: make(chan struct{}),
	}
	req, _ := http.NewRequest("POST", "http://x", strings.NewReader("body"))
	resp, err := tap.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	_, res, _ := tap.snapshot()
	if !strings.Contains(string(res), "not authorized") {
		t.Fatalf("error body not captured: %q", res)
	}
}

func TestBodyTapNilBody(t *testing.T) {
	tap := &bodyTap{
		rt:   &mockTransport{resp: sseResponse("data: [DONE]\n\n")},
		done: make(chan struct{}),
	}
	req, _ := http.NewRequest("GET", "http://x", nil)
	resp, err := tap.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close() // closes pipe writer so scanReasoning goroutine can exit
}

// ---- scanReasoning ----

func TestScanReasoningExtractsReasoning(t *testing.T) {
	tap := &bodyTap{}
	done := make(chan struct{})
	tap.done = done

	sse := "data: {\"choices\":[{\"delta\":{\"reasoning\":\"step one\"}}]}\n" +
		"data: {\"choices\":[{\"delta\":{\"reasoning\":\" step two\"}}]}\n" +
		"data: [DONE]\n"

	pr := io.NopCloser(strings.NewReader(sse))
	go tap.scanReasoning(pr, done)
	<-done

	tap.mu.Lock()
	got := tap.reasoning
	tap.mu.Unlock()
	if got != "step one step two" {
		t.Fatalf("reasoning: got %q", got)
	}
}

func TestScanReasoningQueuesFragments(t *testing.T) {
	tap := &bodyTap{}
	done := make(chan struct{})
	tap.done = done

	sse := "data: {\"choices\":[{\"delta\":{\"reasoning\":\"alpha\"}}]}\n" +
		"data: {\"choices\":[{\"delta\":{\"reasoning\":\"beta\"}}]}\n" +
		"data: [DONE]\n"

	go tap.scanReasoning(io.NopCloser(strings.NewReader(sse)), done)
	<-done

	got := tap.drainReasoning()
	if len(got) != 2 || got[0] != "alpha" || got[1] != "beta" {
		t.Fatalf("reasoningQueue: got %v", got)
	}
}

func TestScanReasoningExtractsReasoningContent(t *testing.T) {
	tap := &bodyTap{}
	done := make(chan struct{})
	tap.done = done

	sse := "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"moonshot \"}}]}\n" +
		"data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"thinks\"}}]}\n" +
		"data: [DONE]\n"

	pr := io.NopCloser(strings.NewReader(sse))
	go tap.scanReasoning(pr, done)
	<-done

	tap.mu.Lock()
	got := tap.reasoning
	tap.mu.Unlock()
	if got != "moonshot thinks" {
		t.Fatalf("reasoning_content: got %q", got)
	}
}

func TestScanReasoningIgnoresBadJSON(t *testing.T) {
	tap := &bodyTap{}
	done := make(chan struct{})
	tap.done = done

	sse := "data: {bad json}\n" +
		"data: {\"choices\":[{\"delta\":{\"reasoning\":\"ok\"}}]}\n" +
		"data: [DONE]\n"

	pr := io.NopCloser(strings.NewReader(sse))
	go tap.scanReasoning(pr, done)
	<-done

	tap.mu.Lock()
	got := tap.reasoning
	tap.mu.Unlock()
	if got != "ok" {
		t.Fatalf("reasoning: got %q", got)
	}
}

func TestWaitReasoningRespectsContextCancel(t *testing.T) {
	tap := &bodyTap{}
	done := make(chan struct{}) // never closed
	tap.mu.Lock()
	tap.done = done
	tap.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	r := tap.WaitReasoning(ctx)
	if r != "" {
		t.Fatalf("expected empty on cancel, got %q", r)
	}
}

func TestWaitReasoningNilDone(t *testing.T) {
	tap := &bodyTap{} // done is nil
	r := tap.WaitReasoning(context.Background())
	if r != "" {
		t.Fatalf("expected empty for nil done, got %q", r)
	}
}

// ---- toMessages ----

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
	if len(asst.ToolCalls) != 1 || asst.ToolCalls[0].ID != "tc1" || asst.ToolCalls[0].Function.Name != "bash" {
		t.Fatalf("tool call: %+v", asst.ToolCalls)
	}
}

func TestToMessagesContentParts(t *testing.T) {
	msgs := []llm.Message{
		{
			Role: llm.RoleUser,
			ContentParts: []llm.ContentPart{
				{Type: llm.ContentPartTypeText, Text: "describe this"},
				{Type: llm.ContentPartTypeImageURL, ImageURL: "http://example.com/img.png"},
			},
		},
	}
	out := toMessages(msgs)
	if len(out) != 1 || out[0].OfUser == nil {
		t.Fatalf("expected user message, got %+v", out)
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

// ---- toTools ----

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
	if out[0].Function.Name != "bash" {
		t.Fatalf("tool: %+v", out[0].Function)
	}
}
