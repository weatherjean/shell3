package edittool

import (
	"strings"
	"testing"
)

func FuzzReplace(f *testing.F) {
	f.Add("hello world", "world", "go", false)
	f.Add("a\nb\nc\n", "b", "B", true)
	f.Add("  indented line\n", "indented line", "x", false)
	f.Add("dup\ndup\n", "dup", "y", false)
	f.Add("", "", "", false)

	f.Fuzz(func(t *testing.T, content, oldString, newString string, all bool) {
		got, err := replace(content, oldString, newString, all)

		idx := strings.Index(content, oldString)
		if !all && oldString != "" && oldString != newString && idx != -1 && idx == strings.LastIndex(content, oldString) {
			if err != nil {
				t.Fatalf("single verbatim occurrence should replace cleanly: err=%v (content=%q old=%q)", err, content, oldString)
			}
			if want := content[:idx] + newString + content[idx+len(oldString):]; got != want {
				t.Fatalf("exact replace mismatch:\n got=%q\nwant=%q", got, want)
			}
		}
	})
}
