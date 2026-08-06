//go:build unix

package media

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/weatherjean/shell3/internal/shell3"
)

// hostToolRegistrar is the narrow registration surface RegisterImageTool
// needs, satisfied by *shell3.Session. Declaring it here (rather than taking
// *shell3.Session directly) keeps this package's tests independent of a live
// Session. Headless steers the tool's delivery instructions: a headless
// session (subagent, cron job) has no user to show it to, so it is told to
// report the saved path instead.
type hostToolRegistrar interface {
	RegisterHostTool(t shell3.HostTool) error
	Headless() bool
}

// RegisterImageTool registers the image_generate host tool on sess when
// c.Generate is configured (i.e. media.imagegen: was declared); it is a
// no-op (nil error, nothing registered) when c is nil or c.Generate is nil.
func RegisterImageTool(sess hostToolRegistrar, c *Clients) error {
	if c == nil || c.Generate == nil {
		return nil
	}
	headless := sess.Headless()
	deliver := "deliver it to the user with the send_media_telegram tool (kind: photo) if you have it, otherwise report the file path."
	if headless {
		deliver = "include it in your final report so the requester can deliver it."
	}
	return sess.RegisterHostTool(shell3.HostTool{
		Name:        "image_generate",
		Description: "Generate an image from a text prompt. Returns the saved image's path — " + deliver,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"prompt": map[string]any{"type": "string", "description": "Text description of the image to generate."},
				"size":   map[string]any{"type": "string", "description": "Optional image dimensions (e.g. \"1024x1024\"). Defaults to the configured size."},
			},
			"required": []string{"prompt"},
		},
		Handler: newImageGenerateHandler(c, headless),
	})
}

// newImageGenerateHandler builds the image_generate tool Handler for c.
// Failures are returned as "error: …" tool-result strings (not Go errors),
// matching the engine's other host tools (see sendMediaHandler). headless
// picks the delivery instruction in the success result: report-the-path for
// headless sessions (subagents, cron jobs).
func newImageGenerateHandler(c *Clients, headless bool) func(ctx context.Context, argsJSON string) (string, error) {
	return func(ctx context.Context, argsJSON string) (string, error) {
		var args struct {
			Prompt string `json:"prompt"`
			Size   string `json:"size"`
		}
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return "error: invalid arguments: " + err.Error(), nil
		}
		prompt := strings.TrimSpace(args.Prompt)
		if prompt == "" {
			return "error: prompt is required", nil
		}
		size := strings.TrimSpace(args.Size)
		if size == "" {
			size = c.GenSize
		}
		path, err := c.Generate(ctx, prompt, size)
		if err != nil {
			return "error: " + err.Error(), nil
		}
		if headless {
			return fmt.Sprintf("generated image at %s — include this path in your final report so the requester can deliver it", path), nil
		}
		return fmt.Sprintf("generated image saved at %s — send it with send_media_telegram (kind: photo), or report this path", path), nil
	}
}
