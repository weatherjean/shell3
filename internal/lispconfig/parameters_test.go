package lispconfig

import (
	"fmt"
	"strings"
	"testing"
)

func TestParameterNamesCannotShadowBindings(t *testing.T) {
	for _, name := range []string{"workdir", "prompt-file", "result-file", "using", "task-attempt", "fixed"} {
		source := fmt.Sprintf(`(shell3 (version 1) (define fixed "constant") (runner fake (parameters (%s string optional "value")) (command "/bin/sh") (result stdout)))`, name)
		_, err := Parse("test.lisp", []byte(source))
		if err == nil || (!strings.Contains(err.Error(), "reserved") && !strings.Contains(err.Error(), "conflicts")) {
			t.Fatalf("name=%s error=%v", name, err)
		}
	}
}
