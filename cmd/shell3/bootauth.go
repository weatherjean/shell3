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
	"os"
	"strings"

	huh "charm.land/huh/v2"
	"github.com/mdp/qrterminal/v3"
	"github.com/pquerna/otp/totp"

	"github.com/weatherjean/shell3/internal/cli"
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
func newTOTPEnrolment(account string) (secret, uri string, err error) {
	key, err := totp.Generate(totp.GenerateOpts{Issuer: "shell3", AccountName: account})
	if err != nil {
		return "", "", err
	}
	return key.Secret(), key.URL(), nil
}

// webEnvPairs is what boot writes to .env for the interface. An absent second
// factor writes no key at all: an empty SHELL3_WEB_TOTP_SECRET= line would read
// as enrolled to anyone looking at the file.
func webEnvPairs(password, totpSecret string) [][2]string {
	pairs := [][2]string{{envWebPassword, password}}
	if totpSecret != "" {
		pairs = append(pairs, [][2]string{{envWebTOTP, totpSecret}}...)
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
	fmt.Fprintln(out, "\nScan this with your authenticator app:")
	qrterminal.GenerateHalfBlock(uri, qrterminal.L, out)
	fmt.Fprintf(out, "\nOr enter the secret by hand: %s\n", secret)
	fmt.Fprintln(out, "Then press enter — the code is asked for at every login.")
	if tty {
		fmt.Fscanln(os.Stdin)
	}
	return secret, nil
}

// printWebCredentials shows what was just written, because a generated password
// exists nowhere else yet. Printed once, to a terminal the operator is already
// looking at — never logged.
func printWebCredentials(password, totpSecret, envPath string) {
	fmt.Printf("\nInterface password: %s\n", password)
	fmt.Printf("  Save it now — it is stored only in %s\n", envPath)
	if totpSecret != "" {
		fmt.Println("  Second factor: on. Lost the phone? Delete " + envWebTOTP + " from .env and restart")
	}
}
