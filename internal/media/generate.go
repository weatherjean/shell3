//go:build unix

package media

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/openai/openai-go"

	"github.com/weatherjean/shell3/internal/luacfg"
)

// extForMediaType maps an OpenRouter-style image media_type to the file
// extension used for the saved output. Unknown or empty types fall back to
// .png, matching the openai branch's fixed PNG assumption.
func extForMediaType(mediaType string) string {
	// Normalize: strip parameters (e.g., "; charset=utf-8"), trim whitespace, lowercase.
	if idx := strings.IndexByte(mediaType, ';'); idx >= 0 {
		mediaType = mediaType[:idx]
	}
	mediaType = strings.TrimSpace(strings.ToLower(mediaType))

	switch mediaType {
	case "image/jpeg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	case "image/png":
		return ".png"
	default:
		return ".png"
	}
}

// writeImageFile decodes b64 image data and saves it to shell3's durable
// media directory (~/.shell3/media) with the given extension, returning the
// saved path.
func writeImageFile(data []byte, ext string) (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	f, err := os.CreateTemp(dir, "img-*"+ext)
	if err != nil {
		return "", fmt.Errorf("media: generate: creating image file: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return "", fmt.Errorf("media: generate: writing image file: %w", err)
	}
	return f.Name(), nil
}

// newGenerator builds Clients.Generate for cfg, resolving its client (and, on
// first use, spawning cfg's model's run_proxy) via sdk. It requests
// base64-encoded image data explicitly (response_format is only sent by the
// SDK when set) so the result can be decoded and written to disk without a
// second network round trip through a signed URL.
//
// cfg.API selects the wire shape: "openai" (or unset) uses the openai-go SDK
// against Images.Generate; "openrouter" POSTs a chat-completions request with
// modalities=["image","text"] and reads the generated image off the reply
// message, matching how OpenRouter serves image-output models. (OpenRouter
// also has a dedicated /api/v1/images endpoint, but it pre-authorizes the
// request's worst-case token cost — ~$2 for a Gemini image model — and 402s
// on any lower balance; the chat route only charges actual usage.)
func newGenerator(sdk sdkFn, cfg luacfg.ImagegenConfig) func(context.Context, string, string) (string, error) {
	if cfg.API == "openrouter" {
		return newOpenRouterGenerator(sdk, cfg)
	}
	return func(ctx context.Context, prompt, size string) (string, error) {
		client, m := sdk(cfg.ModelRef)
		params := openai.ImageGenerateParams{
			Model:          openai.ImageModel(m.ModelID),
			Prompt:         prompt,
			Size:           openai.ImageGenerateParamsSize(size),
			ResponseFormat: openai.ImageGenerateParamsResponseFormatB64JSON,
		}
		resp, err := client.Images.Generate(ctx, params)
		if err != nil {
			return "", err
		}
		if len(resp.Data) == 0 || resp.Data[0].B64JSON == "" {
			return "", fmt.Errorf("media: generate: empty response")
		}
		data, err := base64.StdEncoding.DecodeString(resp.Data[0].B64JSON)
		if err != nil {
			return "", fmt.Errorf("media: generate: decoding image: %w", err)
		}
		return writeImageFile(data, ".png")
	}
}

// openRouterChatImageResponse is the subset of a chat-completions reply this
// package decodes: OpenRouter's image-output models attach generated images
// to the assistant message as data-URL image_url parts.
type openRouterChatImageResponse struct {
	Choices []struct {
		Message struct {
			Images []struct {
				ImageURL struct {
					URL string `json:"url"`
				} `json:"image_url"`
			} `json:"images"`
		} `json:"message"`
	} `json:"choices"`
}

// splitImageDataURL splits a data:<media_type>;base64,<payload> URL into its
// media type and base64 payload. Bare base64 (no data: prefix — some
// providers do this) comes back with an empty media type, which
// extForMediaType turns into the .png fallback.
func splitImageDataURL(url string) (mediaType, b64 string) {
	rest, ok := strings.CutPrefix(url, "data:")
	if !ok {
		return "", url
	}
	mediaType, b64, ok = strings.Cut(rest, ";base64,")
	if !ok {
		return "", url
	}
	return mediaType, b64
}

// newOpenRouterGenerator builds the "openrouter" API-shape Generate function:
// a raw chat-completions POST with modalities=["image","text"], bypassing the
// openai-go SDK (whose chat types don't carry the modalities request field or
// the images reply field). The size argument is ignored — the chat route has
// no size parameter. The client returned by sdk is unused here — only the
// resolved luacfg.Model (base URL, API key, model id) matters — but sdk is
// still called so the shared sdkOnce/once machinery spawns the model's
// run_proxy exactly once on first use, same as every other capability.
func newOpenRouterGenerator(sdk sdkFn, cfg luacfg.ImagegenConfig) func(context.Context, string, string) (string, error) {
	return func(ctx context.Context, prompt, _ string) (string, error) {
		_, m := sdk(cfg.ModelRef)

		body := map[string]any{
			"model":      m.ModelID,
			"modalities": []string{"image", "text"},
			"messages": []map[string]string{
				{"role": "user", "content": prompt},
			},
		}
		payload, err := json.Marshal(body)
		if err != nil {
			return "", fmt.Errorf("media: generate: encoding request: %w", err)
		}

		url := strings.TrimRight(m.BaseURL, "/") + "/chat/completions"
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
		if err != nil {
			return "", fmt.Errorf("media: generate: building request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+m.APIKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return "", fmt.Errorf("media: generate: request: %w", err)
		}
		defer resp.Body.Close()

		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", fmt.Errorf("media: generate: reading response: %w", err)
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			snippet := respBody
			if len(snippet) > 200 {
				snippet = snippet[:200]
			}
			return "", fmt.Errorf("media: generate: openrouter status %d: %s", resp.StatusCode, snippet)
		}

		var parsed openRouterChatImageResponse
		if err := json.Unmarshal(respBody, &parsed); err != nil {
			return "", fmt.Errorf("media: generate: decoding response: %w", err)
		}
		if len(parsed.Choices) == 0 || len(parsed.Choices[0].Message.Images) == 0 ||
			parsed.Choices[0].Message.Images[0].ImageURL.URL == "" {
			return "", fmt.Errorf("media: generate: reply carries no image")
		}
		mediaType, b64 := splitImageDataURL(parsed.Choices[0].Message.Images[0].ImageURL.URL)
		data, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return "", fmt.Errorf("media: generate: decoding image: %w", err)
		}
		return writeImageFile(data, extForMediaType(mediaType))
	}
}
