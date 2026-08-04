//go:build unix

package webui

// Authentication. A login here is a shell — the agent's first verb is `bash`,
// unsandboxed by default, and the config dir it can read holds every provider
// key. So this is not the usual "protect the user's data" gate: an
// unauthenticated request is remote code execution, and the design is
// calibrated to that.
//
// One shared password (there is one operator), kept in .env and reaching the
// YAML as an env: reference, exchanged at a login screen for a server-side
// session. An optional TOTP secret adds a second factor, so a leaked password
// alone is not a session.

import (
	"crypto/subtle"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/pquerna/otp/totp"
)

const sessionCookie = "shell3_session"

const (
	// baseLoginDelay is how long the first failed attempt waits before
	// answering; each further consecutive failure doubles it.
	baseLoginDelay = 250 * time.Millisecond
	// maxLoginDelay caps the wait. Deliberately not a lockout: a lockout would
	// let anyone who can reach the login route hold it closed against the
	// operator, and guessing is already covered by TOTP where it is configured.
	maxLoginDelay = 5 * time.Second
)

// authGate holds what the login route needs across requests: the failure
// counter behind the backoff, and the last accepted TOTP code so it cannot be
// replayed inside its window. Both are in memory only — restarting serve clears
// them, which is a reasonable escape hatch for whoever owns the host.
type authGate struct {
	mu             sync.Mutex
	consecutiveBad int
	lastCode       string
}

// failures reports the current consecutive-failure count.
func (g *authGate) failures() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.consecutiveBad
}

// loginDelay is the wait before answering a failed attempt: exponential in the
// number of consecutive failures, capped. Zero when nothing has failed.
func loginDelay(failures int) time.Duration {
	if failures <= 0 {
		return 0
	}
	delay := baseLoginDelay
	for range failures - 1 {
		delay *= 2
		if delay >= maxLoginDelay {
			return maxLoginDelay
		}
	}
	return delay
}

// password is the configured web password, read fresh so a /reload picks up a
// change. Empty means no gate: `shell3 serve` refuses to start in that state,
// so it is reachable only from library and test use.
func (s *Server) password() string { return s.rt.Web().Password }

// totpSecret is the configured second-factor secret, "" when there is none.
func (s *Server) totpSecret() string { return s.rt.Web().TOTPSecret }

// authorized reports whether a request carries a live session created under the
// current password.
func (s *Server) authorized(r *http.Request) bool {
	pw := s.password()
	if pw == "" {
		return true // unconfigured: no gate to pass
	}
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		return false
	}
	return s.sessions.valid(cookie.Value, fingerprintPassword(pw))
}

// requireSession wraps a handler so it is unreachable without a session.
func (s *Server) requireSession(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.authorized(r) {
			next(w, r)
			return
		}
		// 401 rather than a redirect: every gated route here is either an API
		// call or a resource the app fetches, and both handle a status better
		// than they handle a page. The login screen is drawn by the public
		// shell at /, so a person who lands on any URL still gets a way in.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "authentication required"})
	}
}

// loginRequest is what the login screen posts. Code is absent on the first
// attempt: the screen does not know whether this install has a second factor
// until the server says so.
type loginRequest struct {
	Password string `json:"password"`
	Code     string `json:"code"`
}

// handleLogin verifies the password (and the code, when configured) and issues
// a session. Every failure answers identically, so the route cannot be used to
// discover that a password was right — except for needCode, which is only ever
// returned once the password has already verified.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var req loginRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		s.denyLogin(w, r, "malformed request", false)
		return
	}

	pw := s.password()
	if pw == "" {
		// Nothing to authenticate against. serve refuses to start here, so this
		// is library use; say so rather than pretending a login happened.
		http.Error(w, "no web password configured", http.StatusNotImplemented)
		return
	}

	// Constant time: a comparison that returns early leaks the password's
	// prefix to anyone who can measure the response.
	if subtle.ConstantTimeCompare([]byte(req.Password), []byte(pw)) != 1 {
		s.denyLogin(w, r, "wrong password", false)
		return
	}

	if secret := s.totpSecret(); secret != "" {
		if req.Code == "" {
			// The password was right, so this reveals nothing an attacker with
			// the password could not discover by trying. It is how the screen
			// knows to show the field: /api/capabilities is gated and therefore
			// unreadable at login time.
			s.denyLogin(w, r, "code required", true)
			return
		}
		if !s.acceptCode(secret, req.Code) {
			s.denyLogin(w, r, "wrong or reused code", false)
			return
		}
	}

	token, err := s.sessions.issue(fingerprintPassword(pw), deviceLabel(r))
	if err != nil {
		s.log.Error("webui: issuing a session", err)
		http.Error(w, "could not start a session", http.StatusInternalServerError)
		return
	}

	s.auth.mu.Lock()
	s.auth.consecutiveBad = 0
	s.auth.mu.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(sessionTTL),
		MaxAge:   int(sessionTTL / time.Second),
	})

	// The log keeps the user-agent verbatim: the notification wants something
	// readable, an audit trail wants what was actually sent.
	s.log.Info("webui: signed in", "ip", clientIP(r), "device", deviceLabel(r),
		"agent", rawAgent(r))
	// The operator's only chance of noticing a breach: a sign-in they did not
	// perform shows up in the bell and in web push within seconds.
	s.publishNotification(notification{
		Kind:  "alert",
		Title: "Signed in",
		Body:  deviceLabel(r) + " from " + clientIP(r),
	})

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

