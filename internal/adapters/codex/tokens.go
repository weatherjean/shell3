package codex

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/weatherjean/shell3/internal/config"
)

var authHTTPClient = &http.Client{Timeout: 30 * time.Second}

// instanceName is the stable instance key in the unified CredStore.
const instanceName = "codex"

// Tokens is the in-memory shape used by the rest of this package.
type Tokens struct {
	AccessToken  string
	RefreshToken string
	IDToken      string
	AccountID    string
	PlanType     string
	ExpiresAt    time.Time
}

// IsExpired reports whether the access token is within the skew window of
// expiry. A 60-second skew matches the official Codex CLI.
func (t *Tokens) IsExpired() bool {
	if t.ExpiresAt.IsZero() {
		return true
	}
	return time.Now().Add(60 * time.Second).After(t.ExpiresAt)
}

// LoadTokens reads the codex instance from store.
func LoadTokens(store *config.CredStore) (*Tokens, error) {
	_, fields, ok := store.Get(instanceName)
	if !ok {
		return nil, fmt.Errorf("codex: not authenticated — run: shell3 auth --provider=codex")
	}
	expiresAt, _ := time.Parse(time.RFC3339Nano, fields["expires_at"])
	return &Tokens{
		AccessToken:  fields["access_token"],
		RefreshToken: fields["refresh_token"],
		IDToken:      fields["id_token"],
		AccountID:    fields["account_id"],
		PlanType:     fields["plan_type"],
		ExpiresAt:    expiresAt,
	}, nil
}

// SaveTokens writes the codex instance, overwriting whatever was there.
func SaveTokens(store *config.CredStore, t *Tokens) error {
	fields := map[string]string{
		"access_token":  t.AccessToken,
		"refresh_token": t.RefreshToken,
		"id_token":      t.IDToken,
		"account_id":    t.AccountID,
		"plan_type":     t.PlanType,
		"expires_at":    t.ExpiresAt.UTC().Format(time.RFC3339Nano),
	}
	return store.Set(instanceName, "codex", fields)
}

// decodedIDTokenClaims returns the JWT payload as a JSON string for diagnostics.
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

type idTokenClaims struct {
	AccountID string
	PlanType  string
}

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

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
}

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
