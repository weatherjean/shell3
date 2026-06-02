package web

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const cookieName = "shell3_session"

// signKey derives the HMAC key from the password; rotating the password
// invalidates every previously issued cookie.
func signKey(password string) []byte {
	sum := sha256.Sum256([]byte(password))
	return sum[:]
}

// makeToken returns a signed cookie value that expires at exp.
func makeToken(password string, exp time.Time) string {
	payload := strconv.FormatInt(exp.Unix(), 10)
	mac := hmac.New(sha256.New, signKey(password))
	mac.Write([]byte(payload))
	sig := hex.EncodeToString(mac.Sum(nil))
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + sig
}

// validToken reports whether tok is correctly signed for password and not yet
// expired as of now.
func validToken(password, tok string, now time.Time) bool {
	parts := strings.SplitN(tok, ".", 2)
	if len(parts) != 2 {
		return false
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return false
	}
	payload := string(payloadBytes)
	mac := hmac.New(sha256.New, signKey(password))
	mac.Write([]byte(payload))
	want := hex.EncodeToString(mac.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(want), []byte(parts[1])) != 1 {
		return false
	}
	exp, err := strconv.ParseInt(payload, 10, 64)
	if err != nil {
		return false
	}
	return now.Before(time.Unix(exp, 0))
}

// checkPassword constant-time compares the submitted password to configured.
func checkPassword(configured, submitted string) bool {
	return subtle.ConstantTimeCompare([]byte(configured), []byte(submitted)) == 1
}

// authGuard enforces the session cookie. Disabled entirely when no password is
// configured. /login and /logout are always reachable.
func (s *Server) authGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.Password == "" {
			next.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/login" || r.URL.Path == "/logout" {
			next.ServeHTTP(w, r)
			return
		}
		if s.authenticated(r) {
			next.ServeHTTP(w, r)
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/" {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	})
}

// originGuard validates Host on every request and Origin on state-changing
// POSTs. Disabled when AllowedOrigins is empty (e.g. in tests / unconfigured).
func (s *Server) originGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(s.cfg.AllowedOrigins) == 0 {
			next.ServeHTTP(w, r)
			return
		}
		if !s.cfg.hostAllowed(r.Host) {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		if r.Method == http.MethodPost && !s.cfg.originAllowed(r.Header.Get("Origin")) {
			http.Error(w, "forbidden origin", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) authenticated(r *http.Request) bool {
	ck, err := r.Cookie(cookieName)
	if err != nil {
		return false
	}
	return validToken(s.cfg.Password, ck.Value, time.Now())
}

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Password == "" || s.authenticated(r) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(loginHTML)
}

func (s *Server) handleLoginSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if s.cfg.Password == "" || !checkPassword(s.cfg.Password, r.PostFormValue("password")) {
		http.Redirect(w, r, "/login?e=1", http.StatusSeeOther)
		return
	}
	exp := time.Now().Add(s.cfg.CookieTTL)
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    makeToken(s.cfg.Password, exp),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(s.cfg.CookieTTL.Seconds()),
	})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
