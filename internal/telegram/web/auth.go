//go:build unix

package web

import (
	"time"

	initdata "github.com/telegram-mini-apps/init-data-golang"
)

// verifyInitData validates the Telegram Mini App initData against the bot
// token and returns the embedded user id if it matches wantUser.
func verifyInitData(raw, botToken string, wantUser int64) (bool, int64) {
	if err := initdata.Validate(raw, botToken, time.Hour); err != nil {
		return false, 0
	}
	parsed, err := initdata.Parse(raw)
	if err != nil {
		return false, 0
	}
	uid := parsed.User.ID
	if uid != wantUser {
		return false, 0
	}
	return true, uid
}
