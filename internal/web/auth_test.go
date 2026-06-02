package web

import (
	"testing"
	"time"
)

func TestTokenRoundTrip(t *testing.T) {
	exp := time.Now().Add(time.Hour)
	tok := makeToken("secret", exp)
	if !validToken("secret", tok, time.Now()) {
		t.Fatal("fresh token should be valid")
	}
}

func TestTokenExpired(t *testing.T) {
	exp := time.Now().Add(-time.Minute)
	tok := makeToken("secret", exp)
	if validToken("secret", tok, time.Now()) {
		t.Fatal("expired token must be invalid")
	}
}

func TestTokenWrongPassword(t *testing.T) {
	tok := makeToken("secret", time.Now().Add(time.Hour))
	if validToken("different", tok, time.Now()) {
		t.Fatal("token must be invalid under a rotated password")
	}
}

func TestTokenTampered(t *testing.T) {
	tok := makeToken("secret", time.Now().Add(time.Hour)) + "x"
	if validToken("secret", tok, time.Now()) {
		t.Fatal("tampered token must be invalid")
	}
	if validToken("secret", "garbage", time.Now()) {
		t.Fatal("malformed token must be invalid")
	}
}

func TestCheckPassword(t *testing.T) {
	if !checkPassword("secret", "secret") {
		t.Fatal("matching password should pass")
	}
	if checkPassword("secret", "nope") {
		t.Fatal("wrong password should fail")
	}
}