// denyLogin answers a failed attempt: logged, slowed, and — apart from
// needCode — indistinguishable from every other kind of failure.
func (s *Server) denyLogin(w http.ResponseWriter, r *http.Request, reason string, needCode bool) {
	s.auth.mu.Lock()
	if !needCode {
		s.auth.consecutiveBad++
	}
	failures := s.auth.consecutiveBad
	s.auth.mu.Unlock()

	if !needCode {
		// Logged with the reason the server saw; the client is told nothing.
		// Without this an intrusion attempt would leave no trace at all.
		s.log.Warn("webui: login refused", "reason", reason, "ip", clientIP(r),
			"agent", rawAgent(r), "failures", failures)
		time.Sleep(loginDelay(failures))
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	if needCode {
		_ = json.NewEncoder(w).Encode(map[string]bool{"needCode": true})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"error": "that did not work"})
}

// acceptCode validates a TOTP code and burns it, so a code observed in flight
// cannot be used again inside its 30-second window.
func (s *Server) acceptCode(secret, code string) bool {
	s.auth.mu.Lock()
	defer s.auth.mu.Unlock()
	if code == s.auth.lastCode {
		return false
	}
	if !totp.Validate(code, secret) {
		return false
	}
	s.auth.lastCode = code
	return true
}

// handleLogout revokes this browser's session and clears its cookie.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		s.sessions.revoke(cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

// isHTTPS reports whether the browser's connection is encrypted, including the
// proxied case where TLS terminates upstream and says so in a header.
func isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// clientIP is the caller's address, preferring what a proxy reports. Used for
// the log and the sign-in notice only, never for a security decision: a header
// is the client's to set.
func clientIP(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		if first, _, ok := strings.Cut(fwd, ","); ok {
			return strings.TrimSpace(first)
		}
		return strings.TrimSpace(fwd)
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// rawAgent is the user-agent as sent, for the audit log. Truncated but not
// interpreted: what was actually claimed is the point.
func rawAgent(r *http.Request) string {
	agent := r.UserAgent()
	if agent == "" {
		return "(none)"
	}
	if len(agent) > 160 {
		agent = agent[:160]
	}
	return agent
}

// browsers and platforms are matched in order, so the more specific entry comes
// first: every Chrome user-agent also claims Safari, and an iPhone also says Mac
// OS X.
var browsers = []struct{ token, name string }{
	{"Edg/", "Edge"},
	{"OPR/", "Opera"},
	{"Chrome/", "Chrome"},
	{"Firefox/", "Firefox"},
	{"Safari/", "Safari"},
}

var platforms = []struct{ token, name string }{
	{"iPhone", "iPhone"},
	{"iPad", "iPad"},
	{"Android", "Android"},
	{"Macintosh", "macOS"},
	{"Windows", "Windows"},
	{"Linux", "Linux"},
}

// deviceLabel names the browser that logged in, for the sign-in notice and the
// session record. Summarised rather than verbatim: this is glanced at, and a raw
// user-agent buries "someone signed in" under four lines of version numbers.
//
// An unrecognised client keeps its own name — guessing would be worse than
// verbose — truncated, because a user-agent is text the client chose.
func deviceLabel(r *http.Request) string {
	agent := r.UserAgent()
	if agent == "" {
		return "unknown device"
	}
	for _, b := range browsers {
		if !strings.Contains(agent, b.token) {
			continue
		}
		for _, p := range platforms {
			if strings.Contains(agent, p.token) {
				return b.name + " on " + p.name
			}
		}
		return b.name
	}
	if len(agent) > 80 {
		agent = agent[:80]
	}
	return agent
}
