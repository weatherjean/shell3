package codeagent

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

var stdinScanner = bufio.NewScanner(os.Stdin)

// ReadInput reads one line from the user.
// Returns io.EOF on ctrl+d or closed stdin.
func ReadInput() (string, error) {
	fmt.Print("\033[36m" + "prompt" + "\033[0m" + ": ")
	for stdinScanner.Scan() {
		text := strings.TrimSpace(stdinScanner.Text())
		if text != "" {
			return text, nil
		}
		fmt.Print("\033[36m" + "prompt" + "\033[0m" + ": ")
	}
	return "", io.EOF
}
