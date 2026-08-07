// Package totpdiag diagnoses TOTP failures without ever widening acceptance.
//
// Validation proper stays totp.Validate (±1 period) at the call sites; what
// lives here answers the question that follows a rejection — WHY did a code
// the operator swears is right not match? A code that validates at a wider
// clock offset means drift; a secret that cannot mint a code means the .env
// entry is corrupt. Both used to fail mute, which read as "the authenticator
// is flaky".
package totpdiag

import (
	"fmt"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

// period is the TOTP step every shell3 enrolment uses (the library default).
const period = 30 * time.Second

// probeMax is how far SkewProbe looks: ±10 minutes covers every plausible
// unsynced clock while staying instant to compute.
const probeMax = 10 * time.Minute

// CheckSecret reports whether secret can mint a code at all — the check
// health runs so a corrupt .env entry surfaces as a finding, not a lockout.
func CheckSecret(secret string) error {
	if _, err := totp.GenerateCode(secret, time.Now()); err != nil {
		return fmt.Errorf("TOTP secret does not decode (base32): %w", err)
	}
	return nil
}

// SkewProbe reports whether a REJECTED code would have validated at some
// clock offset beyond the accepted ±1-period window, and the approximate
// offset (positive = the code's clock runs ahead of this machine). Diagnosis
// only: the caller must already have refused the code, and nothing here makes
// it acceptable.
func SkewProbe(secret, code string, now time.Time) (offset time.Duration, drifted bool) {
	strict := totp.ValidateOpts{Period: uint(period / time.Second), Skew: 0,
		Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1}
	// Start at ±2 periods: 0 and ±1 are the accepted window, so a hit there
	// would mean the caller never actually rejected the code.
	for step := 2; time.Duration(step)*period <= probeMax; step++ {
		for _, sign := range []int{1, -1} {
			at := time.Duration(sign*step) * period
			ok, err := totp.ValidateCustom(code, secret, now.Add(at), strict)
			if err != nil {
				return 0, false // undecodable secret: CheckSecret's finding, not drift
			}
			if ok {
				return at, true
			}
		}
	}
	return 0, false
}
