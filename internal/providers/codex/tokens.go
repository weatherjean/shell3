package codex

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// authHTTPClient is used for /oauth/token round-trips. A 30s timeout caps
// hangs when auth.openai.com is slow.
var authHTTPClient = &http.Client{Timeout: 30 * time.Second}

// Tokens is the on-disk schema for ~/.shell3/codex_tokens.json.
type Tokens struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	IDToken      string    `json:"id_token"`
	AccountID    string    `json:"account_id"`
	PlanType     string    `json:"plan_type,omitempty"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// IsExpired reports whether the access token is within the skew window of
// expiry. A 60-second skew avoids racing the server clock and matches the
// behavior of the official Codex CLI.
func (t *Tokens) IsExpired() bool {
	if t.ExpiresAt.IsZero() {
		return true
	}
	return time.Now().Add(60 * time.Second).After(t.ExpiresAt)
}

// tokensPath returns the on-disk location of the codex token file.
func tokensPath(homeDir string) string {
	return filepath.Join(homeDir, ".shell3", "codex_tokens.json")
}

// LoadTokens reads tokens from ~/.shell3/codex_tokens.json. Returns a
// not-found error when the file is missing — callers should prompt the user
// to run `shell3 auth --provider=codex`.
func LoadTokens(homeDir string) (*Tokens, error) {
	path := tokensPath(homeDir)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("codex: not authenticated — run: shell3 auth --provider=codex")
		}
		return nil, fmt.Errorf("codex: read tokens: %w", err)
	}
	var t Tokens
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("codex: invalid tokens file: %w — re-run: shell3 auth --provider=codex", err)
	}
	return &t, nil
}

// SaveTokens writes tokens to ~/.shell3/codex_tokens.json with 0600 perms.
// Creates ~/.shell3 if missing.
func SaveTokens(homeDir string, t *Tokens) error {
	dir := filepath.Join(homeDir, ".shell3")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("codex: mkdir %s: %w", dir, err)
	}
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return fmt.Errorf("codex: marshal tokens: %w", err)
	}
	if err := os.WriteFile(tokensPath(homeDir), data, 0600); err != nil {
		return fmt.Errorf("codex: write tokens: %w", err)
	}
	return nil
}

// decodedIDTokenClaims returns the JWT payload as a JSON string for diagnostics.
// Returns the raw payload even if standard claim parsing would fail.
func decodedIDTokenClaims(idToken string) string {
	parts := strings.Split(idToken, ".")
	if len(parts) < 2 {
		return "<unparseable id_token>"
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return fmt.Sprintf("<base64 decode failed: %v>", err)
	}
	return string(payload)
}

// idTokenClaims holds the fields we extract from the ChatGPT id_token.
// OpenAI nests subscription metadata under the namespaced "https://api.openai.com/auth"
// claim per OIDC custom-claim conventions.
type idTokenClaims struct {
	AccountID string
	PlanType  string
}

// extractIDTokenClaims decodes the JWT payload and pulls AccountID + PlanType
// from the namespaced ChatGPT auth object. Falls back to a top-level lookup
// for older token shapes. Returns zero values when no claim matches.
func extractIDTokenClaims(idToken string) idTokenClaims {
	parts := strings.Split(idToken, ".")
	if len(parts) < 2 {
		return idTokenClaims{}
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return idTokenClaims{}
	}
	var raw struct {
		ChatGPTAccountIDTop string `json:"chatgpt_account_id"`
		Auth                struct {
			ChatGPTAccountID string `json:"chatgpt_account_id"`
			ChatGPTPlanType  string `json:"chatgpt_plan_type"`
			Organizations    []struct {
				ID string `json:"id"`
			} `json:"organizations"`
		} `json:"https://api.openai.com/auth"`
	}
	if err := json.Unmarshal(payload, &raw); err != nil {
		return idTokenClaims{}
	}
	c := idTokenClaims{PlanType: raw.Auth.ChatGPTPlanType}
	switch {
	case raw.Auth.ChatGPTAccountID != "":
		c.AccountID = raw.Auth.ChatGPTAccountID
	case raw.ChatGPTAccountIDTop != "":
		c.AccountID = raw.ChatGPTAccountIDTop
	case len(raw.Auth.Organizations) > 0:
		c.AccountID = raw.Auth.Organizations[0].ID
	}
	return c
}

// tokenResponse mirrors auth.openai.com /oauth/token JSON.
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
}

// exchangeCode swaps an OAuth authorization code for tokens.
func exchangeCode(ctx context.Context, code, codeVerifier, redirectURI string) (*Tokens, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {clientID},
		"code_verifier": {codeVerifier},
	}
	return postToken(ctx, form)
}

// refreshTokens uses a refresh_token to mint a new access_token.
// Preserves the existing refresh_token / id_token / account_id when the server
// omits them on refresh (some IdPs do, OpenAI included).
func refreshTokens(ctx context.Context, prev *Tokens) (*Tokens, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {prev.RefreshToken},
		"client_id":     {clientID},
		"scope":         {oauthScope},
	}
	next, err := postToken(ctx, form)
	if err != nil {
		return nil, err
	}
	if next.RefreshToken == "" {
		next.RefreshToken = prev.RefreshToken
	}
	if next.IDToken == "" {
		next.IDToken = prev.IDToken
		next.AccountID = prev.AccountID
	}
	return next, nil
}

// postToken sends a form POST to /oauth/token and returns parsed Tokens.
// Handles the response shape; absolute expiry is computed from expires_in.
func postToken(ctx context.Context, form url.Values) (*Tokens, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, oauthBase+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("codex: build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	res, err := authHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("codex: token request: %w", err)
	}
	defer res.Body.Close()

	body, _ := io.ReadAll(res.Body)
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("codex: token endpoint %d: %s", res.StatusCode, string(body))
	}

	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return nil, fmt.Errorf("codex: parse token response: %w", err)
	}
	if tr.AccessToken == "" {
		return nil, fmt.Errorf("codex: token endpoint returned empty access_token")
	}
	t := &Tokens{
		AccessToken:  tr.AccessToken,
		RefreshToken: tr.RefreshToken,
		IDToken:      tr.IDToken,
		ExpiresAt:    time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second),
	}
	if tr.IDToken != "" {
		c := extractIDTokenClaims(tr.IDToken)
		t.AccountID = c.AccountID
		t.PlanType = c.PlanType
	}
	return t, nil
}
