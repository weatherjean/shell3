//go:build unix

package media

import (
	"context"
	"fmt"
	"strings"

	"github.com/openai/openai-go"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/config"
	"github.com/weatherjean/shell3/internal/llm"
)

// newDescriber builds Clients.Describe for cfg, resolving its client (and, on
// first use, spawning cfg's model's run_proxy) via sdk. It reuses
// chat.LoadMediaPart for image loading/resizing, so Describe accepts the same
// image types (and 10 MB cap) as the read_media tool.
func newDescriber(sdk sdkFn, cfg config.DescribeConfig) func(context.Context, string) (string, error) {
	return func(ctx context.Context, path string) (string, error) {
		part, _, err := chat.LoadMediaPart(path, "")
		if err != nil {
			return "", err
		}
		if part.Type != llm.ContentPartTypeImageURL {
			return "", fmt.Errorf("describe: %q is not an image", path)
		}

		client, m := sdk(cfg.ModelRef)
		params := openai.ChatCompletionNewParams{
			Model: openai.ChatModel(m.ModelID),
			Messages: []openai.ChatCompletionMessageParamUnion{
				openai.UserMessage([]openai.ChatCompletionContentPartUnionParam{
					openai.TextContentPart(cfg.Prompt),
					openai.ImageContentPart(openai.ChatCompletionContentPartImageImageURLParam{
						URL: part.ImageURL,
					}),
				}),
			},
		}
		resp, err := client.Chat.Completions.New(ctx, params)
		if err != nil {
			return "", err
		}
		if len(resp.Choices) == 0 {
			return "", fmt.Errorf("media: describe: empty response")
		}
		out := strings.TrimSpace(resp.Choices[0].Message.Content)
		if out == "" {
			return "", fmt.Errorf("media: describe: empty response")
		}
		return out, nil
	}
}
