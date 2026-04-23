package codeagent

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

var stdinScanner = bufio.NewScanner(os.Stdin)

const (
	hint      = "\033[2m  enter to send · ctrl+c to exit\033[0m"
	eraseHint = "\033[2A\033[2K\033[2B" // up 2 (to hint line), erase, down 2 (back past user input line)
)

// ReadInput reads one line from the user.
// Shows a dim hint below the prompt label; erases it on submit.
// Returns io.EOF on ctrl+d or closed stdin.
func ReadInput() (string, error) {
	printPrompt()
	for stdinScanner.Scan() {
		fmt.Print(eraseHint)
		text := strings.TrimSpace(stdinScanner.Text())
		if text != "" {
			return text, nil
		}
		printPrompt()
	}
	return "", io.EOF
}

func printPrompt() {
	fmt.Print("\033[36mprompt:\033[0m\n")
	fmt.Println(hint)
}
