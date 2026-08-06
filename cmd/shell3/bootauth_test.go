//go:build unix

package main

import (
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

func TestEnvKeySet(t *testing.T) {
	keys := envKeySet("# comment\nA=1\nexport B=2\n\nMALFORMED\nC=3")
	for _, want := range []string{"A", "B", "C"} {
		if !keys[want] {
			t.Errorf("missing key %q in %v", want, keys)
		}
	}
	if keys["MALFORMED"] || keys["# comment"] {
		t.Errorf("junk keys leaked: %v", keys)
	}
}

func TestTOTPCodeValidator(t *testing.T) {
	secret, _, err := newTOTPEnrolment("test")
	if err != nil {
		t.Fatal(err)
	}
	validate := totpCodeValidator(secret)

	if err := validate(""); err != nil {
		t.Errorf("blank (cancel) must be accepted: %v", err)
	}
	if err := validate("000000"); err == nil {
		t.Error("a wrong code must be rejected")
	}
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := validate(code); err != nil {
		t.Errorf("the current code must verify: %v", err)
	}
	if err := validate("  " + code + " "); err != nil {
		t.Errorf("surrounding whitespace must be tolerated: %v", err)
	}
}

func TestWebEnvPairsSkipsEmpty(t *testing.T) {
	if pairs := webEnvPairs("", ""); len(pairs) != 0 {
		t.Errorf("nothing to write, got %v", pairs)
	}
	pairs := webEnvPairs("pw", "")
	if len(pairs) != 1 || pairs[0][0] != envWebPassword {
		t.Errorf("password only, got %v", pairs)
	}
	pairs = webEnvPairs("pw", "sec")
	if len(pairs) != 2 || pairs[1][0] != envWebTOTP {
		t.Errorf("password+totp, got %v", pairs)
	}
	pairs = webEnvPairs("", "sec")
	if len(pairs) != 1 || pairs[0][0] != envWebTOTP {
		t.Errorf("totp only (password kept in .env), got %v", pairs)
	}
}
