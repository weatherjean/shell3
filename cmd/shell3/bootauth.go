//go:build unix

package main

// The authentication half of `shell3 boot`: the password the interface is
// reached with, and the optional second factor.
//
// Both are secrets, so both go to .env under the names shell3.yaml references.
// A login here is a shell, which is why the password floor is 16 characters and
// why boot offers to generate one rather than trusting an invented one.

import (
	"crypto/rand"
	"fmt"
	"io"
	"math/big"
	"strings"
	"time"

	huh "charm.land/huh/v2"
	"github.com/mdp/qrterminal/v3"
	"github.com/pquerna/otp/totp"

	"github.com/weatherjean/shell3/internal/cli"
	"github.com/weatherjean/shell3/internal/totpdiag"
)

// passwordAlphabet is unambiguous by design: a password read off a terminal and
// typed into a phone should not hinge on telling l from 1 or O from 0.
const passwordAlphabet = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// generatedPasswordLength is comfortably past the floor: this is a password
// meant to be pasted into a manager, not memorised.
const generatedPasswordLength = 24

// validateWebPassword is the rule boot enforces on a typed password. Length
// only: composition rules push people towards predictable substitutions, while
// length is what actually resists guessing.
func validateWebPassword(password string) error {
	if strings.TrimSpace(password) == "" {
		return fmt.Errorf("the interface cannot be served without a password")
	}
	if n := len([]rune(password)); n < minPasswordLength {
		return fmt.Errorf("at least %d characters, please — this password guards a shell (got %d)",
			minPasswordLength, n)
	}
	return nil
}

// generateWebPassword draws a random password from the unambiguous alphabet.
func generateWebPassword() (string, error) {
	var b strings.Builder
	for range generatedPasswordLength {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(passwordAlphabet))))
		if err != nil {
			return "", err
		}
		b.WriteByte(passwordAlphabet[n.Int64()])
	}
	return b.String(), nil
}

// newTOTPEnrolment mints a second-factor secret and the otpauth:// URI an
// authenticator app scans.
//
// The account label carries the minting time. Every enrolment is a NEW
// secret, and an authenticator that already holds an identically-named entry
// (a previous boot, an earlier install) can collide the scan into it — the
// operator then reads codes off the stale entry, which can never match, and
// nothing on either side says why. A label unique per run makes the fresh
// entry unmistakable and leaves any stale twin visibly stale.
func newTOTPEnrolment(account string) (secret, uri string, err error) {
	label := account + " " + time.Now().Format("2006-01-02 15:04:05")
	key, err := totp.Generate(totp.GenerateOpts{Issuer: "shell3", AccountName: label})
	if err != nil {
		return "", "", err
	}
	return key.Secret(), key.URL(), nil
}

// webEnvPairs is what boot writes to .env for the interface. An empty value
// writes no key at all: a blank SHELL3_WEB_TOTP_SECRET= line would read as
// enrolled to anyone looking at the file, and a blank password means the
// existing .env entry is being kept.
func webEnvPairs(password, totpSecret string) [][2]string {
	var pairs [][2]string
	if password != "" {
		pairs = append(pairs, [2]string{envWebPassword, password})
	}
	if totpSecret != "" {
		pairs = append(pairs, [2]string{envWebTOTP, totpSecret})
	}
	return pairs
}

// askWebPassword asks for the interface password, offering a generated one. The
// generated value is pre-filled rather than imposed, so accepting it is one
// keypress and replacing it is just typing.
func askWebPassword(tty bool) (string, error) {
	generated, err := generateWebPassword()
	if err != nil {
		return "", err
	}
	if !tty {
		// Headless: a generated password is the only safe default, and boot
		// prints it below so it is not lost.
		return generated, nil
	}

	password := generated
	err = huh.NewForm(huh.NewGroup(
		huh.NewInput().
			Title("Interface password").
			Description(fmt.Sprintf("Reaching the interface means reaching a shell, so this is\n"+
				"the whole boundary. %d characters minimum; the suggestion is random.", minPasswordLength)).
			Value(&password).
			Validate(validateWebPassword),
	).Title("Web interface")).WithTheme(cli.HuhTheme()).Run()
	if err != nil {
		return "", err
	}
	return password, nil
}

