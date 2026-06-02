package web

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
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
