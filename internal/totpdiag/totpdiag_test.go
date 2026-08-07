package totpdiag

import (
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

func testSecret(t *testing.T) string {
	t.Helper()
	key, err := totp.Generate(totp.GenerateOpts{Issuer: "shell3", AccountName: "test"})
	if err != nil {
		t.Fatal(err)
	}
	return key.Secret()
}

func TestCheckSecretAcceptsAFreshEnrolment(t *testing.T) {
	if err := CheckSecret(testSecret(t)); err != nil {
		t.Errorf("a freshly minted secret must check out: %v", err)
	}
}

func TestCheckSecretRejectsGarbage(t *testing.T) {
	if err := CheckSecret("not!base32@at#all"); err == nil {
		t.Error("a corrupt secret must be a finding")
	}
}

func TestSkewProbeFindsADriftedClock(t *testing.T) {
	secret := testSecret(t)
	now := time.Now()
	for _, drift := range []time.Duration{2 * time.Minute, -3 * time.Minute} {
		code, err := totp.GenerateCode(secret, now.Add(drift))
		if err != nil {
			t.Fatal(err)
		}
		off, ok := SkewProbe(secret, code, now)
		if !ok {
			t.Fatalf("a code minted %s away must probe as drift", drift)
		}
		// The probe walks whole periods, so the answer is exact to ±one period.
		if diff := off - drift; diff > period || diff < -period {
			t.Errorf("drift %s reported as %s", drift, off)
		}
	}
}

func TestSkewProbeStaysQuietOnARandomWrongCode(t *testing.T) {
	// A code minted for one secret can collide with one of the other secret's
	// ~38 probed windows by chance: odds ~38 in 10^6 per run. Accepted — a
	// failure here with fresh random secrets is overwhelmingly a real bug.
	secret := testSecret(t)
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	other := testSecret(t)
	if _, ok := SkewProbe(other, code, time.Now()); ok {
		t.Error("a code for a different secret must not read as drift")
	}
}

func TestSkewProbeStaysQuietInsideTheAcceptedWindow(t *testing.T) {
	secret := testSecret(t)
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if off, ok := SkewProbe(secret, code, time.Now()); ok {
		t.Errorf("an accepted-window code is not drift, probed as %s", off)
	}
}
