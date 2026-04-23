package codeagent

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// ReadInput reads one line from the user.
// Uses gum input if available, plain readline otherwise.
// Returns io.EOF when user wants to exit (ctrl+c or empty gum cancel).
func ReadInput() (string, error) {
	if _, err := exec.LookPath("gum"); err == nil {
		return readGum()
	}
	return readPlain()
}

func readGum() (string, error) {
	out, err := exec.Command("gum", "input", "--placeholder", "Ask shell3...").Output()
	if err != nil {
		return "", io.EOF
	}
	text := strings.TrimSpace(string(out))
	if text == "" {
		return "", io.EOF
	}
	return text, nil
}

func readPlain() (string, error) {
	fmt.Print("> ")
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			return readPlain()
		}
		return text, nil
	}
	return "", io.EOF
}
