package config

import (
	"strings"
	"testing"
)

func FuzzParseDotEnvValue(f *testing.F) {
	f.Add(`plain`)
	f.Add(`"quoted value"`)
	f.Add(`value # comment`)
	f.Add(`"value # not a comment"`)
	f.Add(`'single # quoted'`)
	f.Add(``)
	f.Add(`#`)

	f.Fuzz(func(t *testing.T, v string) {
		_ = parseDotEnvValue(v)

		if !strings.Contains(v, "#") {
			if got := stripInlineComment(v); got != v {
				t.Fatalf("stripInlineComment altered a value with no '#':\n in=%q\nout=%q", v, got)
			}
		}

		trimmed := strings.TrimSpace(v)
		wrapped := len(trimmed) >= 2 &&
			(trimmed[0] == '"' || trimmed[0] == '\'') && trimmed[len(trimmed)-1] == trimmed[0]
		if v == trimmed && !strings.Contains(v, "#") && !wrapped {
			if got := parseDotEnvValue(v); got != v {
				t.Fatalf("parseDotEnvValue altered a clean value:\n in=%q\nout=%q", v, got)
			}
		}
	})
}
