//go:build unix

package web

import (
	"crypto/subtle"
	"net/http"
	"time"

	initdata "github.com/telegram-mini-apps/init-data-golang"
)

// AuthFunc authorizes one dashboard/chat API request. The server rejects a
// request with 401 when it returns false.
type AuthFunc func(r *http.Request) bool

// TelegramAuth accepts requests carrying valid Telegram Mini App initData
// (X-Init-Data header or ?initData= param) signed by botToken for chatID.
func TelegramAuth(botToken string, chatID int64) AuthFunc {
	return func(r *http.Request) bool {
		raw := r.Header.Get("X-Init-Data")
		if raw == "" {
			raw = r.URL.Query().Get("initData")
		}
		return verifyInitData(raw, botToken, chatID)
	}
}

// TokenAuth accepts requests whose X-Auth-Token header (or ?key= param)
// equals secret, compared in constant time. An empty secret rejects
// everything — "no secret configured" must never mean "open".
func TokenAuth(secret string) AuthFunc {
	return func(r *http.Request) bool {
		if secret == "" {
			return false
		}
		got := r.Header.Get("X-Auth-Token")
		if got == "" {
			got = r.URL.Query().Get("key")
		}
		return subtle.ConstantTimeCompare([]byte(got), []byte(secret)) == 1
	}
}

// NoAuth accepts everything. FOR LOCAL DEV ONLY (shell3 dash): it exposes
// history, files, and runs unauthenticated, so the caller MUST bind the
// server to localhost. Never use it on a publicly reachable address.
func NoAuth() AuthFunc { return func(*http.Request) bool { return true } }

// verifyInitData validates the Telegram Mini App initData against the bot
// token and checks the embedded user id matches wantUser.
func verifyInitData(raw, botToken string, wantUser int64) bool {
	if err := initdata.Validate(raw, botToken, time.Hour); err != nil {
		return false
	}
	parsed, err := initdata.Parse(raw)
	if err != nil {
		return false
	}
	return parsed.User.ID == wantUser
}
