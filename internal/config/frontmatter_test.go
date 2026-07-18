package config

import (
	"strings"
	"testing"
)

func TestSplitFrontmatter(t *testing.T) {
	front, body, err := splitFrontmatter([]byte("---\ndescription: hi\n---\n\nBody line.\n"))
	if err != nil {
		t.Fatal(err)
	}
	if string(front) != "description: hi" {
		t.Fatalf("front = %q", front)
	}
	if body != "Body line.\n" {
		t.Fatalf("body = %q", body)
	}
}

func TestSplitFrontmatterCRLF(t *testing.T) {
	front, body, err := splitFrontmatter([]byte("---\r\nname: x\r\n---\r\nbody\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(front), "name: x") {
		t.Fatalf("front = %q", front)
	}
	if !strings.HasPrefix(body, "body") {
		t.Fatalf("body = %q", body)
	}
}

func TestSplitFrontmatterBodyMayContainDashes(t *testing.T) {
	_, body, err := splitFrontmatter([]byte("---\na: 1\n---\nfirst\n---\nsecond\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "---") {
		t.Fatalf("body lost inner ---: %q", body)
	}
}

func TestSplitFrontmatterErrors(t *testing.T) {
	for name, in := range map[string]string{
		"empty":        "",
		"no-fence":     "just text\n",
		"unterminated": "---\na: 1\nbody\n",
	} {
		if _, _, err := splitFrontmatter([]byte(in)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}
