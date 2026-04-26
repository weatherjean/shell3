package codex

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"time"
)

// originator returns the value sent in the OAuth `originator` parameter and
// later as a request header. auth.openai.com appears to allow-list this
// per client_id, so we default to the official Codex CLI's value.
// Override with SHELL3_CODEX_ORIGINATOR for experimentation.
func originator() string {
	if v := os.Getenv("SHELL3_CODEX_ORIGINATOR"); v != "" {
		return v
	}
	return defaultOriginator
}

// OAuth + Responses API constants. These values mirror the official Codex
// CLI so the auth flow is recognized by auth.openai.com. The originator
// field identifies the client and is allow-listed per client_id.
const (
	clientID          = "app_EMoamEEZ73f0CkXaXp7hrann"
	oauthBase         = "https://auth.openai.com"
	oauthScope        = "openid profile email offline_access"
	defaultOriginator = "codex_cli_rs" // matches official Codex CLI; auth.openai.com allow-lists per client_id
	redirectURIHost   = "localhost"
	redirectPort      = 1455
	redirectPath      = "/auth/callback"
	responsesURL      = "https://chatgpt.com/backend-api/codex/responses"
)

func redirectURI() string {
	return fmt.Sprintf("http://%s:%d%s", redirectURIHost, redirectPort, redirectPath)
}

// pkcePair returns a (verifier, challenge) pair using the S256 method.
// Verifier is 64 bytes of url-safe random; challenge is its sha256 digest.
func pkcePair() (verifier, challenge string, err error) {
	buf := make([]byte, 64)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("codex: pkce random: %w", err)
	}
	verifier = base64.RawURLEncoding.EncodeToString(buf)
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return verifier, challenge, nil
}

// randomState returns a short opaque CSRF state token.
func randomState() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// authorizeURL builds the auth.openai.com /oauth/authorize URL with the
// extra params Codex CLI uses (id_token_add_organizations, simplified flow,
// originator).
func authorizeURL(state, challenge string) string {
	q := url.Values{
		"response_type":              {"code"},
		"client_id":                  {clientID},
		"redirect_uri":               {redirectURI()},
		"scope":                      {oauthScope},
		"state":                      {state},
		"code_challenge":             {challenge},
		"code_challenge_method":      {"S256"},
		"id_token_add_organizations": {"true"},
		"codex_cli_simplified_flow":  {"true"},
		"originator":                 {originator()},
	}
	return oauthBase + "/oauth/authorize?" + q.Encode()
}

// runBrowserFlow runs an end-to-end PKCE OAuth flow:
//  1. Open a local HTTP listener on 127.0.0.1:1455
//  2. Print + open the browser to the authorize URL
//  3. Wait for the redirect, validate state, exchange the code
//  4. Save the resulting tokens to ~/.shell3/codex_tokens.json
//
// Returns the saved tokens on success.
func runBrowserFlow(ctx context.Context, homeDir string, w io.Writer) (*Tokens, error) {
	verifier, challenge, err := pkcePair()
	if err != nil {
		return nil, err
	}
	state, err := randomState()
	if err != nil {
		return nil, fmt.Errorf("codex: state: %w", err)
	}

	// Bind both IPv4 and IPv6 loopback so the browser reaches us regardless
	// of how it resolves "localhost". A coexisting Codex CLI clone bound to
	// the other family can otherwise intercept the callback and reject it
	// with its own state-check.
	listener, err := dualStackLoopbackListener(redirectPort)
	if err != nil {
		return nil, fmt.Errorf("codex: bind localhost:%d (in use? `lsof -i :%d`): %w", redirectPort, redirectPort, err)
	}

	type result struct {
		tokens *Tokens
		err    error
	}
	resultCh := make(chan result, 1)

	mux := http.NewServeMux()
	mux.HandleFunc(redirectPath, func(rw http.ResponseWriter, req *http.Request) {
		q := req.URL.Query()
		if errParam := q.Get("error"); errParam != "" {
			msg := fmt.Errorf("codex: auth error %q: %s", errParam, q.Get("error_description"))
			writeErrorPage(rw, msg.Error())
			resultCh <- result{err: msg}
			return
		}
		if got := q.Get("state"); got != state {
			msg := fmt.Errorf("codex: state mismatch (csrf protection)")
			writeErrorPage(rw, msg.Error())
			resultCh <- result{err: msg}
			return
		}
		code := q.Get("code")
		if code == "" {
			msg := fmt.Errorf("codex: callback missing code")
			writeErrorPage(rw, msg.Error())
			resultCh <- result{err: msg}
			return
		}
		t, err := exchangeCode(req.Context(), code, verifier, redirectURI())
		if err != nil {
			writeErrorPage(rw, err.Error())
			resultCh <- result{err: err}
			return
		}
		if t.AccountID == "" {
			err := fmt.Errorf("codex: id_token has no chatgpt_account_id claim. Set SHELL3_DEBUG=1 to dump claims for diagnostics")
			if os.Getenv("SHELL3_DEBUG") != "" {
				err = fmt.Errorf("%v\nDecoded claims: %s", err, decodedIDTokenClaims(t.IDToken))
			}
			writeErrorPage(rw, err.Error())
			resultCh <- result{err: err}
			return
		}
		// Plan check intentionally not enforced here. Free plans can transact
		// with the Responses API (subject to server-side rate limits), so we
		// let the API surface 4xx rather than gatekeep up front based on a
		// claim that may not reflect current entitlement.
		writeSuccessPage(rw)
		resultCh <- result{tokens: t}
	})

	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := srv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			// Buffered channel: non-blocking send so we never deadlock if the
			// main goroutine has already returned.
			select {
			case resultCh <- result{err: fmt.Errorf("codex: callback server: %w", err)}:
			default:
			}
		}
	}()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	authURL := authorizeURL(state, challenge)
	fmt.Fprintln(w, "Sign in with ChatGPT.")
	fmt.Fprintln(w, "Opening browser. If it does not open, paste this URL:")
	fmt.Fprintln(w, "  "+authURL)
	fmt.Fprintln(w)
	if err := openBrowser(authURL); err != nil {
		fmt.Fprintf(w, "(could not auto-open browser: %v)\n", err)
	}
	fmt.Fprintln(w, "Waiting for callback…")

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-resultCh:
		if r.err != nil {
			return nil, r.err
		}
		if err := SaveTokens(homeDir, r.tokens); err != nil {
			return nil, err
		}
		fmt.Fprintf(w, "\nSigned in. Tokens saved to %s\n", tokensPath(homeDir))
		fmt.Fprintf(w, "Account: %s\n", r.tokens.AccountID)
		return r.tokens, nil
	}
}