// askTOTPEnrolment offers a second factor and, if taken, prints the QR code to
// scan. Returns "" when declined.
//
// Losing the phone is not a lockout here: the secret is a line in .env on this
// machine, so recovery is deleting it and restarting. That is what makes a
// second factor cheap enough to offer at all.
func askTOTPEnrolment(tty bool, account string, out io.Writer) (string, error) {
	if !tty {
		return "", nil
	}
	enrol := true
	if err := huh.NewForm(huh.NewGroup(
		huh.NewConfirm().
			Title("Add a second factor (an authenticator-app code)?").
			Description("A leaked or guessed password then is not enough to get in.\n" +
				"Not now? `shell3 boot --totp` enrols (or resets) any time.\n" +
				"Lost your phone? Delete the secret from .env and restart.").
			Value(&enrol),
	).Title("Two-factor")).WithTheme(cli.HuhTheme()).Run(); err != nil {
		return "", err
	}
	if !enrol {
		return "", nil
	}

	secret, uri, err := newTOTPEnrolment(account)
	if err != nil {
		return "", err
	}
	printTOTPEnrolment(secret, uri, out)
	verified, err := verifyTOTPEnrolment(secret)
	if err != nil {
		return "", err
	}
	if !verified {
		fmt.Fprintln(out, "\nEnrolment cancelled — no second factor. `shell3 boot --totp` enrols any time.")
		return "", nil
	}
	return secret, nil
}

// totpCodeValidator validates one enrolment-confirmation code against secret.
// Blank is accepted — it is the "cancel enrolment" escape — and whitespace is
// tolerated because codes get typed off a phone screen.
//
// A rejection diagnoses itself: a code that validates at a wider clock offset
// names the drift, and repeated failures name the other cause that "wait for
// the next code" can never cure — codes read off a stale authenticator entry
// from an earlier enrolment. Diagnosis only; nothing here widens acceptance.
func totpCodeValidator(secret string) func(string) error {
	failures := 0
	return func(code string) error {
		code = strings.TrimSpace(code)
		if code == "" {
			return nil
		}
		if totp.Validate(code, secret) {
			return nil
		}
		if offset, drifted := totpdiag.SkewProbe(secret, code, time.Now()); drifted {
			return fmt.Errorf("that code is minted about %s away from this machine's clock — sync the phone's or this machine's time and try the next code", offset)
		}
		failures++
		if failures >= 2 {
			return fmt.Errorf("still no match — a code from an entry the app already had can never match this enrolment: delete every shell3 entry in the app, rescan THIS QR, and use the new entry's code")
		}
		return fmt.Errorf("that code does not match — wait for the app's next one and try again")
	}
}

// verifyTOTPEnrolment asks for one code from the freshly scanned entry and
// reports whether it verified. This is what catches a mis-scan, a stale
// authenticator entry, or a skewed clock at enrolment time — before the
// factor is armed — instead of at the login screen with no better error than
// "that did not work". Blank input cancels (returns false, nil).
func verifyTOTPEnrolment(secret string) (bool, error) {
	code := ""
	if err := huh.NewForm(huh.NewGroup(
		huh.NewInput().
			Title("Enter the 6-digit code your app shows now").
			Description("Confirms the scan before the second factor is armed.\nLeave blank to cancel enrolment.").
			Validate(totpCodeValidator(secret)).
			Value(&code),
	).Title("Verify")).WithTheme(cli.HuhTheme()).Run(); err != nil {
		return false, err
	}
	return strings.TrimSpace(code) != "", nil
}

// printTOTPEnrolment shows the QR code and manual secret for a fresh
// enrolment — shared by the boot offer and `boot --totp`.
func printTOTPEnrolment(secret, uri string, out io.Writer) {
	fmt.Fprintln(out, "\nScan this with your authenticator app:")
	qrterminal.GenerateHalfBlock(uri, qrterminal.L, out)
	fmt.Fprintf(out, "\nOr enter the secret by hand: %s\n", secret)
	fmt.Fprintln(out, "Then type the code it shows to confirm — the code is asked for at every login.")
}

// printWebCredentials shows what was just written, because a generated password
// exists nowhere else yet. Printed once, to a terminal the operator is already
// looking at — never logged. An empty value was kept from .env, announced when
// the decision was made — nothing to show here.
func printWebCredentials(password, totpSecret, envPath string) {
	if password != "" {
		fmt.Printf("\nInterface password: %s\n", password)
		fmt.Printf("  Save it now — it is stored only in %s\n", envPath)
	}
	if totpSecret != "" {
		fmt.Println("  Second factor: on. Lost the phone? Delete " + envWebTOTP + " from .env and restart")
	}
}
