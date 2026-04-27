package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"sync"

	"github.com/google/uuid"

	"github.com/weatherjean/shell3/internal/config"
	"github.com/weatherjean/shell3/internal/llm"
)

// client is the codex.provider's Streamer implementation.
type client struct {
	model     string
	store     *config.CredStore
	sessionID string

	mu     sync.Mutex
	tokens *Tokens
}

// newClient builds a client backed by the unified CredStore.
func newClient(store *config.CredStore, model string) (*client, error) {
	t, err := LoadTokens(store)
	if err != nil {
		return nil, err
	}
	return &client{
		model:     model,
		store:     store,
		sessionID: uuid.NewString(),
		tokens:    t,
	}, nil
}

// SetModel swaps the active model for subsequent requests.
func (c *client) SetModel(model string) { c.model = model }

// Stream sends msgs to the Responses API and emits llm.StreamEvent values
// for each text delta, reasoning delta, tool call, and final usage record.
// Refreshes tokens on demand if expired and retries once on 401.
func (c *client) Stream(ctx context.Context, msgs []llm.Message, tools []llm.ToolDefinition, onEvent func(llm.StreamEvent)) error {
	if err := c.ensureFreshToken(ctx); err != nil {
		return err
	}

	body, err := buildRequest(c.model, msgs, tools)
	if err != nil {
		return err
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("codex: marshal request: %w", err)
	}

	res, err := c.doRequest(ctx, buf)
	if err != nil {
		return err
	}
	if res.StatusCode == http.StatusUnauthorized {
		// Tokens may have been revoked server-side mid-session. Force a
		// refresh and retry exactly once before surfacing the error.
		_ = res.Body.Close()
		if rerr := c.forceRefresh(ctx); rerr != nil {
			return fmt.Errorf("codex: 401 and refresh failed: %w", rerr)
		}
		res, err = c.doRequest(ctx, buf)
		if err != nil {
			return err
		}
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		errBody, _ := io.ReadAll(io.LimitReader(res.Body, 4*1024))
		return fmt.Errorf("codex: %d %s: %s", res.StatusCode, res.Status, redactSensitive(errBody))
	}

	return parseStream(res.Body, onEvent)
}

// doRequest builds the POST and dispatches it. Caller owns the response body.
func (c *client) doRequest(ctx context.Context, buf []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, responsesURL, bytes.NewReader(buf))
	if err != nil {
		return nil, fmt.Errorf("codex: build request: %w", err)
	}
	c.applyHeaders(req)
	res, err := streamHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("codex: request: %w", err)
	}
	return res, nil
}

// streamHTTPClient is the dedicated HTTP client for Responses API calls.
// No client-level Timeout — request context governs lifetime; an SSE stream
// is expected to stay open for the duration of a model turn.
var streamHTTPClient = &http.Client{}

// redactSensitive scrubs Bearer tokens from error bodies before they are
// surfaced to logs or the user.
func redactSensitive(b []byte) string {
	s := string(b)
	for {
		i := bytes.Index([]byte(s), []byte("Bearer "))
		if i < 0 {
			return s
		}
		end := i + 7
		for end < len(s) && (s[end] == '.' || s[end] == '-' || s[end] == '_' || (s[end] >= '0' && s[end] <= '9') || (s[end] >= 'A' && s[end] <= 'Z') || (s[end] >= 'a' && s[end] <= 'z')) {
			end++
		}
		s = s[:i] + "Bearer <redacted>" + s[end:]
	}
}

// applyHeaders sets the auth + identity headers required by the Codex
// Responses API. Mirrors what the official CLI sends.
func (c *client) applyHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer "+c.tokens.AccessToken)
	req.Header.Set("ChatGPT-Account-Id", c.tokens.AccountID)
	req.Header.Set("originator", originator())
	req.Header.Set("session_id", c.sessionID)
	req.Header.Set("User-Agent", userAgent())
}

// userAgent returns the UA string sent on every Responses API request.
// Format mirrors the official Codex CLI (`codex_cli_rs/<ver> (<os> <arch>)`)
// so server-side allow-lists recognize us. Intentionally omits hostname to
// avoid leaking machine identifiers to the server.
func userAgent() string {
	return fmt.Sprintf("%s/0.1 (%s %s)", originator(), runtime.GOOS, runtime.GOARCH)
}

// ensureFreshToken refreshes the access token if it is about to expire.
// Idempotent and safe under concurrent Stream calls (single-flight via mu).
func (c *client) ensureFreshToken(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.tokens.IsExpired() {
		return nil
	}
	if c.tokens.RefreshToken == "" {
		return fmt.Errorf("codex: token expired and no refresh_token — re-run: shell3 auth --provider=codex")
	}
	next, err := refreshTokens(ctx, c.tokens)
	if err != nil {
		return fmt.Errorf("codex: refresh: %w", err)
	}
	if err := SaveTokens(c.store, next); err != nil {
		return err
	}
	c.tokens = next
	return nil
}

// forceRefresh refreshes regardless of expiry. Used after a 401 to recover.
func (c *client) forceRefresh(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.tokens.RefreshToken == "" {
		return fmt.Errorf("no refresh_token")
	}
	next, err := refreshTokens(ctx, c.tokens)
	if err != nil {
		return err
	}
	if err := SaveTokens(c.store, next); err != nil {
		return err
	}
	c.tokens = next
	return nil
}
