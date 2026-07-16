//go:build unix

package web

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestPopupCommandsMatchHelp pins the frontend's COMMANDS autocomplete list to
// the server's helpText. The popup is a hand-maintained mirror (like
// telegram.BotCommands beside handleCommand); drift ships a popup offering
// commands the server rejects, or hiding commands /help lists.
func TestPopupCommandsMatchHelp(t *testing.T) {
	html, err := os.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	var popup []string
	for _, m := range regexp.MustCompile(`cmd: "(/[a-z]+)"`).FindAllStringSubmatch(string(html), -1) {
		popup = append(popup, m[1])
	}
	var help []string
	for _, line := range strings.Split(helpText, "\n") {
		if strings.HasPrefix(line, "/") {
			help = append(help, strings.Fields(line)[0])
		}
	}
	sort.Strings(popup)
	sort.Strings(help)
	if len(popup) == 0 || strings.Join(popup, " ") != strings.Join(help, " ") {
		t.Fatalf("popup COMMANDS and command.go helpText drifted:\n  popup: %v\n  help:  %v", popup, help)
	}
}
