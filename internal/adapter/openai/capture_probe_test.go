//go:build probe

package openai

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

func captureKey(t *testing.T) string {
	t.Helper()
	if key := os.Getenv("MINMAX_API_KEY"); key != "" {
		return key
	}
	t.Skip("MINMAX_API_KEY not in process environment")
	return ""
}

func TestCaptureStreams(t *testing.T) {
	cl := openai.NewClient(
		option.WithAPIKey(captureKey(t)),
		option.WithBaseURL("https://api.minimax.io/v1"),
	)
	cases := []struct {
		name, prompt string
		split        bool
	}{
		{"tags_in_prose", `In one short sentence, explain what the Python regex r"<think>.*?</think>" matches. Include the regex verbatim.`, false},
		{"tags_in_prose_split", `In one short sentence, explain what the Python regex r"<think>.*?</think>" matches. Include the regex verbatim.`, true},
		{"plain_answer", "What is 17 * 23? Answer with just the number.", false},
		{"code_fence", "Show me a 3-line Python function that strips HTML tags from a string. Code only.", false},
		{"long_prose", "Explain in two paragraphs why streaming APIs use server-sent events.", false},
	}
	dir := filepath.Join("testdata", "minimax")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			params := openai.ChatCompletionNewParams{
				Model:               "MiniMax-M3",
				Messages:            []openai.ChatCompletionMessageParamUnion{openai.UserMessage(c.prompt)},
				MaxCompletionTokens: openai.Int(3000),
			}
			opts := []option.RequestOption{}
			if c.split {
				opts = append(opts, option.WithJSONSet("reasoning_split", true))
			}
			stream := cl.Chat.Completions.NewStreaming(context.Background(), params, opts...)
			defer stream.Close()
			var sb strings.Builder
			for stream.Next() {
				sb.WriteString(stream.Current().RawJSON())
				sb.WriteString("\n")
			}
			if err := stream.Err(); err != nil {
				t.Fatalf("stream: %v", err)
			}
			path := filepath.Join(dir, c.name+".jsonl")
			if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
				t.Fatal(err)
			}
			t.Logf("wrote %s (%d bytes)", path, sb.Len())
		})
	}
}