// openBrowser launches the platform default browser to url.
// Best-effort; failure is non-fatal (user can paste the URL manually).
func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Start()
}

// writeSuccessPage shows a minimal "you can close this tab" page.
func writeSuccessPage(rw http.ResponseWriter) {
	rw.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = rw.Write([]byte(`<!doctype html><meta charset="utf-8"><title>shell3 signed in</title>
<body style="font-family:system-ui;padding:2rem"><h2>Signed in to shell3</h2>
<p>You can close this tab and return to your terminal.</p></body>`))
}

// writeErrorPage shows the auth failure to the user in-browser.
func writeErrorPage(rw http.ResponseWriter, msg string) {
	rw.Header().Set("Content-Type", "text/html; charset=utf-8")
	rw.WriteHeader(http.StatusBadRequest)
	_, _ = fmt.Fprintf(rw, `<!doctype html><meta charset="utf-8"><title>shell3 auth failed</title>
<body style="font-family:system-ui;padding:2rem"><h2>Sign-in failed</h2>
<pre>%s</pre><p>Return to your terminal and try again.</p></body>`, msg)
}

// dualStackLoopbackListener returns a net.Listener that accepts connections
// on both 127.0.0.1:port and [::1]:port. If only one family is available,
// it returns a listener on that one. Errors only when neither succeeds.
//
// Required because Go's default `net.Listen("tcp", ":port")` binds in a
// platform-dependent way (often dual-stack via a single socket), which can
// silently lose to a peer process that grabbed only one family first.
func dualStackLoopbackListener(port int) (net.Listener, error) {
	v4, err4 := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", port))
	v6, err6 := net.Listen("tcp6", fmt.Sprintf("[::1]:%d", port))
	switch {
	case err4 == nil && err6 == nil:
		return newMultiListener(v4, v6), nil
	case err4 == nil:
		return v4, nil
	case err6 == nil:
		return v6, nil
	default:
		return nil, fmt.Errorf("v4: %v; v6: %v", err4, err6)
	}
}

// multiListener accepts on any of its underlying listeners. Inbound
// connections are coalesced onto a single channel, so each Accept call
// receives the next ready connection from either underlying. Close shuts
// down both underlyings; the accept goroutines exit on their next Accept
// error and write their final result to the buffered channel without
// blocking, leaving no leaked goroutines once Close returns.
type multiListener struct {
	a, b net.Listener
	ch   chan acceptResult
	once sync.Once
}

type acceptResult struct {
	c   net.Conn
	err error
}

func newMultiListener(a, b net.Listener) *multiListener {
	m := &multiListener{
		a:  a,
		b:  b,
		ch: make(chan acceptResult, 64),
	}
	go m.pump(a)
	go m.pump(b)
	return m
}

func (m *multiListener) pump(l net.Listener) {
	for {
		c, err := l.Accept()
		select {
		case m.ch <- acceptResult{c, err}:
		default:
			// Channel full (caller stopped reading) — drop the connection
			// rather than block forever. Should not happen in practice with
			// a buffered channel of 64.
			if c != nil {
				_ = c.Close()
			}
		}
		if err != nil {
			return
		}
	}
}

func (m *multiListener) Accept() (net.Conn, error) {
	r, ok := <-m.ch
	if !ok {
		return nil, net.ErrClosed
	}
	return r.c, r.err
}

func (m *multiListener) Close() error {
	var err1, err2 error
	m.once.Do(func() {
		err1 = m.a.Close()
		err2 = m.b.Close()
	})
	if err1 != nil {
		return err1
	}
	return err2
}

func (m *multiListener) Addr() net.Addr { return m.a.Addr() }

// homeDir resolves the user's home directory or returns an error.
// Mirrors os.UserHomeDir but kept local so callers don't need to import os.
func homeDir() (string, error) {
	h, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("codex: resolve home dir: %w", err)
	}
	return h, nil
}
